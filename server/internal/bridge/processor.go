package bridge

import (
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	zoneCount     = 10
	zoneAppear    = 8
	zoneDisappear = 28
	zoneEnergyOn  = 18.0
	zoneEnergyOff = 9.0
	minPersonSep  = 0.75
	stillMotion   = 4.5
	posAlphaMove  = 0.10
	posAlphaStill = 0.012
	posDeadStill  = 0.28
	maxPeopleSoft = 8
)

type zoneTrack struct {
	energy   float64
	prevE    float64
	motion   float64
	xEMA     float64
	yEMA     float64
	hits     int
	miss     int
	alive    bool
	pose     string
	poseHold int
	peak     bool
}

type Processor struct {
	hub    *Hub
	in     chan CSIPacket
	ml     *InferenceClient
	syncer *NodeSyncer
	layout *Layout

	mu       sync.Mutex
	emaAmp   []float64
	emaPhase []float64
	baseline []float64
	ready    bool
	zones    [zoneCount]zoneTrack
	quiet    int

	udpNodes   atomic.Int32
	udpLive    []string
	pktCount   atomic.Uint64
	lastRate   float64
	stereo     atomic.Bool
	lastUDPPub atomic.Int64
	lastPeople []Person
	lastCount  int
	lastPres   bool
}

func NewProcessor(hub *Hub, ml *InferenceClient, layout *Layout) *Processor {
	p := &Processor{
		hub:    hub,
		ml:     ml,
		layout: layout,
		in:     make(chan CSIPacket, 256),
		syncer: NewNodeSyncer(),
	}
	xs := zoneXs()
	for i := range p.zones {
		p.zones[i].xEMA = xs[i]
		p.zones[i].pose = "standing"
	}
	go p.rateLoop()
	go p.stereoLoop()
	return p
}

func zoneXs() [zoneCount]float64 {
	room := GetAppRoom()
	hw := room.Width * 0.5
	step := room.Width / float64(zoneCount+1)
	var xs [zoneCount]float64
	for i := 0; i < zoneCount; i++ {
		xs[i] = -hw + step*float64(i+1)
	}
	return xs
}

func (p *Processor) SetUDPLive(nodes []string) {
	norm := make([]string, 0, len(nodes))
	live := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		id := normalizeNodeID(n)
		if id == "" || live[id] {
			continue
		}
		live[id] = true
		norm = append(norm, id)
	}
	n := len(norm)
	wasStereo := p.stereo.Load()
	p.udpNodes.Store(int32(n))
	p.stereo.Store(live["ESP_NODE_01"] && live["ESP_NODE_02"])

	p.mu.Lock()
	prev := append([]string(nil), p.udpLive...)
	p.udpLive = norm
	p.mu.Unlock()

	if p.syncer != nil {
		p.syncer.ClearAbsent(live)
	}
	if p.ml != nil {
		p.ml.ClearAbsent(live)
	}
	if p.layout != nil {
		p.layout.SetLive(live)
	}
	if n == 0 {
		p.mu.Lock()
		p.lastPeople = nil
		p.lastCount = 0
		p.lastPres = false
		p.mu.Unlock()
	}

	changed := len(prev) != len(norm)
	if !changed {
		for i := range norm {
			if i >= len(prev) || prev[i] != norm[i] {
				changed = true
				break
			}
		}
	}
	now := time.Now().UnixMilli()
	last := p.lastUDPPub.Load()
	if changed || wasStereo != p.stereo.Load() || now-last > 400 {
		p.lastUDPPub.Store(now)
		p.publishUDPStatus()
	}
}

func (p *Processor) rememberPeople(frame Frame) Frame {
	p.mu.Lock()
	if frame.Presence && len(frame.People) > 0 {
		p.lastPeople = append([]Person(nil), frame.People...)
		p.lastCount = frame.Count
		p.lastPres = true
	}
	p.mu.Unlock()
	return frame
}

func (p *Processor) holdPeople(frame Frame) Frame {
	if frame.Presence && len(frame.People) > 0 {
		return p.rememberPeople(frame)
	}
	people, count, ok := p.snapshotPeople()
	if !ok || len(people) == 0 {
		return frame
	}
	frame.People = people
	frame.Count = count
	frame.Presence = true
	return frame
}

