#include <stdio.h>
#include <string.h>
#include <math.h>
#include <stdlib.h>
#include <sys/time.h>

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/event_groups.h"
#include "freertos/semphr.h"

#include "esp_system.h"
#include "esp_wifi.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_mac.h"
#include "nvs_flash.h"
#include "esp_netif.h"
#include "lwip/sockets.h"
#include "lwip/inet.h"
#include "ping/ping_sock.h"

static const char *TAG = "csi_fw";

#define WIFI_CONNECTED_BIT BIT0
#define WIFI_FAIL_BIT      BIT1

#define MAX_SUBCARRIERS    32
#define CSI_DECIMATE       2
#define CSI_STIM_MS        60

#define LNK_PEER_CAPTURE   1
#define LNK_MAX_MACS       8
#define LNK_MAX_SAMPLES    32
#define LNK_WINDOW_MS      1000
#define MAC_LEN            6

typedef struct {
    uint32_t seq;
    uint64_t ts_ms;
    uint8_t  count;
    uint8_t  amp[MAX_SUBCARRIERS];
    int16_t  phase[MAX_SUBCARRIERS];
    int8_t   rssi;
    bool     has_rssi;
} csi_frame_t;

typedef struct {
    uint8_t  mac[MAC_LEN];
    bool     used;
    uint8_t  count;
    uint8_t  next;
    uint64_t last_ms;
    int8_t   rssi[LNK_MAX_SAMPLES];
} lnk_entry_t;

typedef enum {
    SOURCE_AP,
    SOURCE_AP_UNVERIFIED,
    SOURCE_OTHER,
} frame_source_t;

static EventGroupHandle_t s_wifi_event_group;
static QueueHandle_t s_csi_queue;
static SemaphoreHandle_t s_print_mu;
static uint32_t s_csi_seq;
static uint32_t s_csi_skip;
static int s_retry_num;
static int s_udp_sock = -1;
static struct sockaddr_in s_udp_dest;
static portMUX_TYPE s_lnk_lock = portMUX_INITIALIZER_UNLOCKED;
static lnk_entry_t s_lnk[LNK_MAX_MACS];
static uint8_t s_bssid[MAC_LEN];
static bool s_bssid_known;
static uint8_t s_self_mac[MAC_LEN];

static uint64_t now_ms(void)
{
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return (uint64_t)tv.tv_sec * 1000ULL + (uint64_t)tv.tv_usec / 1000ULL;
}

static void bssid_refresh(void)
{
    wifi_ap_record_t ap_info;
    esp_err_t err = esp_wifi_sta_get_ap_info(&ap_info);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "BSSID unavailable: %s", esp_err_to_name(err));
        return;
    }
    portENTER_CRITICAL(&s_lnk_lock);
    memcpy(s_bssid, ap_info.bssid, MAC_LEN);
    s_bssid_known = true;
    portEXIT_CRITICAL(&s_lnk_lock);
}

static frame_source_t classify_source(const uint8_t *mac)
{
    portENTER_CRITICAL(&s_lnk_lock);
    bool known = s_bssid_known;
    bool match = known && memcmp(mac, s_bssid, MAC_LEN) == 0;
    portEXIT_CRITICAL(&s_lnk_lock);
    if (!known) {
        return SOURCE_AP_UNVERIFIED;
    }
    return match ? SOURCE_AP : SOURCE_OTHER;
}

static lnk_entry_t *lnk_find_locked(const uint8_t *mac)
{
    for (int i = 0; i < LNK_MAX_MACS; i++) {
        if (s_lnk[i].used && memcmp(s_lnk[i].mac, mac, MAC_LEN) == 0) {
            return &s_lnk[i];
        }
    }
    return NULL;
}

