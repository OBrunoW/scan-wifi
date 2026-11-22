package bridge

import (
	"math"
	"sync"
	"time"

	"github.com/bruno/scan-wifi/internal/geometry"
)

const (
	layoutEvaluateEvery  = time.Second
	layoutMedianWindow   = 5 * time.Second
	layoutEMAAlpha       = 0.1
	layoutReadySamples   = 20
	layoutStaleTTL       = 120 * time.Second
	layoutPeerTTL        = 120 * time.Second
	layoutCommitPersist  = 12
	layoutMinThresholdM  = 0.40
	layoutThresholdRatio = 0.10
	layoutDefaultAngle   = 32.0 * math.Pi / 180.0
	layoutDefaultRoomM   = 12.0

	geometryNone         = "none"
	geometryTriangle     = "triangle"
	geometryDefaultAngle = "default_angle"
	scaleNone            = "none"
	scaleMeasured        = "measured"
	scaleEstimated       = "estimated"
)

var layoutSlots = [2]string{"ESP_NODE_01", "ESP_NODE_02"}

type nodeState int

const (
	nodeAbsent nodeState = iota
	nodeWarming
	nodeActive
	nodeStale
)

type linkFilter struct {
	window  *geometry.MedianWindow
	ema     geometry.EMA
	total   int
	firstAt time.Time
	lastAt  time.Time
}

func newLinkFilter() *linkFilter {
	return &linkFilter{
		window: geometry.NewMedianWindow(layoutMedianWindow),
		ema:    geometry.NewEMA(layoutEMAAlpha),
	}
}

func (f *linkFilter) add(at time.Time, rssi float64) {
	if f.total == 0 {
		f.firstAt = at
	}
	f.window.Add(at, rssi)
	f.total++
	f.lastAt = at
}

func (f *linkFilter) evaluate(now time.Time) {
	if f.total == 0 {
		return
	}
	if now.Sub(f.firstAt) < layoutMedianWindow {
		return
	}
	if median, ok := f.window.Median(now); ok {
		f.ema.Add(median)
	}
}

func (f *linkFilter) ready() bool {
	return f.total >= layoutReadySamples && f.ema.Samples() > 0
}

func (f *linkFilter) expired(now time.Time) bool {
	return f.total > 0 && now.Sub(f.lastAt) > layoutPeerTTL
}

type layoutNode struct {
	mac      string
	state    nodeState
	live     bool
	outSince time.Time
	filter   *linkFilter
	stable   *geometry.StablePoint
}

func newLayoutNode() *layoutNode {
	n := &layoutNode{}
	n.reset()
	return n
}

func (n *layoutNode) reset() {
	n.state = nodeAbsent
	n.filter = newLinkFilter()
	n.stable = geometry.NewStablePoint(layoutCommitPersist)
}

func (n *layoutNode) markSeen(now time.Time) {
	if n.state != nodeAbsent {
		return
	}
	n.state = nodeWarming
	if !n.live {
		n.outSince = now
	}
}

func (n *layoutNode) advance(now time.Time) {
	if n.state == nodeAbsent {
		return
	}
	if n.live {
		if n.state == nodeStale {
			n.state = nodeActive
		}
		return
	}
	if now.Sub(n.outSince) > layoutStaleTTL {
		n.reset()
		return
	}
	if n.state == nodeActive {
		n.state = nodeStale
	}
}

func (n *layoutNode) visible() bool {
	return n.state == nodeActive || n.state == nodeStale
}

type layoutCandidate struct {
	geometry string
	scale    string
	points   [2]geometry.Point
	placed   [2]bool
}

type Layout struct {
	mu sync.Mutex

	roomW     float64
	roomD     float64
	baselineM float64
	model     geometry.PathLoss
	now       func() time.Time
	publish   func(Room)

	nodes [2]*layoutNode
	peer  *linkFilter

	publishedGeometry string
	publishedScale    string
	labelPending      int
	lastRoom          Room
}

func NewLayout(roomW, roomD, baselineM float64) *Layout {
	return NewLayoutWith(roomW, roomD, baselineM, time.Now, SetAppRoom)
}

