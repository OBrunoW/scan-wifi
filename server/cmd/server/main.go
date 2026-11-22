package main

import (
	"encoding/json"
	"flag"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/bruno/scan-wifi/internal/bridge"
	"github.com/bruno/scan-wifi/internal/ping"
	"github.com/bruno/scan-wifi/internal/udpnode"
	"github.com/bruno/scan-wifi/web"
)

func main() {
	port := flag.String("port", envOr("PORT", "8787"), "porta HTTP local")
	routerIP := flag.String("router", "192.168.1.1", "IP do roteador para ping CSI")
	pingEvery := flag.Duration("ping", 30*time.Millisecond, "intervalo do ping daemon")
	noUI := flag.Bool("no-ui", false, "só servidor HTTP (sem janela webview)")
	roomW := flag.Float64("room-w", 0, "largura da sala em metros (ou ROOM_W)")
	roomD := flag.Float64("room-d", 0, "profundidade da sala em metros (ou ROOM_D)")
	inferURL := flag.String("inference", envOr("INFERENCE_SERVER_URL", "http://127.0.0.1:8790"), "URL do microsserviço de pose CSI")
	udpAddr := flag.String("udp", envOr("CSI_UDP_ADDR", envOr("CSI_UDP", ":9001")), "endereço UDP para CSI multi-ESP (vazio = desliga)")
	stimIPs := flag.String("stim", envOr("CSI_STIM", ""), "IPs extras para ping CSI (CSV), ex: 192.168.0.105")
	flag.Parse()
	w := *roomW
	if w <= 0 {
		w = envFloat("ROOM_W", 0)
	}
	d := *roomD
	if d <= 0 {
		d = envFloat("ROOM_D", 0)
	}
	baselineM := envFloat("ESP_BASELINE_M", 0)
	if os.Getenv("ESP_BASELINE_M") != "" && baselineM <= 0 {
		log.Printf("warning: ESP_BASELINE_M=%q is invalid; using estimated scale", os.Getenv("ESP_BASELINE_M"))
		baselineM = 0
	}
	layout := bridge.NewLayout(w, d, baselineM)
	go layout.Run()
	r := bridge.GetAppRoom()
	log.Printf("auto-map: origin=transmitter(0,0) room~%.1fx%.1f (ESPs placed by RSSI, scale %s)", r.Width, r.Depth, expectedScale(baselineM))

	hub := bridge.NewHub()
	go hub.Run()

	ml := bridge.NewInferenceClient(*inferURL)
	if ml.Enabled() {
		go ml.Loop(func() bridge.Room { return bridge.GetAppRoom() }, 120*time.Millisecond)
		log.Printf("inferência CSI: %s", *inferURL)
	}

	processor := bridge.NewProcessor(hub, ml, layout)
	go processor.Run()

	var udpListener *udpnode.Listener
	udpOn := *udpAddr != "" && *udpAddr != "off"
	if udpOn {
		udpListener = udpnode.NewListener(*udpAddr, processor)
		go udpListener.Run()
		log.Printf("UDP CSI multi-nó em %s", *udpAddr)
	}

	if !udpOn {
		log.Println("aviso: -udp off; modo demo ativo")
		go runDemo(processor)
	}

	var peerIPs ping.TargetSource
	if udpListener != nil {
		peerIPs = ping.MemoPeers(udpListener.PeerIPs, 200*time.Millisecond)
	}
	extras := ping.StaticExtras(*stimIPs)
	go ping.RunDaemon(*routerIP, *pingEvery, func() []string {
		var out []string
		if extras != nil {
			out = append(out, extras()...)
		}
		if peerIPs != nil {
			out = append(out, peerIPs()...)
		}
		return out
	})

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(web.FS)))
	mux.HandleFunc("/ws", hub.HandleWS)
	mux.HandleFunc("/api/status", hub.HandleStatus)
	mux.HandleFunc("/api/udp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if udpListener == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"listening": false, "peers": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(udpListener.Snapshot())
	})
	mux.HandleFunc("/api/calibrate", handleCalibrate(ml))
	mux.HandleFunc("/api/calibrate/status", handleCalibrateStatus(ml))
	mux.HandleFunc("/api/collect/start", handleCollectStart(ml))
	mux.HandleFunc("/api/collect/save", handleCollectSave(ml))
	mux.HandleFunc("/api/collect/status", handleCollectStatus(ml))
	registerCalibrationRoutes(mux, bridge.NewCalibrationClient(ml, *inferURL))

	bind := envOr("HOST_BIND", "127.0.0.1")
	addr := bind + ":" + *port
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("HTTP em http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	time.Sleep(200 * time.Millisecond)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if *noUI || !uiEnabled {
		log.Println("modo headless (HTTP); use -tags webview no build para janela nativa")
		<-sigCh
		_ = srv.Close()
		return
	}

	runUI(addr, srv, sigCh)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func expectedScale(baselineM float64) string {
	if baselineM > 0 {
		return "medida"
	}
	return "estimada"
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func handleCalibrate(ml *bridge.InferenceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ml == nil || !ml.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]string{"code": "UNAVAILABLE", "message": "microsserviço de inferência desligado"},
			})
			return
		}
		var body struct {
			Posture string `json:"posture"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Posture == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"code": "BAD_REQUEST", "message": "informe posture"},
			})
			return
		}
		out, err := ml.Calibrate(body.Posture)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"code": "BAD_REQUEST", "message": err.Error()},
			})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleCalibrateStatus(ml *bridge.InferenceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ml == nil || !ml.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]string{"code": "UNAVAILABLE", "message": "microsserviço de inferência desligado"},
			})
			return
		}
		out, err := ml.Status()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"code": "BAD_GATEWAY", "message": err.Error()},
			})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleCollectStart(ml *bridge.InferenceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ml == nil || !ml.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "UNAVAILABLE", "message": "inference offline"}})
			return
		}
		var body struct {
			Posture string `json:"posture"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Posture == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "BAD_REQUEST", "message": "informe posture"}})
			return
		}
		out, err := ml.CollectStart(body.Posture)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "BAD_REQUEST", "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleCollectSave(ml *bridge.InferenceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ml == nil || !ml.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "UNAVAILABLE", "message": "inference offline"}})
			return
		}
		var body struct {
			Posture string `json:"posture"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		out, err := ml.CollectSave(body.Posture)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "BAD_REQUEST", "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleCollectStatus(ml *bridge.InferenceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ml == nil || !ml.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "UNAVAILABLE", "message": "inference offline"}})
			return
		}
		out, err := ml.CollectStatus()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "BAD_GATEWAY", "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func runDemo(p *bridge.Processor) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	var i float64
	for range t.C {
		i += 0.05
		amp := make([]float64, 32)
		phase := make([]float64, 32)
		for k := range amp {
			amp[k] = 70
			phase[k] = 10
		}
		p1 := 8 + int(3*math.Sin(i*0.4))
		p2 := 22 + int(3*math.Cos(i*0.35))
		for k := range amp {
			d1 := float64(k - p1)
			d2 := float64(k - p2)
			amp[k] += 90*math.Exp(-d1*d1/8) + 85*math.Exp(-d2*d2/8)
			phase[k] += 40 * math.Sin(i+float64(k)*0.15)
		}
		p.Ingest(bridge.CSIPacket{Seq: uint32(i * 20), Amp: amp, Phase: phase})
	}
}

func registerCalibrationRoutes(mux *http.ServeMux, cal *bridge.CalibrationClient) {
	mux.HandleFunc("GET /api/calibration/state", handleCalibrationState(cal))
	mux.HandleFunc("POST /api/calibration/session", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.CreateSession(bridge.GetAppRoom())
	}))
	mux.HandleFunc("POST /api/calibration/marks/{label}/start", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		var body struct {
			Posture string `json:"posture"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Posture == "" {
			return calibrationBadRequest("informe posture"), nil
		}
		return cal.StartMark(r.PathValue("label"), body.Posture)
	}))
	mux.HandleFunc("POST /api/calibration/marks/{label}/skip", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.SkipMark(r.PathValue("label"))
	}))
	mux.HandleFunc("DELETE /api/calibration/marks/{label}/samples", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.ClearMark(r.PathValue("label"))
	}))
	mux.HandleFunc("POST /api/calibration/compute", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.Compute()
	}))
	mux.HandleFunc("GET /api/calibration/list", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.Do(http.MethodGet, "/calibration/list", nil)
	}))
	mux.HandleFunc("GET /api/calibration/active", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.Do(http.MethodGet, "/calibration/active", nil)
	}))
	mux.HandleFunc("POST /api/calibration/{id}/activate", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.Do(http.MethodPost, "/calibration/"+url.PathEscape(r.PathValue("id"))+"/activate", nil)
	}))
	mux.HandleFunc("POST /api/calibration/{id}/discard", relayCalibration(cal, func(r *http.Request) (bridge.CalibrationResponse, error) {
		return cal.Do(http.MethodPost, "/calibration/"+url.PathEscape(r.PathValue("id"))+"/discard", nil)
	}))
}