func (p *Processor) snapshotPeople() ([]Person, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]Person(nil), p.lastPeople...)
	return out, p.lastCount, p.lastPres
}

func (p *Processor) publishUDPStatus() {
	live := p.liveNodes()
	n := len(live)
	people, count, pres := p.snapshotPeople()
	frame := Frame{
		People:    people,
		Count:     count,
		Presence:  pres && n > 0,
		CSIRate:   p.lastRate,
		UdpOK:     n > 0,
		UDPNodes:  n,
		UDPLive:   live,
		Stereo:    p.stereo.Load(),
		Timestamp: time.Now().UnixMilli(),
		Room:      GetAppRoom(),
	}
	if p.ml != nil {
		if resp, ok := p.ml.Last(); ok && resp.Source != "" {
			frame.MLSource = resp.Source
			frame.Calibrated = resp.Calibrated
		}
	}
	p.hub.Broadcast(frame)
}

func (p *Processor) liveNodes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.liveNodesLocked()
}

func (p *Processor) liveNodesLocked() []string {
	out := make([]string, len(p.udpLive))
	copy(out, p.udpLive)
	return out
}

func (p *Processor) Syncer() *NodeSyncer { return p.syncer }

func (p *Processor) Ingest(pkt CSIPacket) {
	if pkt.NodeID == "" {
		pkt.NodeID = "ESP_NODE_01"
	}
	p.IngestNode(pkt)
}

func (p *Processor) IngestLink(sample LinkSample) {
	if p.layout != nil {
		p.layout.ObserveLink(sample)
	}
}

func (p *Processor) IngestNode(pkt CSIPacket) {
	if p.layout != nil {
		p.layout.Observe(pkt)
	}
	if p.syncer != nil {
		p.syncer.Push(pkt)
	}
	if p.ml != nil {
		p.ml.Push(pkt)
	}
	select {
	case p.in <- pkt:
	default:
	}
}

func (p *Processor) Run() {
	const alpha = 0.18
	for pkt := range p.in {
		node := normalizeNodeID(pkt.NodeID)
		if p.stereo.Load() && node == "ESP_NODE_02" {
			continue
		}
		p.pktCount.Add(1)
		frame := p.estimate(pkt, alpha)
		frame.UDPNodes = int(p.udpNodes.Load())
		frame.UDPLive = p.liveNodes()
		frame.Stereo = p.stereo.Load()
		frame = p.mergeML(frame)
		frame = p.holdPeople(frame)
		p.hub.Broadcast(frame)
	}
}

func (p *Processor) stereoLoop() {
	const alpha = 0.18
	for pair := range p.syncer.Out() {
		p.pktCount.Add(1)
		pkt := blendStereo(pair.Node1, pair.Node2)
		frame := p.estimate(pkt, alpha)
		frame.UDPNodes = int(p.udpNodes.Load())
		frame.UDPLive = p.liveNodes()
		frame.Stereo = true
		frame = p.mergeML(frame)
		frame = p.holdPeople(frame)
		p.hub.Broadcast(frame)
	}
}

func blendStereo(a, b CSIPacket) CSIPacket {
	n := len(a.Amp)
	if len(b.Amp) < n {
		n = len(b.Amp)
	}
	if n == 0 {
		if len(a.Amp) > 0 {
			return a
		}
		return b
	}
	amp := make([]float64, n)
	phase := make([]float64, n)
	for i := 0; i < n; i++ {
		amp[i] = 0.5 * (a.Amp[i] + b.Amp[i])
		phase[i] = 0.5 * (a.Phase[i] + b.Phase[i])
	}
	return CSIPacket{
		Seq:    a.Seq,
		Amp:    amp,
		Phase:  phase,
		NodeID: "STEREO",
		TsMs:   pairTs(a, b),
	}
}

func pairTs(a, b CSIPacket) int64 {
	if a.TsMs > 0 && b.TsMs > 0 {
		return (a.TsMs + b.TsMs) / 2
	}
	if a.TsMs > 0 {
		return a.TsMs
	}
	return b.TsMs
}