static lnk_entry_t *lnk_claim_locked(const uint8_t *mac)
{
    lnk_entry_t *victim = NULL;
    for (int i = 0; i < LNK_MAX_MACS; i++) {
        if (!s_lnk[i].used) {
            victim = &s_lnk[i];
            break;
        }
        bool is_bssid = s_bssid_known && memcmp(s_lnk[i].mac, s_bssid, MAC_LEN) == 0;
        if (!is_bssid && (victim == NULL || s_lnk[i].last_ms < victim->last_ms)) {
            victim = &s_lnk[i];
        }
    }
    if (victim == NULL) {
        return NULL;
    }
    memset(victim, 0, sizeof(*victim));
    memcpy(victim->mac, mac, MAC_LEN);
    victim->used = true;
    return victim;
}

static void lnk_record(const uint8_t *mac, int8_t rssi)
{
    uint64_t now = now_ms();
    portENTER_CRITICAL(&s_lnk_lock);
    lnk_entry_t *entry = lnk_find_locked(mac);
    if (entry == NULL) {
        entry = lnk_claim_locked(mac);
    }
    if (entry != NULL) {
        entry->rssi[entry->next] = rssi;
        entry->next = (uint8_t)((entry->next + 1) % LNK_MAX_SAMPLES);
        if (entry->count < LNK_MAX_SAMPLES) {
            entry->count++;
        }
        entry->last_ms = now;
    }
    portEXIT_CRITICAL(&s_lnk_lock);
}

static void wifi_event_handler(void *arg, esp_event_base_t base,
                               int32_t id, void *data)
{
    if (base == WIFI_EVENT && id == WIFI_EVENT_STA_START) {
        esp_wifi_connect();
    } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_DISCONNECTED) {
        wifi_event_sta_disconnected_t *ev = (wifi_event_sta_disconnected_t *)data;
        ESP_LOGW(TAG, "Wi-Fi desconectado reason=%u", (unsigned)ev->reason);
        s_retry_num++;
        if (s_retry_num > 1) {
            vTaskDelay(pdMS_TO_TICKS(500));
        }
        esp_wifi_connect();
        ESP_LOGW(TAG, "reconectando Wi-Fi (%d)", s_retry_num);
    } else if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
        ip_event_got_ip_t *event = (ip_event_got_ip_t *)data;
        ESP_LOGI(TAG, "IP: " IPSTR, IP2STR(&event->ip_info.ip));
        s_retry_num = 0;
        bssid_refresh();
        xEventGroupSetBits(s_wifi_event_group, WIFI_CONNECTED_BIT);
    }
}

static void wifi_csi_raw_cb(void *ctx, wifi_csi_info_t *info)
{
    if (info == NULL || info->buf == NULL || info->len <= 0) {
        return;
    }

    frame_source_t source = classify_source(info->mac);
    if (source == SOURCE_OTHER) {
#if LNK_PEER_CAPTURE
        if (memcmp(info->mac, s_self_mac, MAC_LEN) != 0) {
            lnk_record(info->mac, info->rx_ctrl.rssi);
        }
#endif
        return;
    }
    if (source == SOURCE_AP) {
        lnk_record(info->mac, info->rx_ctrl.rssi);
    }

    if ((++s_csi_skip % CSI_DECIMATE) != 0) {
        return;
    }

    csi_frame_t frame = {};
    frame.seq = ++s_csi_seq;
    frame.ts_ms = now_ms();
    frame.rssi = info->rx_ctrl.rssi;
    frame.has_rssi = (source == SOURCE_AP);

    const int8_t *iq = (const int8_t *)info->buf;
    int total_pairs = info->len / 2;
    int step = 1;
    int pairs = total_pairs;
    if (total_pairs > MAX_SUBCARRIERS) {
        step = total_pairs / MAX_SUBCARRIERS;
        if (step < 1) {
            step = 1;
        }
        pairs = MAX_SUBCARRIERS;
    }

    for (int i = 0; i < pairs; i++) {
        int idx = i * step * 2;
        if (idx + 1 >= info->len) {
            break;
        }
        float i_val = (float)iq[idx];
        float q_val = (float)iq[idx + 1];
        float amp = sqrtf(i_val * i_val + q_val * q_val);
        float phase = atan2f(q_val, i_val) * (180.0f / (float)M_PI);

        int a = (int)(amp * 2.0f);
        if (a > 255) {
            a = 255;
        }
        frame.amp[i] = (uint8_t)a;
        frame.phase[i] = (int16_t)(phase * 10.0f);
        frame.count++;
    }

    if (frame.count == 0) {
        return;
    }

    xQueueSend(s_csi_queue, &frame, 0);
}

