package ping

import (
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type TargetSource func() []string

func RunDaemon(routerIP string, every time.Duration, extras TargetSource) {
	if every < 20*time.Millisecond {
		every = 20 * time.Millisecond
	}
	log.Printf("stim CSI -> peers/UDP + router %s a cada %s", routerIP, every)

	stim := time.NewTicker(every)
	defer stim.Stop()
	routerTick := time.NewTicker(time.Second)
	defer routerTick.Stop()

	for {
		select {
		case <-stim.C:
			var targets []string
			if extras != nil {
				targets = uniqueTargets(extras()...)
			}
			for _, ip := range targets {
				go udpPoke(ip)
			}
		case <-routerTick.C:
			if routerIP != "" {
				go pingOnce(routerIP)
			}
		}
	}
}

func uniqueTargets(in ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func udpPoke(ip string) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(ip, "9"), 40*time.Millisecond)
	if err != nil {
		return
	}
	_, _ = conn.Write([]byte{1, 2, 3, 4})
	_ = conn.Close()
}

func pingOnce(ip string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("ping", "-n", "1", "-w", "100", ip)
	default:
		cmd = exec.Command("ping", "-c", "1", "-W", "1", ip)
	}
	if err := cmd.Run(); err == nil {
		return
	}
	udpPoke(ip)
}

func StaticExtras(csv string) TargetSource {
	parts := uniqueTargets(strings.Split(csv, ",")...)
	if len(parts) == 0 {
		return nil
	}
	return func() []string { return parts }
}

func MemoPeers(src TargetSource, every time.Duration) TargetSource {
	if src == nil {
		return nil
	}
	var mu sync.Mutex
	var cached []string
	var last time.Time
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < every && cached != nil {
			return append([]string(nil), cached...)
		}
		cached = src()
		last = time.Now()
		return append([]string(nil), cached...)
	}
}
