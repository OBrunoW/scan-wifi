package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const predictErrLogEvery = 5 * time.Second

func (c *InferenceClient) logPredictErr(stage string, err error) {
	c.mu.Lock()
	due := time.Since(c.lastErrLog) >= predictErrLogEvery
	if due {
		c.lastErrLog = time.Now()
	}
	c.mu.Unlock()
	if due {
		log.Printf("inferencia: falha em %s: %v", stage, err)
	}
}

type InferPacket struct {
	Seq    uint32    `json:"seq"`
	Amp    []float64 `json:"amp"`
	Phase  []float64 `json:"phase"`
	NodeID string    `json:"node_id,omitempty"`
	TsMs   int64     `json:"ts_ms,omitempty"`
}

type InferRoom struct {
	Width float64 `json:"width"`
	Depth float64 `json:"depth"`
	ESPX  float64 `json:"esp_x"`
	ESPZ  float64 `json:"esp_z"`
	ESP2X float64 `json:"esp2_x"`
	ESP2Z float64 `json:"esp2_z"`
	TxX   float64 `json:"tx_x"`
	TxZ   float64 `json:"tx_z"`
	LinkM float64 `json:"link_m"`
}

type InferPredictReq struct {
	Packets  []InferPacket `json:"packets"`
	Packets2 []InferPacket `json:"packets2,omitempty"`
	Room     InferRoom     `json:"room"`
	Stereo   bool          `json:"stereo,omitempty"`
}

