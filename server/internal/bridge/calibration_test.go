package bridge

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCalibrationMarksGrid(t *testing.T) {
	marks, err := CalibrationMarks(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 6 {
		t.Fatalf("marks = %d, want 6", len(marks))
	}
	for i, mark := range marks {
		wantLabel := "M" + string(rune('1'+i))
		if mark.Label != wantLabel {
			t.Fatalf("label %d = %s, want %s", i, mark.Label, wantLabel)
		}
		if math.Abs(mark.X) > 4 || math.Abs(mark.Z) > 4 {
			t.Fatalf("%s outside 80%% of the room: %+v", mark.Label, mark)
		}
	}
	if marks[1].X != 0 {
		t.Fatalf("middle column must be centered, got x=%v", marks[1].X)
	}
	if marks[0].Z >= marks[3].Z {
		t.Fatalf("second row must have larger z: %v vs %v", marks[0].Z, marks[3].Z)
	}
}

func TestCalibrationMarksFollowRoom(t *testing.T) {
	wide, _ := CalibrationMarks(12, 6)
	narrow, _ := CalibrationMarks(6, 6)
	if wide[2].X <= narrow[2].X {
		t.Fatalf("wider room must spread marks: %v vs %v", wide[2].X, narrow[2].X)
	}
}

func TestCalibrationMarksRejectsUnknownRoom(t *testing.T) {
	if _, err := CalibrationMarks(0, 10); err == nil {
		t.Fatal("expected error for zero width")
	}
}

func TestCalibrationCaptureStopsWhenMarkIsComplete(t *testing.T) {
	var mu sync.Mutex
	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Packets []InferPacket `json:"packets"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received += len(body.Packets)
		total := received
		mu.Unlock()
		active := map[string]any{"label": "M1"}
		if total >= 6 {
			active = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": active})
	}))
	defer server.Close()

	source := NewInferenceClient(server.URL)
	client := NewCalibrationClient(source, server.URL)
	client.startCapture("M1")
	for i := 0; i < 6; i++ {
		source.Push(CSIPacket{Seq: uint32(i), Amp: []float64{1, 2}, Phase: []float64{0, 0}})
	}

	deadline := time.After(3 * time.Second)
	for {
		client.mu.Lock()
		active := client.active
		client.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("capture did not stop after the mark was completed")
		case <-time.After(20 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if received != 6 {
		t.Fatalf("received %d packets, want 6", received)
	}
	client.StopCapture()
}

func TestCalibrationRelayConvertsErrorDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]string{"code": "CALIBRATION_NO_SESSION", "message": "x"}})
	}))
	defer server.Close()

	client := NewCalibrationClient(NewInferenceClient(server.URL), server.URL)
	resp, err := client.Do(http.MethodPost, "/calibration/compute", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusConflict {
		t.Fatalf("status = %d", resp.Status)
	}
	errBody, ok := resp.Body["error"].(map[string]any)
	if !ok || errBody["code"] != "CALIBRATION_NO_SESSION" {
		t.Fatalf("body = %v", resp.Body)
	}
}

func TestCalibrationTapIgnoresSecondNode(t *testing.T) {
	source := NewInferenceClient("http://unused")
	client := NewCalibrationClient(source, "http://unused")
	client.mu.Lock()
	client.active = true
	client.mu.Unlock()
	source.SetTap(client.onPacket)
	source.Push(CSIPacket{NodeID: "ESP_NODE_02", Amp: []float64{1}, Phase: []float64{0}})
	source.Push(CSIPacket{NodeID: "ESP_NODE_01", Amp: []float64{1}, Phase: []float64{0}})
	if got := len(client.drain()); got != 1 {
		t.Fatalf("buffered %d packets, want 1", got)
	}
}