static int format_csi_line(char *line, size_t cap, const csi_frame_t *frame)
{
    int off = snprintf(line, cap, "CSI,%s,%llu,%lu,%u",
                       CONFIG_CSI_NODE_ID,
                       (unsigned long long)frame->ts_ms,
                       (unsigned long)frame->seq,
                       (unsigned)frame->count);
    for (int i = 0; i < frame->count && off < (int)cap - 16; i++) {
        off += snprintf(line + off, cap - (size_t)off, ",%u,%d",
                        (unsigned)frame->amp[i], (int)frame->phase[i]);
    }
    if (frame->has_rssi && off < (int)cap - 12) {
        off += snprintf(line + off, cap - (size_t)off, ",rssi=%d", (int)frame->rssi);
    }
    if (off < (int)cap - 2) {
        line[off++] = '\n';
        line[off] = '\0';
    }
    return off;
}

static void udp_init(void)
{
    s_udp_sock = socket(AF_INET, SOCK_DGRAM, IPPROTO_IP);
    if (s_udp_sock < 0) {
        ESP_LOGE(TAG, "falha socket UDP");
        return;
    }
    memset(&s_udp_dest, 0, sizeof(s_udp_dest));
    s_udp_dest.sin_family = AF_INET;
    s_udp_dest.sin_port = htons(CONFIG_CSI_UDP_PORT);
    if (inet_pton(AF_INET, CONFIG_CSI_UDP_HOST, &s_udp_dest.sin_addr) != 1) {
        ESP_LOGE(TAG, "CSI_UDP_HOST invalido: %s", CONFIG_CSI_UDP_HOST);
        close(s_udp_sock);
        s_udp_sock = -1;
        return;
    }
    ESP_LOGI(TAG, "UDP -> %s:%d node=%s", CONFIG_CSI_UDP_HOST, CONFIG_CSI_UDP_PORT, CONFIG_CSI_NODE_ID);
}

static void csi_tx_task(void *arg)
{
    csi_frame_t frame;
    char line[768];

    while (1) {
        if (xQueueReceive(s_csi_queue, &frame, portMAX_DELAY) != pdTRUE) {
            continue;
        }

        int off = format_csi_line(line, sizeof(line), &frame);
        if (off <= 0) {
            continue;
        }

        if (s_udp_sock >= 0) {
            sendto(s_udp_sock, line, (size_t)off, 0,
                   (struct sockaddr *)&s_udp_dest, sizeof(s_udp_dest));
        }

#if CONFIG_CSI_ENABLE_UART
        if (s_print_mu) {
            xSemaphoreTake(s_print_mu, portMAX_DELAY);
        }
        fwrite(line, 1, (size_t)off, stdout);
        fflush(stdout);
        if (s_print_mu) {
            xSemaphoreGive(s_print_mu);
        }
#endif
    }
}

static int8_t median_rssi(const int8_t *values, int count)
{
    int8_t sorted[LNK_MAX_SAMPLES];
    memcpy(sorted, values, (size_t)count);
    for (int i = 1; i < count; i++) {
        int8_t key = sorted[i];
        int j = i - 1;
        while (j >= 0 && sorted[j] > key) {
            sorted[j + 1] = sorted[j];
            j--;
        }
        sorted[j + 1] = key;
    }
    if (count % 2 == 1) {
        return sorted[count / 2];
    }
    return (int8_t)(((int)sorted[count / 2 - 1] + (int)sorted[count / 2]) / 2);
}

