package bridge

import (
	"sync"
	"time"
)

const (
	syncWindowMs = 250
	syncBufMax   = 128
)

type StereoPair struct {
	Node1 CSIPacket
	Node2 CSIPacket
	TsMs  int64
}

type NodeSyncer struct {
	mu   sync.Mutex
	bufs map[string][]CSIPacket
	out  chan StereoPair
}

func NewNodeSyncer() *NodeSyncer {
	return &NodeSyncer{
		bufs: make(map[string][]CSIPacket),
		out:  make(chan StereoPair, 64),
	}
}

func (s *NodeSyncer) Out() <-chan StereoPair { return s.out }

func normalizeNodeID(id string) string {
	switch id {
	case "", "1", "node1", "ESP1":
		return "ESP_NODE_01"
	case "2", "node2", "ESP2":
		return "ESP_NODE_02"
	default:
		return id
	}
}

func (s *NodeSyncer) Push(pkt CSIPacket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pkt.NodeID = normalizeNodeID(pkt.NodeID)
	if pkt.TsMs == 0 {
		pkt.TsMs = time.Now().UnixMilli()
	}
	s.bufs[pkt.NodeID] = append(s.bufs[pkt.NodeID], pkt)
	if len(s.bufs[pkt.NodeID]) > syncBufMax {
		s.bufs[pkt.NodeID] = s.bufs[pkt.NodeID][len(s.bufs[pkt.NodeID])-syncBufMax:]
	}
	s.tryEmitLocked()
}

func (s *NodeSyncer) tryEmitLocked() {
	b1 := s.bufs["ESP_NODE_01"]
	b2 := s.bufs["ESP_NODE_02"]
	if len(b1) == 0 || len(b2) == 0 {
		return
	}

	p1 := b1[len(b1)-1]
	bestI := -1
	bestD := int64(1 << 62)
	for i, p := range b2 {
		d := p1.TsMs - p.TsMs
		if d < 0 {
			d = -d
		}
		if d < bestD {
			bestD = d
			bestI = i
		}
	}
	if bestI < 0 || bestD > syncWindowMs {
		return
	}
	p2 := b2[bestI]
	s.bufs["ESP_NODE_01"] = b1[:len(b1)-1]
	s.bufs["ESP_NODE_02"] = append(append([]CSIPacket{}, b2[:bestI]...), b2[bestI+1:]...)

	pair := StereoPair{Node1: p1, Node2: p2, TsMs: (p1.TsMs + p2.TsMs) / 2}
	select {
	case s.out <- pair:
	default:
	}
}

func (s *NodeSyncer) SnapshotNode(node string, n int) []CSIPacket {
	s.mu.Lock()
	defer s.mu.Unlock()
	node = normalizeNodeID(node)
	buf := s.bufs[node]
	if n <= 0 || n > len(buf) {
		n = len(buf)
	}
	if n == 0 {
		return nil
	}
	out := make([]CSIPacket, n)
	copy(out, buf[len(buf)-n:])
	return out
}

func (s *NodeSyncer) ClearNode(node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node = normalizeNodeID(node)
	delete(s.bufs, node)
}

func (s *NodeSyncer) ClearAbsent(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.bufs {
		if !live[k] {
			delete(s.bufs, k)
		}
	}
}