func (p *Processor) mergeML(frame Frame) Frame {
	if p.ml == nil || !p.ml.Enabled() {
		return frame
	}
	resp, ok := p.ml.Last()
	if !ok {
		return frame
	}
	frame.Calibrated = resp.Calibrated
	if resp.Source != "" {
		frame.MLSource = resp.Source
	}
	if len(resp.Persons) == 0 || len(frame.People) == 0 {
		return frame
	}

	used := make([]bool, len(frame.People))
	for _, ip := range resp.Persons {
		if ip.Confidence < 0.45 {
			continue
		}
		bestJ := -1
		bestD := 1e9
		for j := range frame.People {
			if used[j] {
				continue
			}
			dx := frame.People[j].X - ip.X
			dy := frame.People[j].Y - ip.Z
			d := dx*dx + dy*dy
			if d < bestD {
				bestD = d
				bestJ = j
			}
		}
		if bestJ < 0 {
			continue
		}
		used[bestJ] = true
		frame.People[bestJ].Pose = mapPosture(ip.Posture)
		frame.People[bestJ].Confidence = ip.Confidence
		frame.People[bestJ].Profile = mapProfile(ip.Profile)

		w := 0.08
		if frame.Calibrated {
			w = 0.2
		}
		if frame.People[bestJ].Motion < 0.25 {
			w *= 0.15
		}
		if bestD > 2.5 {
			w *= 0.25
		}
		frame.People[bestJ].X = (1-w)*frame.People[bestJ].X + w*ip.X
		frame.People[bestJ].Y = (1-w)*frame.People[bestJ].Y + w*ip.Z
	}
	return frame
}

func mapProfile(p string) string {
	switch p {
	case "child", "crianca", "kid":
		return "child"
	case "pet", "animal", "dog", "cao":
		return "pet"
	default:
		return "adult"
	}
}

func mapPosture(p string) string {
	switch p {
	case "seated", "sitting", "sentado":
		return "sitting"
	case "lying", "deitado":
		return "lying"
	case "walking", "caminhando", "walk", "running", "correndo", "run":
		return "standing"
	case "arm_l_up", "braco_esquerdo", "left_arm":
		return "arm_l_up"
	case "arm_r_up", "braco_direito", "right_arm":
		return "arm_r_up"
	case "leg_l_up", "perna_esquerda", "left_leg":
		return "leg_l_up"
	case "leg_r_up", "perna_direita", "right_leg":
		return "leg_r_up"
	default:
		return "standing"
	}
}

func (p *Processor) rateLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prev uint64
	for range t.C {
		cur := p.pktCount.Load()
		p.mu.Lock()
		p.lastRate = float64(cur - prev)
		p.mu.Unlock()
		prev = cur
	}
}

func estimatePose(energy, motion, y float64, still bool, x float64) string {
	if energy < 16 && motion < 4 && y < 0.35 {
		return "lying"
	}
	if energy < 26 && motion < 8 && motion < 5.5 {
		return "sitting"
	}
	left := x < 0
	if still && motion >= 6.0 {
		if motion >= 9.0 {
			if left {
				return "arm_l_up"
			}
			return "arm_r_up"
		}
		if left {
			return "leg_l_up"
		}
		return "leg_r_up"
	}
	return "standing"
}

func (p *Processor) anyAlive() bool {
	for i := range p.zones {
		if p.zones[i].alive {
			return true
		}
	}
	return false
}