type InferPerson struct {
	ID         int     `json:"id"`
	Posture    string  `json:"posture"`
	Profile    string  `json:"profile"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	Confidence float64 `json:"confidence"`
}

type InferPredictResp struct {
	Persons    []InferPerson `json:"persons"`
	Calibrated bool          `json:"calibrated"`
	Source     string        `json:"source"`
	Device     string        `json:"device"`
}

type InferCalibrateReq struct {
	Posture string        `json:"posture"`
	Packets []InferPacket `json:"packets"`
}

type InferCalibrateResp struct {
	Posture  string         `json:"posture"`
	Samples  int            `json:"samples"`
	Ready    bool           `json:"ready"`
	Profiles map[string]int `json:"profiles"`
}

type InferenceClient struct {
	base   string
	client *http.Client

	mu         sync.Mutex
	buf        []CSIPacket
	buf2       []CSIPacket
	maxBuf     int
	last       InferPredictResp
	ok         bool
	lastOKAt   time.Time
	lastErrLog time.Time
	enabled    bool
	stereo     bool
	tap        func(CSIPacket)
}

func (c *InferenceClient) SetTap(tap func(CSIPacket)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.tap = tap
	c.mu.Unlock()
}

func NewInferenceClient(base string) *InferenceClient {
	c := &InferenceClient{
		base:    base,
		client:  &http.Client{Timeout: 1500 * time.Millisecond},
		maxBuf:  64,
		enabled: base != "",
	}
	return c
}

func (c *InferenceClient) Enabled() bool { return c != nil && c.enabled }

func (c *InferenceClient) Push(pkt CSIPacket) {
	if c == nil || !c.enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	amp := append([]float64(nil), pkt.Amp...)
	phase := append([]float64(nil), pkt.Phase...)
	cp := CSIPacket{Seq: pkt.Seq, Amp: amp, Phase: phase, NodeID: pkt.NodeID, TsMs: pkt.TsMs}
	node := normalizeNodeID(pkt.NodeID)
	if node == "ESP_NODE_02" {
		c.buf2 = append(c.buf2, cp)
		if len(c.buf2) > c.maxBuf {
			c.buf2 = c.buf2[len(c.buf2)-c.maxBuf:]
		}
		return
	}
	c.buf = append(c.buf, cp)
	if len(c.buf) > c.maxBuf {
		c.buf = c.buf[len(c.buf)-c.maxBuf:]
	}
	if c.tap != nil {
		c.tap(cp)
	}
}

func (c *InferenceClient) ClearAbsent(live map[string]bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !live["ESP_NODE_01"] {
		c.buf = nil
	}
	if !live["ESP_NODE_02"] {
		c.buf2 = nil
	}
	c.stereo = live["ESP_NODE_01"] && live["ESP_NODE_02"]
}

func (c *InferenceClient) Snapshot(n int) []CSIPacket {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 || n > len(c.buf) {
		n = len(c.buf)
	}
	out := make([]CSIPacket, n)
	copy(out, c.buf[len(c.buf)-n:])
	return out
}

func (c *InferenceClient) Last() (InferPredictResp, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok || c.lastOKAt.IsZero() {
		return c.last, false
	}
	if time.Since(c.lastOKAt) > 3*time.Second {
		return c.last, false
	}
	return c.last, true
}

func packetsToInfer(pkts []CSIPacket) []InferPacket {
	out := make([]InferPacket, len(pkts))
	for i, p := range pkts {
		out[i] = InferPacket{Seq: p.Seq, Amp: p.Amp, Phase: p.Phase, NodeID: p.NodeID, TsMs: p.TsMs}
	}
	return out
}

func (c *InferenceClient) snapshotLocked(buf []CSIPacket, n int) []CSIPacket {
	if n <= 0 || n > len(buf) {
		n = len(buf)
	}
	out := make([]CSIPacket, n)
	copy(out, buf[len(buf)-n:])
	return out
}

func (c *InferenceClient) Predict(room Room) {
	if c == nil || !c.enabled {
		return
	}
	c.mu.Lock()
	pkts := c.snapshotLocked(c.buf, 32)
	pkts2 := c.snapshotLocked(c.buf2, 32)
	stereo := c.stereo && len(pkts2) >= 4
	c.mu.Unlock()
	if len(pkts) < 4 {
		return
	}
	body := InferPredictReq{
		Packets:  packetsToInfer(pkts),
		Packets2: packetsToInfer(pkts2),
		Stereo:   stereo,
		Room: InferRoom{
			Width: room.Width,
			Depth: room.Depth,
			ESPX:  room.ESPX,
			ESPZ:  room.ESPZ,
			ESP2X: room.ESP2X,
			ESP2Z: room.ESP2Z,
			TxX:   room.TxX,
			TxZ:   room.TxZ,
			LinkM: room.LinkM,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		c.logPredictErr("serializar /predict", err)
		return
	}
	resp, err := c.client.Post(c.base+"/predict", "application/json", bytes.NewReader(raw))
	if err != nil {
		c.logPredictErr("chamar /predict", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.logPredictErr("/predict status", fmt.Errorf("HTTP %d", resp.StatusCode))
		return
	}
	var out InferPredictResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.logPredictErr("decodificar /predict", err)
		return
	}
	c.mu.Lock()
	c.last = out
	c.ok = true
	c.lastOKAt = time.Now()
	c.mu.Unlock()
}

func (c *InferenceClient) Calibrate(posture string) (InferCalibrateResp, error) {
	pkts := c.Snapshot(50)
	if len(pkts) < 10 {
		return InferCalibrateResp{}, errInsufficientCSI
	}
	body := InferCalibrateReq{Posture: posture, Packets: packetsToInfer(pkts)}
	raw, _ := json.Marshal(body)
	resp, err := c.client.Post(c.base+"/calibrate", "application/json", bytes.NewReader(raw))
	if err != nil {
		return InferCalibrateResp{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		log.Printf("calibrate status %d: %v", resp.StatusCode, detail)
		return InferCalibrateResp{}, errCalibrateFailed
	}
	var out InferCalibrateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return InferCalibrateResp{}, err
	}
	return out, nil
}

func (c *InferenceClient) Status() (map[string]any, error) {
	resp, err := c.client.Get(c.base + "/calibrate/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *InferenceClient) CollectStart(posture string) (map[string]any, error) {
	body, _ := json.Marshal(map[string]string{"posture": posture})
	resp, err := c.client.Post(c.base+"/collect/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return out, errCalibrateFailed
	}
	return out, nil
}

func (c *InferenceClient) CollectSave(posture string) (map[string]any, error) {
	pkts := c.Snapshot(64)
	if len(pkts) < 8 {
		return nil, errInsufficientCSI
	}
	payload := map[string]any{
		"posture": posture,
		"packets": packetsToInfer(pkts),
		"room": func() InferRoom {
			ar := GetAppRoom()
			return InferRoom{
				Width: ar.Width, Depth: ar.Depth,
				ESPX: ar.ESPX, ESPZ: ar.ESPZ, ESP2X: ar.ESP2X, ESP2Z: ar.ESP2Z,
				TxX: ar.TxX, TxZ: ar.TxZ, LinkM: ar.LinkM,
			}
		}(),
	}
	raw, _ := json.Marshal(payload)
	resp, err := c.client.Post(c.base+"/collect/save", "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return out, errCalibrateFailed
	}
	return out, nil
}

func (c *InferenceClient) CollectStatus() (map[string]any, error) {
	resp, err := c.client.Get(c.base + "/collect/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *InferenceClient) Loop(roomFn func() Room, every time.Duration) {
	if c == nil || !c.enabled {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		c.Predict(roomFn())
	}
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const (
	errInsufficientCSI = simpleError("CSI insuficiente para calibrar (aguarde ~2s com CSI UDP ativo)")
	errCalibrateFailed = simpleError("falha na calibração no microsserviço")
)
