package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	calibrationGridCols    = 3
	calibrationGridRows    = 2
	calibrationRoomFill    = 0.8
	calibrationFlushEvery  = 250 * time.Millisecond
	calibrationMaxFlushErr = 5
	calibrationTimeout     = 15 * time.Second
)

var errCalibrationRoomUnknown = errors.New("room dimensions unknown")

type CalibrationMark struct {
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Z     float64 `json:"z"`
}

func CalibrationMarks(width, depth float64) ([]CalibrationMark, error) {
	if width <= 0 || depth <= 0 {
		return nil, errCalibrationRoomUnknown
	}
	cellW := width * calibrationRoomFill / calibrationGridCols
	cellD := depth * calibrationRoomFill / calibrationGridRows
	originX := -width * calibrationRoomFill / 2
	originZ := -depth * calibrationRoomFill / 2
	marks := make([]CalibrationMark, 0, calibrationGridCols*calibrationGridRows)
	for row := 0; row < calibrationGridRows; row++ {
		for col := 0; col < calibrationGridCols; col++ {
			marks = append(marks, CalibrationMark{
				Label: fmt.Sprintf("M%d", len(marks)+1),
				X:     originX + cellW*(float64(col)+0.5),
				Z:     originZ + cellD*(float64(row)+0.5),
			})
		}
	}
	return marks, nil
}

type CalibrationResponse struct {
	Status int
	Body   map[string]any
}

type CalibrationClient struct {
	base   string
	http   *http.Client
	source *InferenceClient

	mu      sync.Mutex
	buffer  []CSIPacket
	stop    chan struct{}
	flushed chan struct{}
	active  bool
}

func NewCalibrationClient(source *InferenceClient, base string) *CalibrationClient {
	return &CalibrationClient{
		base:   base,
		http:   &http.Client{Timeout: calibrationTimeout},
		source: source,
	}
}

func (c *CalibrationClient) Enabled() bool { return c != nil && c.source.Enabled() }

func (c *CalibrationClient) Do(method, path string, payload any) (CalibrationResponse, error) {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return CalibrationResponse{}, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return CalibrationResponse{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return CalibrationResponse{}, err
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return CalibrationResponse{}, fmt.Errorf("inference returned HTTP %d with an unreadable body: %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if detail, ok := body["detail"].(map[string]any); ok {
			body = map[string]any{"error": detail}
		}
	}
	return CalibrationResponse{Status: resp.StatusCode, Body: body}, nil
}

func markPath(label, action string) string {
	return "/calibration/marks/" + url.PathEscape(label) + "/" + action
}

func (c *CalibrationClient) CreateSession(room Room) (CalibrationResponse, error) {
	marks, err := CalibrationMarks(room.Width, room.Depth)
	if err != nil {
		return CalibrationResponse{}, err
	}
	c.StopCapture()
	return c.Do(http.MethodPost, "/calibration/session", map[string]any{
		"marks": marks,
		"room":  map[string]float64{"width": room.Width, "depth": room.Depth},
	})
}

func (c *CalibrationClient) StartMark(label, posture string) (CalibrationResponse, error) {
	c.StopCapture()
	resp, err := c.Do(http.MethodPost, markPath(label, "start"), map[string]string{"posture": posture})
	if err != nil || resp.Status != http.StatusOK {
		return resp, err
	}
	c.startCapture(label)
	return resp, nil
}

func (c *CalibrationClient) SkipMark(label string) (CalibrationResponse, error) {
	c.StopCapture()
	return c.Do(http.MethodPost, markPath(label, "skip"), nil)
}

func (c *CalibrationClient) ClearMark(label string) (CalibrationResponse, error) {
	c.StopCapture()
	return c.Do(http.MethodDelete, markPath(label, "samples"), nil)
}

func (c *CalibrationClient) Compute() (CalibrationResponse, error) {
	c.StopCapture()
	return c.Do(http.MethodPost, "/calibration/compute", nil)
}

func (c *CalibrationClient) startCapture(label string) {
	c.mu.Lock()
	c.buffer = nil
	stop := make(chan struct{})
	flushed := make(chan struct{})
	c.stop = stop
	c.flushed = flushed
	c.active = true
	c.mu.Unlock()
	c.source.SetTap(c.onPacket)
	go c.flushLoop(label, stop, flushed)
}

func (c *CalibrationClient) StopCapture() {
	c.mu.Lock()
	stop, flushed := c.stop, c.flushed
	c.stop, c.flushed = nil, nil
	c.buffer = nil
	c.active = false
	c.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-flushed
	c.source.SetTap(nil)
}

func (c *CalibrationClient) onPacket(pkt CSIPacket) {
	c.mu.Lock()
	if c.active {
		c.buffer = append(c.buffer, pkt)
	}
	c.mu.Unlock()
}

func (c *CalibrationClient) drain() []CSIPacket {
	c.mu.Lock()
	defer c.mu.Unlock()
	batch := c.buffer
	c.buffer = nil
	return batch
}

func (c *CalibrationClient) finishCapture(flushed chan struct{}) {
	c.mu.Lock()
	if c.flushed == flushed {
		c.active = false
		c.buffer = nil
	}
	c.mu.Unlock()
}

func (c *CalibrationClient) flushLoop(label string, stop <-chan struct{}, flushed chan struct{}) {
	defer close(flushed)
	defer c.finishCapture(flushed)
	ticker := time.NewTicker(calibrationFlushEvery)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		batch := c.drain()
		if len(batch) == 0 {
			continue
		}
		done, err := c.flush(label, batch)
		if err != nil {
			log.Printf("calibration: flush for %s failed: %v", label, err)
		}
		if done {
			return
		}
		if err == nil {
			failures = 0
			continue
		}
		failures++
		if failures >= calibrationMaxFlushErr {
			log.Printf("calibration: capture for %s stopped after %d failures", label, failures)
			return
		}
	}
}

func (c *CalibrationClient) flush(label string, batch []CSIPacket) (bool, error) {
	resp, err := c.Do(http.MethodPost, markPath(label, "packets"), map[string]any{"packets": packetsToInfer(batch)})
	if err != nil {
		return false, err
	}
	if resp.Status != http.StatusOK {
		return true, fmt.Errorf("inference rejected packets with HTTP %d: %v", resp.Status, resp.Body["error"])
	}
	active, _ := resp.Body["active"].(map[string]any)
	activeLabel, _ := active["label"].(string)
	return activeLabel != label, nil
}