static int format_lnk_line(char *line, size_t cap, const lnk_entry_t *entry)
{
    return snprintf(line, cap,
                    "LNK,%s,%02x:%02x:%02x:%02x:%02x:%02x,%02x:%02x:%02x:%02x:%02x:%02x,%d\n",
                    CONFIG_CSI_NODE_ID,
                    s_self_mac[0], s_self_mac[1], s_self_mac[2],
                    s_self_mac[3], s_self_mac[4], s_self_mac[5],
                    entry->mac[0], entry->mac[1], entry->mac[2],
                    entry->mac[3], entry->mac[4], entry->mac[5],
                    (int)median_rssi(entry->rssi, entry->count));
}

static void lnk_take_snapshot(lnk_entry_t *snapshot)
{
    portENTER_CRITICAL(&s_lnk_lock);
    memcpy(snapshot, s_lnk, sizeof(s_lnk));
    for (int i = 0; i < LNK_MAX_MACS; i++) {
        s_lnk[i].count = 0;
        s_lnk[i].next = 0;
    }
    portEXIT_CRITICAL(&s_lnk_lock);
}

static void lnk_tx_task(void *arg)
{
    lnk_entry_t snapshot[LNK_MAX_MACS];
    char line[128];

    while (1) {
        vTaskDelay(pdMS_TO_TICKS(LNK_WINDOW_MS));
        lnk_take_snapshot(snapshot);
        for (int i = 0; i < LNK_MAX_MACS; i++) {
            if (!snapshot[i].used || snapshot[i].count == 0 || s_udp_sock < 0) {
                continue;
            }
            int len = format_lnk_line(line, sizeof(line), &snapshot[i]);
            if (len <= 0 || len >= (int)sizeof(line)) {
                continue;
            }
            sendto(s_udp_sock, line, (size_t)len, 0,
                   (struct sockaddr *)&s_udp_dest, sizeof(s_udp_dest));
        }
    }
}

static void wifi_init_sta(void)
{
    s_wifi_event_group = xEventGroupCreate();

    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_sta();

    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));

    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        WIFI_EVENT, ESP_EVENT_ANY_ID, &wifi_event_handler, NULL, NULL));
    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        IP_EVENT, IP_EVENT_STA_GOT_IP, &wifi_event_handler, NULL, NULL));

    wifi_config_t wifi_config = {};
    strncpy((char *)wifi_config.sta.ssid, CONFIG_CSI_WIFI_SSID,
            sizeof(wifi_config.sta.ssid));
    strncpy((char *)wifi_config.sta.password, CONFIG_CSI_WIFI_PASSWORD,
            sizeof(wifi_config.sta.password));
    wifi_config.sta.threshold.authmode = WIFI_AUTH_WPA_WPA2_PSK;
    wifi_config.sta.pmf_cfg.capable = true;
    wifi_config.sta.pmf_cfg.required = false;

    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &wifi_config));
    ESP_ERROR_CHECK(esp_wifi_start());

    ESP_ERROR_CHECK(esp_wifi_set_bandwidth(WIFI_IF_STA, WIFI_BW20));
    ESP_ERROR_CHECK(esp_wifi_set_ps(WIFI_PS_NONE));

    EventBits_t bits = xEventGroupWaitBits(
        s_wifi_event_group, WIFI_CONNECTED_BIT | WIFI_FAIL_BIT,
        pdFALSE, pdFALSE, portMAX_DELAY);

    if (bits & WIFI_CONNECTED_BIT) {
        ESP_LOGI(TAG, "conectado a SSID:%s node=%s", CONFIG_CSI_WIFI_SSID, CONFIG_CSI_NODE_ID);
    } else {
        ESP_LOGE(TAG, "falha ao conectar Wi-Fi");
    }
}

