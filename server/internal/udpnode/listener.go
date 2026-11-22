package udpnode

import (
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bruno/scan-wifi/internal/bridge"
	"github.com/bruno/scan-wifi/internal/csiparse"
)

const peerTTL = 12 * time.Second
const stimMemory = 15 * time.Minute

type Sink interface {
	IngestNode(pkt bridge.CSIPacket)
	IngestLink(sample bridge.LinkSample)
	SetUDPLive(nodes []string)
}

type peer struct {
	ip       string
	nodeHint string
	slot     string
	last     time.Time
	pkts     uint64
}

type Listener struct {
	addr string
	sink Sink

	mu      sync.Mutex
	peers   map[string]*peer
	slots   map[string]string
	recent  map[string]time.Time
	ok      atomic.Bool
	total   atomic.Uint64
	lastLog time.Time
}

func NewListener(addr string, sink Sink) *Listener {
	return &Listener{
		addr:   addr,
		sink:   sink,
		peers:  make(map[string]*peer),
		slots:  make(map[string]string),
		recent: make(map[string]time.Time),
	}
}

func (l *Listener) Run() {
	pc, err := net.ListenPacket("udp", l.addr)
	if err != nil {
		log.Printf("udp: nao abriu %s: %v", l.addr, err)
		return
	}
	defer pc.Close()
	log.Printf("udp: escutando CSI em %s (deteccao por IP de origem)", l.addr)
	l.ok.Store(true)

	go l.heartbeat()

	buf := make([]byte, 4096)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				l.publishLive()
				continue
			}
			log.Printf("udp read: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		ip := addrIP(addr)
		line := strings.TrimSpace(string(buf[:n]))
		if strings.HasPrefix(line, "LNK,") {
			l.handleLink(ip, line)
			continue
		}
		if !strings.HasPrefix(line, "CSI,") {
			continue
		}
		pkt, err := csiparse.ParseLine(line)
		if err != nil {
			log.Printf("udp: parse falhou de %s: %v | %q", ip, err, truncate(line, 80))
			continue
		}
		slot, isNew := l.touch(ip, pkt.NodeID)
		pkt.NodeID = slot
		l.total.Add(1)
		l.sink.IngestNode(pkt)
		if isNew {
			l.publishLive()
		}
		l.maybeLog()
	}
}

func (l *Listener) handleLink(ip, line string) {
	sample, err := csiparse.ParseLinkLine(line)
	if err != nil {
		log.Printf("udp: link parse failed from %s: %v | %q", ip, err, truncate(line, 80))
		return
	}
	slot, isNew := l.touch(ip, sample.NodeID)
	sample.NodeID = slot
	l.sink.IngestLink(sample)
	if isNew {
		l.publishLive()
	}
}

func (l *Listener) heartbeat() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		l.publishLive()
	}
}

func (l *Listener) touch(ip, hint string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	p, ok := l.peers[ip]
	isNew := !ok
	if !ok {
		p = &peer{ip: ip}
		l.peers[ip] = p
		p.slot = l.allocSlotLocked(hint)
		l.slots[p.slot] = ip
		log.Printf("udp: novo ESP %s -> %s (hint=%q)", ip, p.slot, hint)
	}
	if hint != "" {
		p.nodeHint = hint
	}
	p.last = now
	p.pkts++
	l.recent[ip] = now
	return p.slot, isNew
}

func (l *Listener) allocSlotLocked(hint string) string {
	hint = bridgeNormalize(hint)
	if hint == "ESP_NODE_01" || hint == "ESP_NODE_02" {
		if owner, used := l.slots[hint]; !used || owner == "" {
			return hint
		}
	}
	if _, used := l.slots["ESP_NODE_01"]; !used {
		return "ESP_NODE_01"
	}
	if _, used := l.slots["ESP_NODE_02"]; !used {
		return "ESP_NODE_02"
	}
	return "ESP_NODE_01"
}

func bridgeNormalize(id string) string {
	switch id {
	case "", "1", "node1", "ESP1":
		return "ESP_NODE_01"
	case "2", "node2", "ESP2":
		return "ESP_NODE_02"
	default:
		return id
	}
}

func (l *Listener) publishLive() {
	l.mu.Lock()
	now := time.Now()
	for ip, p := range l.peers {
		if now.Sub(p.last) > peerTTL {
			log.Printf("udp: ESP offline %s (%s)", p.slot, ip)
			l.recent[ip] = p.last
			delete(l.slots, p.slot)
			delete(l.peers, ip)
		}
	}
	for ip, ts := range l.recent {
		if now.Sub(ts) > stimMemory {
			delete(l.recent, ip)
		}
	}
	live := make([]string, 0, len(l.peers))
	for _, p := range l.peers {
		live = append(live, p.slot)
	}
	l.mu.Unlock()
	sort.Strings(live)
	uniq := unique(live)
	l.sink.SetUDPLive(uniq)
}

func (l *Listener) maybeLog() {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastLog) < 3*time.Second {
		return
	}
	l.lastLog = now
	parts := make([]string, 0, len(l.peers))
	for _, p := range l.peers {
		parts = append(parts, p.slot+"@"+p.ip)
	}
	sort.Strings(parts)
	log.Printf("udp: vivos=%d total_pkts=%d %v", len(parts), l.total.Load(), parts)
}

func (l *Listener) PeerIPs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	seen := map[string]bool{}
	out := make([]string, 0, len(l.peers)+len(l.recent))
	for _, p := range l.peers {
		if !seen[p.ip] {
			seen[p.ip] = true
			out = append(out, p.ip)
		}
	}
	for ip, ts := range l.recent {
		if now.Sub(ts) > stimMemory {
			continue
		}
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	sort.Strings(out)
	return out
}

func (l *Listener) Snapshot() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := make([]map[string]any, 0, len(l.peers))
	for _, p := range l.peers {
		list = append(list, map[string]any{
			"ip":     p.ip,
			"slot":   p.slot,
			"hint":   p.nodeHint,
			"pkts":   p.pkts,
			"age_ms": time.Since(p.last).Milliseconds(),
		})
	}
	recent := make([]string, 0, len(l.recent))
	now := time.Now()
	for ip, ts := range l.recent {
		if now.Sub(ts) <= stimMemory {
			recent = append(recent, ip)
		}
	}
	sort.Strings(recent)
	return map[string]any{
		"listening": l.ok.Load(),
		"addr":      l.addr,
		"peers":     list,
		"recent":    recent,
		"total":     l.total.Load(),
	}
}

func addrIP(addr net.Addr) string {
	if a, ok := addr.(*net.UDPAddr); ok && a.IP != nil {
		return a.IP.String()
	}
	s := addr.String()
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return s
	}
	return host
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
