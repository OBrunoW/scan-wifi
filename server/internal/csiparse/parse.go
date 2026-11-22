package csiparse

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bruno/scan-wifi/internal/bridge"
)

var (
	ErrBadLine = errors.New("linha CSI invalida")
	ErrBadLink = errors.New("invalid LNK line")
	ErrBadRSSI = errors.New("invalid rssi")
)

const rssiKey = "rssi="

func ParseLine(line string) (bridge.CSIPacket, error) {
	parts := strings.Split(strings.TrimSpace(line), ",")
	var zero bridge.CSIPacket
	if len(parts) < 3 || parts[0] != "CSI" {
		return zero, ErrBadLine
	}
	if isV2(parts) {
		return parseV2(parts)
	}
	return parseLegacy(parts)
}

func isV2(parts []string) bool {
	if len(parts) < 5 {
		return false
	}
	if looksLikeNodeID(parts[1]) {
		return true
	}
	if _, err := strconv.ParseUint(parts[1], 10, 32); err == nil {
		return false
	}
	if _, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
		return true
	}
	return false
}

func looksLikeNodeID(s string) bool {
	u := strings.ToUpper(s)
	if strings.HasPrefix(u, "ESP") || strings.Contains(u, "NODE") {
		return true
	}
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func parseV2(parts []string) (bridge.CSIPacket, error) {
	var zero bridge.CSIPacket
	if len(parts) < 5 {
		return zero, ErrBadLine
	}
	node := parts[1]
	ts, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return zero, err
	}
	seq, err := strconv.ParseUint(parts[3], 10, 32)
	if err != nil {
		return zero, err
	}
	n, err := strconv.Atoi(parts[4])
	if err != nil || n < 1 {
		return zero, ErrBadLine
	}
	need := 5 + n*2
	if len(parts) < need {
		return zero, ErrBadLine
	}
	amp := make([]float64, n)
	phase := make([]float64, n)
	for i := 0; i < n; i++ {
		a, err1 := strconv.ParseFloat(parts[5+i*2], 64)
		ph, err2 := strconv.ParseFloat(parts[6+i*2], 64)
		if err1 != nil || err2 != nil {
			return zero, ErrBadLine
		}
		amp[i] = a
		phase[i] = ph / 10.0
	}
	rssi, hasRSSI, err := parseRSSITail(parts[need:])
	if err != nil {
		return zero, err
	}
	return bridge.CSIPacket{
		Seq:     uint32(seq),
		Amp:     amp,
		Phase:   phase,
		NodeID:  node,
		TsMs:    ts,
		RSSI:    rssi,
		HasRSSI: hasRSSI,
	}, nil
}

func parseRSSITail(tail []string) (float64, bool, error) {
	for _, token := range tail {
		value, found := strings.CutPrefix(strings.TrimSpace(token), rssiKey)
		if !found {
			continue
		}
		rssi, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false, ErrBadRSSI
		}
		return rssi, true, nil
	}
	return 0, false, nil
}

func ParseLinkLine(line string) (bridge.LinkSample, error) {
	var zero bridge.LinkSample
	parts := strings.Split(strings.TrimSpace(line), ",")
	if len(parts) != 5 || parts[0] != "LNK" {
		return zero, ErrBadLink
	}
	selfMAC, okSelf := normalizeMAC(parts[2])
	srcMAC, okSrc := normalizeMAC(parts[3])
	if parts[1] == "" || !okSelf || !okSrc {
		return zero, ErrBadLink
	}
	rssi, err := strconv.ParseFloat(parts[4], 64)
	if err != nil {
		return zero, ErrBadRSSI
	}
	return bridge.LinkSample{NodeID: parts[1], SelfMAC: selfMAC, SrcMAC: srcMAC, RSSI: rssi}, nil
}

func normalizeMAC(raw string) (string, bool) {
	compact := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), ":", ""))
	if len(compact) != 12 {
		return "", false
	}
	for _, r := range compact {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", false
		}
	}
	return compact, true
}

func parseLegacy(parts []string) (bridge.CSIPacket, error) {
	var zero bridge.CSIPacket
	seq, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return zero, err
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 1 {
		return zero, ErrBadLine
	}
	need := 3 + n*2
	if len(parts) < need {
		return zero, ErrBadLine
	}
	amp := make([]float64, n)
	phase := make([]float64, n)
	for i := 0; i < n; i++ {
		a, err1 := strconv.ParseFloat(parts[3+i*2], 64)
		ph, err2 := strconv.ParseFloat(parts[4+i*2], 64)
		if err1 != nil || err2 != nil {
			return zero, ErrBadLine
		}
		amp[i] = a
		phase[i] = ph / 10.0
	}
	return bridge.CSIPacket{
		Seq:    uint32(seq),
		Amp:    amp,
		Phase:  phase,
		NodeID: "ESP_NODE_01",
		TsMs:   time.Now().UnixMilli(),
	}, nil
}