static void csi_init(void)
{
    wifi_csi_config_t csi_config = {
        .lltf_en = true,
        .htltf_en = true,
        .stbc_htltf2_en = true,
        .ltf_merge_en = true,
        .channel_filter_en = false,
        .manu_scale = false,
        .shift = 0,
        .dump_ack_en = false,
    };

    ESP_ERROR_CHECK(esp_wifi_set_csi_config(&csi_config));
    ESP_ERROR_CHECK(esp_wifi_set_csi_rx_cb(&wifi_csi_raw_cb, NULL));
    ESP_ERROR_CHECK(esp_wifi_set_csi(true));
    ESP_LOGI(TAG, "CSI habilitado");
}

static void promiscuous_init(void)
{
#if LNK_PEER_CAPTURE
    wifi_promiscuous_filter_t filter = {
        .filter_mask = WIFI_PROMIS_FILTER_MASK_DATA,
    };
    ESP_ERROR_CHECK(esp_wifi_set_promiscuous_filter(&filter));
    ESP_ERROR_CHECK(esp_wifi_set_promiscuous(true));
    ESP_LOGI(TAG, "promiscuous mode enabled (peer capture)");
#endif
}

static bool gateway_addr(esp_ip4_addr_t *out)
{
    esp_netif_t *netif = esp_netif_get_handle_from_ifkey("WIFI_STA_DEF");
    if (netif == NULL) {
        return false;
    }
    esp_netif_ip_info_t info;
    if (esp_netif_get_ip_info(netif, &info) != ESP_OK) {
        return false;
    }
    if (info.gw.addr == 0) {
        return false;
    }
    *out = info.gw;
    return true;
}

static void csi_stim_start(void)
{
    esp_ip4_addr_t gw;
    if (!gateway_addr(&gw)) {
        ESP_LOGW(TAG, "stim CSI: sem gateway");
        return;
    }

    esp_ping_config_t cfg = ESP_PING_DEFAULT_CONFIG();
    cfg.count = ESP_PING_COUNT_INFINITE;
    cfg.interval_ms = CSI_STIM_MS;
    cfg.timeout_ms = CSI_STIM_MS;
    cfg.target_addr.u_addr.ip4.addr = gw.addr;
    cfg.target_addr.type = ESP_IPADDR_TYPE_V4;
    cfg.task_stack_size = 3072;
    cfg.task_prio = 5;

    esp_ping_callbacks_t cbs = {};
    esp_ping_handle_t ping = NULL;
    if (esp_ping_new_session(&cfg, &cbs, &ping) != ESP_OK || ping == NULL) {
        ESP_LOGE(TAG, "stim CSI: falha ao criar ping");
        return;
    }
    if (esp_ping_start(ping) != ESP_OK) {
        ESP_LOGE(TAG, "stim CSI: falha ao iniciar ping");
        return;
    }
    ESP_LOGI(TAG, "stim CSI: ping gateway " IPSTR " a cada %d ms", IP2STR(&gw), CSI_STIM_MS);
}

extern "C" void app_main(void);

void app_main(void)
{
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    s_print_mu = xSemaphoreCreateMutex();
    s_csi_queue = xQueueCreate(CONFIG_CSI_UART_TX_QUEUE_LEN, sizeof(csi_frame_t));
    if (s_csi_queue == NULL) {
        ESP_LOGE(TAG, "falha ao criar fila CSI");
        return;
    }

    xTaskCreate(csi_tx_task, "csi_tx", 4096, NULL, 8, NULL);

    ESP_ERROR_CHECK(esp_read_mac(s_self_mac, ESP_MAC_WIFI_STA));

    wifi_init_sta();
    udp_init();
    csi_init();
    promiscuous_init();
    xTaskCreate(lnk_tx_task, "lnk_tx", 4096, NULL, 7, NULL);
    csi_stim_start();

    ESP_LOGI(TAG, "firmware CSI pronto — UDP+UART node=%s", CONFIG_CSI_NODE_ID);
    esp_log_level_set("*", ESP_LOG_WARN);
    esp_log_level_set("csi_fw", ESP_LOG_INFO);
}