func NewLayoutWith(roomW, roomD, baselineM float64, now func() time.Time, publish func(Room)) *Layout {
	if roomW <= 0 {
		roomW = layoutDefaultRoomM
	}
	if roomD <= 0 {
		roomD = layoutDefaultRoomM
	}
	if baselineM < 0 {
		baselineM = 0
	}
	l := &Layout{
		roomW:             roomW,
		roomD:             roomD,
		baselineM:         baselineM,
		model:             geometry.DefaultPathLoss(),
		now:               now,
		publish:           publish,
		nodes:             [2]*layoutNode{newLayoutNode(), newLayoutNode()},
		peer:              newLinkFilter(),
		publishedGeometry: geometryNone,
		publishedScale:    scaleNone,
	}
	l.lastRoom = l.buildRoom()
	publish(l.lastRoom)
	return l
}

func layoutSlotIndex(nodeID string) int {
	normalized := normalizeNodeID(nodeID)
	for i, slot := range layoutSlots {
		if slot == normalized {
			return i
		}
	}
	return -1
}

func (l *Layout) Observe(pkt CSIPacket) {
	if !pkt.HasRSSI {
		return
	}
	index := layoutSlotIndex(pkt.NodeID)
	if index < 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	node := l.nodes[index]
	node.markSeen(now)
	node.filter.add(now, pkt.RSSI)
}

func (l *Layout) ObserveLink(sample LinkSample) {
	index := layoutSlotIndex(sample.NodeID)
	if index < 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if sample.SelfMAC != "" {
		l.nodes[index].mac = sample.SelfMAC
	}
	other := l.nodes[1-index].mac
	if other == "" || sample.SrcMAC != other || sample.SrcMAC == l.nodes[index].mac {
		return
	}
	l.peer.add(l.now(), sample.RSSI)
}

func (l *Layout) SetLive(live map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for i, slot := range layoutSlots {
		node := l.nodes[i]
		wasLive := node.live
		node.live = live[slot]
		if node.live {
			node.outSince = time.Time{}
		} else if wasLive {
			node.outSince = now
		}
	}
}

func (l *Layout) Run() {
	ticker := time.NewTicker(layoutEvaluateEvery)
	defer ticker.Stop()
	for range ticker.C {
		l.Evaluate(l.now())
	}
}

func (l *Layout) Evaluate(now time.Time) {
	l.mu.Lock()
	room, changed := l.evaluateLocked(now)
	l.mu.Unlock()
	if changed {
		l.publish(room)
	}
}

func (l *Layout) evaluateLocked(now time.Time) (Room, bool) {
	visibleBefore := l.visibility()
	l.advanceStates(now)
	l.filterLinks(now)
	candidate := l.buildCandidate()
	committed := l.offerPoints(candidate)
	visibilityChanged := l.visibility() != visibleBefore
	l.updateLabels(candidate, committed || visibilityChanged)
	room := l.buildRoom()
	if room == l.lastRoom {
		return room, false
	}
	l.lastRoom = room
	return room, true
}

func (l *Layout) visibility() [2]bool {
	return [2]bool{l.nodes[0].visible(), l.nodes[1].visible()}
}

func (l *Layout) advanceStates(now time.Time) {
	for _, node := range l.nodes {
		node.advance(now)
	}
	anyAbsent := l.nodes[0].state == nodeAbsent || l.nodes[1].state == nodeAbsent
	if anyAbsent || l.peer.expired(now) {
		l.peer = newLinkFilter()
	}
}

func (l *Layout) filterLinks(now time.Time) {
	for _, node := range l.nodes {
		if node.state != nodeAbsent {
			node.filter.evaluate(now)
		}
	}
	l.peer.evaluate(now)
}

func (l *Layout) nodeReady(index int) bool {
	node := l.nodes[index]
	return node.state != nodeAbsent && node.filter.ready()
}

func (l *Layout) buildCandidate() layoutCandidate {
	ready := [2]bool{l.nodeReady(0), l.nodeReady(1)}
	var legs [2]float64
	for i, node := range l.nodes {
		if ready[i] {
			legs[i] = l.model.Distance(node.filter.ema.Value())
		}
	}
	switch {
	case ready[0] && ready[1] && l.peer.ready():
		return l.triangleCandidate(legs)
	case ready[0] || ready[1]:
		return l.defaultAngleCandidate(legs, ready)
	default:
		return layoutCandidate{geometry: geometryNone, scale: scaleNone}
	}
}