func calibrationBadRequest(message string) bridge.CalibrationResponse {
	return bridge.CalibrationResponse{
		Status: http.StatusBadRequest,
		Body:   map[string]any{"error": map[string]string{"code": "BAD_REQUEST", "message": message}},
	}
}

func relayCalibration(cal *bridge.CalibrationClient, call func(*http.Request) (bridge.CalibrationResponse, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cal.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "UNAVAILABLE", "message": "inference offline"}})
			return
		}
		resp, err := call(r)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "BAD_GATEWAY", "message": err.Error()}})
			return
		}
		writeJSON(w, resp.Status, resp.Body)
	}
}

func handleCalibrationState(cal *bridge.CalibrationClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cal.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "UNAVAILABLE", "message": "inference offline"}})
			return
		}
		room := bridge.GetAppRoom()
		marks, err := bridge.CalibrationMarks(room.Width, room.Depth)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "CALIBRATION_ROOM_UNKNOWN", "message": "sala sem dimensoes; defina ROOM_W e ROOM_D"}})
			return
		}
		session, err := cal.Do(http.MethodGet, "/calibration/session", nil)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "BAD_GATEWAY", "message": err.Error()}})
			return
		}
		active, err := cal.Do(http.MethodGet, "/calibration/active", nil)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "BAD_GATEWAY", "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"marks":   marks,
			"room":    map[string]float64{"width": room.Width, "depth": room.Depth},
			"session": session.Body,
			"active":  active.Body["active"],
		})
	}
}
