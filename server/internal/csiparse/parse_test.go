package csiparse

import (
	"errors"
	"testing"
)

func TestParseLineWithoutRSSIKeepsLegacyContract(t *testing.T) {
	pkt, err := ParseLine("CSI,ESP_NODE_01,1000,7,2,10,100,20,-50")
	if err != nil {
		t.Fatal(err)
	}
	if pkt.HasRSSI || len(pkt.Amp) != 2 || pkt.NodeID != "ESP_NODE_01" {
		t.Fatalf("unexpected packet %+v", pkt)
	}
}

func TestParseLineReadsRSSITail(t *testing.T) {
	pkt, err := ParseLine("CSI,ESP_NODE_02,1000,7,2,10,100,20,-50,rssi=-57\n")
	if err != nil {
		t.Fatal(err)
	}
	if !pkt.HasRSSI || pkt.RSSI != -57 || len(pkt.Phase) != 2 {
		t.Fatalf("unexpected packet %+v", pkt)
	}
}

func TestParseLineRejectsMalformedRSSI(t *testing.T) {
	_, err := ParseLine("CSI,ESP_NODE_02,1000,7,1,10,100,rssi=abc")
	if !errors.Is(err, ErrBadRSSI) {
		t.Fatalf("expected ErrBadRSSI, got %v", err)
	}
}

func TestParseLinkLine(t *testing.T) {
	sample, err := ParseLinkLine("LNK,ESP_NODE_01,24:0A:C4:00:11:22,240ac4003344,-63\n")
	if err != nil {
		t.Fatal(err)
	}
	if sample.NodeID != "ESP_NODE_01" || sample.SelfMAC != "240ac4001122" || sample.SrcMAC != "240ac4003344" || sample.RSSI != -63 {
		t.Fatalf("unexpected sample %+v", sample)
	}
}

func TestParseLinkLineRejectsBadInput(t *testing.T) {
	cases := []string{
		"LNK,ESP_NODE_01,240ac4001122,-63",
		"LNK,ESP_NODE_01,zz0ac4001122,240ac4003344,-63",
		"CSI,ESP_NODE_01,240ac4001122,240ac4003344,-63",
		"LNK,,240ac4001122,240ac4003344,-63",
	}
	for _, line := range cases {
		if _, err := ParseLinkLine(line); !errors.Is(err, ErrBadLink) {
			t.Fatalf("%q: expected ErrBadLink, got %v", line, err)
		}
	}
	if _, err := ParseLinkLine("LNK,ESP_NODE_01,240ac4001122,240ac4003344,x"); !errors.Is(err, ErrBadRSSI) {
		t.Fatalf("expected ErrBadRSSI, got %v", err)
	}
}