func (l *Layout) triangleCandidate(legs [2]float64) layoutCandidate {
	base := l.model.Distance(l.peer.ema.Value())
	candidate := layoutCandidate{geometry: geometryTriangle, scale: scaleEstimated, placed: [2]bool{true, true}}
	if l.baselineM > 0 {
		factor := l.baselineM / base
		legs[0] *= factor
		legs[1] *= factor
		base = l.baselineM
		candidate.scale = scaleMeasured
	}
	candidate.points[0], candidate.points[1], _ = geometry.PlaceTriangle(legs[0], legs[1], base)
	if candidate.scale == scaleEstimated {
		l.frame(&candidate)
	}
	return candidate
}

func (l *Layout) defaultAngleCandidate(legs [2]float64, ready [2]bool) layoutCandidate {
	angles := [2]float64{-layoutDefaultAngle, layoutDefaultAngle}
	candidate := layoutCandidate{geometry: geometryDefaultAngle, scale: scaleEstimated, placed: ready}
	for i := range legs {
		if ready[i] {
			candidate.points[i] = geometry.PlaceByAngle(legs[i], angles[i])
		}
	}
	l.frame(&candidate)
	return candidate
}

func (l *Layout) frame(candidate *layoutCandidate) {
	var span float64
	for i, point := range candidate.points {
		if candidate.placed[i] {
			span = math.Max(span, geometry.Distance(geometry.Point{}, point))
		}
	}
	if candidate.placed[0] && candidate.placed[1] {
		span = math.Max(span, geometry.Distance(candidate.points[0], candidate.points[1]))
	}
	factor := geometry.FrameFactor(span, l.roomW, l.roomD)
	for i := range candidate.points {
		candidate.points[i] = candidate.points[i].Scale(factor)
	}
}

func (l *Layout) offerPoints(candidate layoutCandidate) bool {
	committed := false
	for i, node := range l.nodes {
		if !candidate.placed[i] || node.state == nodeStale || node.state == nodeAbsent {
			continue
		}
		_, changed := node.stable.Offer(candidate.points[i], commitThreshold(node.stable))
		if !changed {
			continue
		}
		node.state = nodeActive
		committed = true
	}
	return committed
}

func commitThreshold(stable *geometry.StablePoint) float64 {
	published, has := stable.Published()
	if !has {
		return layoutMinThresholdM
	}
	return math.Max(layoutMinThresholdM, layoutThresholdRatio*math.Hypot(published.X, published.Z))
}

func (l *Layout) updateLabels(candidate layoutCandidate, adoptNow bool) {
	if candidate.geometry == l.publishedGeometry && candidate.scale == l.publishedScale {
		l.labelPending = 0
		return
	}
	l.labelPending++
	if !adoptNow && l.labelPending < layoutCommitPersist {
		return
	}
	l.publishedGeometry = candidate.geometry
	l.publishedScale = candidate.scale
	l.labelPending = 0
}

func (l *Layout) buildRoom() Room {
	room := Room{
		Width:       l.roomW,
		Depth:       l.roomD,
		TxOK:        true,
		MapGeometry: l.publishedGeometry,
		MapScale:    l.publishedScale,
	}
	var points [2]geometry.Point
	for i, node := range l.nodes {
		if node.visible() {
			points[i], _ = node.stable.Published()
		}
	}
	first, second := l.nodes[0].visible(), l.nodes[1].visible()
	room.ESP1OK, room.ESP2OK = first, second
	room.ESPX, room.ESPZ = points[0].X, points[0].Z
	room.ESP2X, room.ESP2Z = points[1].X, points[1].Z
	switch {
	case first:
		room.LinkM = geometry.Distance(geometry.Point{}, points[0])
	case second:
		room.LinkM = geometry.Distance(geometry.Point{}, points[1])
	}
	if first && second && l.publishedGeometry == geometryTriangle {
		room.ESPLinkM = geometry.Distance(points[0], points[1])
	}
	return room
}