func (p *Processor) estimate(pkt CSIPacket, alpha float64) Frame {
	p.mu.Lock()
	defer p.mu.Unlock()

	ts := time.Now().UnixMilli()
	room := GetAppRoom()
	empty := Frame{
		People:    []Person{},
		UdpOK:     p.udpNodes.Load() > 0,
		UDPNodes:  int(p.udpNodes.Load()),
		UDPLive:   p.liveNodesLocked(),
		Stereo:    p.stereo.Load(),
		CSIRate:   p.lastRate,
		Seq:       pkt.Seq,
		Timestamp: ts,
		Room:      room,
	}

	n := len(pkt.Amp)
	if n == 0 {
		return empty
	}

	if len(p.emaAmp) != n {
		p.emaAmp = make([]float64, n)
		p.emaPhase = make([]float64, n)
		p.baseline = make([]float64, n)
		copy(p.emaAmp, pkt.Amp)
		copy(p.emaPhase, pkt.Phase)
		copy(p.baseline, pkt.Amp)
		p.ready = false
	}

	var sumDiff float64
	var sumDelta float64
	zoneEnergy := [zoneCount]float64{}
	zoneN := [zoneCount]int{}

	for i := 0; i < n; i++ {
		prev := p.emaAmp[i]
		p.emaAmp[i] = alpha*pkt.Amp[i] + (1-alpha)*p.emaAmp[i]
		p.emaPhase[i] = alpha*pkt.Phase[i] + (1-alpha)*p.emaPhase[i]
		d := p.emaAmp[i] - p.baseline[i]
		sumDiff += d * d
		dd := p.emaAmp[i] - prev
		sumDelta += dd * dd

		z := i * zoneCount / n
		if z >= zoneCount {
			z = zoneCount - 1
		}
		zoneEnergy[z] += d*d + dd*dd*4
		zoneN[z]++
	}

	globalEnergy := math.Sqrt(sumDiff / float64(n))
	globalMotion := math.Sqrt(sumDelta/float64(n)) * 10
	still := globalMotion < stillMotion

	if !p.ready {
		p.ready = true
	} else if !p.anyAlive() && globalEnergy < 8 && still {
		for i := 0; i < n; i++ {
			p.baseline[i] = 0.015*p.emaAmp[i] + 0.985*p.baseline[i]
		}
	}

	activity := globalEnergy + globalMotion*1.2
	if activity < 7 {
		p.quiet++
	} else {
		p.quiet = 0
	}

	if p.quiet > 35 {
		for z := range p.zones {
			p.zones[z].hits = 0
			p.zones[z].miss++
			p.zones[z].energy *= 0.92
			p.zones[z].motion *= 0.9
			p.zones[z].peak = false
			if p.zones[z].miss > zoneDisappear {
				p.zones[z].alive = false
				p.zones[z].miss = 0
			}
		}
	}

	xs := zoneXs()
	energies := make([]float64, zoneCount)
	for z := 0; z < zoneCount; z++ {
		e := 0.0
		if zoneN[z] > 0 {
			e = math.Sqrt(zoneEnergy[z] / float64(zoneN[z]))
		}
		tr := &p.zones[z]
		delta := math.Abs(e - tr.prevE)
		tr.prevE = e
		tr.energy = alpha*e + (1-alpha)*tr.energy
		tr.motion = alpha*delta*10 + (1-alpha)*tr.motion
		energies[z] = tr.energy
	}

	for z := 0; z < zoneCount; z++ {
		left := 0.0
		right := 0.0
		if z > 0 {
			left = energies[z-1]
		}
		if z < zoneCount-1 {
			right = energies[z+1]
		}
		isPeak := energies[z] >= left && energies[z] >= right && energies[z] > zoneEnergyOn*0.85
		p.zones[z].peak = isPeak
	}

	for z := 0; z < zoneCount; z++ {
		tr := &p.zones[z]
		score := tr.energy + tr.motion*1.2
		candidate := tr.peak || (tr.alive && score > zoneEnergyOff)

		if tr.alive {
			if !candidate && score < zoneEnergyOff {
				tr.miss++
				if still && tr.miss > zoneDisappear/2 {
					tr.miss = zoneDisappear / 2
				}
				if tr.miss >= zoneDisappear {
					tr.alive = false
					tr.hits = 0
					tr.miss = 0
					tr.energy *= 0.3
					tr.motion *= 0.3
				}
			} else {
				tr.miss = 0
			}
		} else if tr.peak && score > zoneEnergyOn {
			tr.hits++
			if tr.hits >= zoneAppear {
				tr.alive = true
				tr.miss = 0
				tr.xEMA = xs[z]
			}
		} else {
			tr.hits = 0
		}
	}

	halfD := room.Depth * 0.5
	nearZ := room.ESPZ
	farZ := room.TxZ
	if farZ < nearZ {
		nearZ, farZ = farZ, nearZ
	}
	if math.Abs(farZ-nearZ) < 1 {
		farZ = nearZ + room.Depth*0.7
	}

	aliveE := make([]float64, 0, zoneCount)
	for z := 0; z < zoneCount; z++ {
		if p.zones[z].alive {
			aliveE = append(aliveE, p.zones[z].energy)
		}
	}
	eMin, eMax := 0.0, 1.0
	if len(aliveE) > 0 {
		eMin, eMax = aliveE[0], aliveE[0]
		for _, e := range aliveE[1:] {
			if e < eMin {
				eMin = e
			}
			if e > eMax {
				eMax = e
			}
		}
	}

	type cand struct {
		id    int
		x, y  float64
		pose  string
		score float64
		mot   float64
	}
	cands := make([]cand, 0, zoneCount)

	for z := 0; z < zoneCount; z++ {
		tr := &p.zones[z]
		if !tr.alive {
			continue
		}
		rawX := xs[z]
		rel := 0.5
		if eMax > eMin+1e-6 {
			rel = (tr.energy - eMin) / (eMax - eMin)
		}
		absDepth := clamp(tr.energy/45, 0, 1)
		depthNorm := clamp(0.55*rel+0.45*absDepth, 0, 1)
		span := float64(z+1) / float64(zoneCount+1)
		rawZ := nearZ + depthNorm*(farZ-nearZ)*0.92
		rawZ = 0.75*rawZ + 0.25*(nearZ+span*(farZ-nearZ))

		a := posAlphaMove
		jump := math.Hypot(rawX-tr.xEMA, rawZ-tr.yEMA)
		if still || tr.motion < stillMotion {
			if jump < posDeadStill {
				a = 0
			} else {
				a = posAlphaStill
			}
		}
		if a > 0 {
			tr.xEMA = a*rawX + (1-a)*tr.xEMA
			tr.yEMA = a*rawZ + (1-a)*tr.yEMA
		}

		nextPose := estimatePose(tr.energy, tr.motion, depthNorm, still || tr.motion < stillMotion && math.Abs(tr.xEMA-rawX) < 0.15 && math.Abs(tr.yEMA-rawZ) < 0.15, tr.xEMA)
		holdNeed := 10
		if strings.Contains(nextPose, "_up") {
			holdNeed = 4
		}
		if nextPose != tr.pose {
			tr.poseHold++
			if tr.poseHold >= holdNeed {
				tr.pose = nextPose
				tr.poseHold = 0
			}
		} else {
			tr.poseHold = 0
		}

		cands = append(cands, cand{
			id:    z + 1,
			x:     clamp(tr.xEMA, -room.Width*0.48, room.Width*0.48),
			y:     clamp(tr.yEMA, -halfD*0.95, halfD*0.95),
			pose:  tr.pose,
			score: tr.energy + tr.motion,
			mot:   clamp(tr.motion/15, 0, 1),
		})
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > maxPeopleSoft {
		cands = cands[:maxPeopleSoft]
	}
	people := make([]Person, 0, len(cands))
	for _, c := range cands {
		tooClose := false
		for _, kept := range people {
			dx := c.x - kept.X
			dy := c.y - kept.Y
			if math.Sqrt(dx*dx+dy*dy) < minPersonSep {
				tooClose = true
				break
			}
		}
		if tooClose {
			continue
		}
		people = append(people, Person{
			ID:     c.id,
			X:      c.x,
			Y:      c.y,
			Pose:   c.pose,
			Motion: c.mot,
		})
	}

	frame := Frame{
		People:    people,
		Count:     len(people),
		Presence:  len(people) > 0,
		CSIRate:   p.lastRate,
		UdpOK:     p.udpNodes.Load() > 0,
		UDPNodes:  int(p.udpNodes.Load()),
		UDPLive:   p.liveNodesLocked(),
		Stereo:    p.stereo.Load(),
		Seq:       pkt.Seq,
		Timestamp: ts,
		Room:      room,
	}
	if len(people) > 0 {
		var cx, cy float64
		for _, person := range people {
			cx += person.X
			cy += person.Y
		}
		frame.X = cx / float64(len(people))
		frame.Y = cy / float64(len(people))
	}
	return frame
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
