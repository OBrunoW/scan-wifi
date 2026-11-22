package bridge

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/bruno/scan-wifi/internal/geometry"
)

const (
	nodeOne = "ESP_NODE_01"
	nodeTwo = "ESP_NODE_02"

	staticSeedCount        = 200
	staticRunSeconds       = 600
	staticSamplesPerSecond = 20
	staticNoiseSDdB        = 4.0
	staticMaxExtraCommits  = 1
	staticMinStablePercent = 100
	layoutWarmupEvals      = int(layoutMedianWindow / layoutEvaluateEvery)
	macOne                 = "a4cf12000001"
	macTwo                 = "a4cf12000002"
	macRouter              = "a4cf12ffffff"
)

type testClock struct {
	current time.Time
}

func (c *testClock) now() time.Time { return c.current }

func (c *testClock) advance(span time.Duration) { c.current = c.current.Add(span) }

type layoutRig struct {
	t      *testing.T
	layout *Layout
	clock  *testClock
	rooms  []Room

	peerFlip bool
}

func newRig(t *testing.T, baselineM float64) *layoutRig {
	rig := &layoutRig{t: t, clock: &testClock{current: time.Unix(1_700_000_000, 0)}}
	rig.layout = NewLayoutWith(6, 4.5, baselineM, rig.clock.now, func(room Room) {
		rig.rooms = append(rig.rooms, room)
	})
	rig.setLive(true, true)
	rig.announce()
	return rig
}

func rssiForMeters(meters float64) float64 {
	return geometry.DefaultPathLoss().RefDBm - 25*math.Log10(meters)
}

func (r *layoutRig) setLive(one, two bool) {
	r.layout.SetLive(map[string]bool{nodeOne: one, nodeTwo: two})
}

func (r *layoutRig) announce() {
	r.layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macRouter, RSSI: -50})
	r.layout.ObserveLink(LinkSample{NodeID: nodeTwo, SelfMAC: macTwo, SrcMAC: macRouter, RSSI: -50})
}

func (r *layoutRig) feedAP(node string, rssi float64, count int) {
	for i := 0; i < count; i++ {
		r.layout.Observe(CSIPacket{NodeID: node, RSSI: rssi, HasRSSI: true})
	}
}

func (r *layoutRig) tick() {
	r.clock.advance(time.Second)
	r.layout.Evaluate(r.clock.now())
}

func (r *layoutRig) ticks(count int) {
	for i := 0; i < count; i++ {
		r.tick()
	}
}

func (r *layoutRig) room() Room { return r.rooms[len(r.rooms)-1] }

func spreadCount(total, step int) int {
	count := total / layoutWarmupEvals
	if step < total%layoutWarmupEvals {
		count++
	}
	return count
}

func (r *layoutRig) warmNode(node string, rssi float64, total int) {
	for step := 0; step < layoutWarmupEvals; step++ {
		r.feedAP(node, rssi, spreadCount(total, step))
		r.tick()
	}
}

func (r *layoutRig) warmBoth(legOne, legTwo float64) {
	for step := 0; step < layoutWarmupEvals; step++ {
		r.feedAP(nodeOne, rssiForMeters(legOne), spreadCount(layoutReadySamples, step))
		r.feedAP(nodeTwo, rssiForMeters(legTwo), spreadCount(layoutReadySamples, step))
		r.tick()
	}
}

func (r *layoutRig) feedPeerAlternating(rssi float64, count int) {
	for i := 0; i < count; i++ {
		if r.peerFlip {
			r.layout.ObserveLink(LinkSample{NodeID: nodeTwo, SelfMAC: macTwo, SrcMAC: macOne, RSSI: rssi})
		} else {
			r.layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macTwo, RSSI: rssi})
		}
		r.peerFlip = !r.peerFlip
	}
}

func (r *layoutRig) feedPeerSpread(rssi float64, total int) {
	for step := 0; step < layoutWarmupEvals; step++ {
		r.feedPeerAlternating(rssi, spreadCount(total, step))
		if step < layoutWarmupEvals-1 {
			r.tick()
		}
	}
}

func (r *layoutRig) warmPeer(rssi float64, total int) {
	r.feedPeerSpread(rssi, total)
	r.tick()
}

func countCommits(rooms []Room, pick func(Room) [2]float64) int {
	commits := 0
	for i := 1; i < len(rooms); i++ {
		if pick(rooms[i]) != pick(rooms[i-1]) {
			commits++
		}
	}
	return commits
}

func firstNode(room Room) [2]float64  { return [2]float64{room.ESPX, room.ESPZ} }
func secondNode(room Room) [2]float64 { return [2]float64{room.ESP2X, room.ESP2Z} }

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestMedianRejectsOutliersInLayout(t *testing.T) {
	rig := newRig(t, 0)
	for step := 0; step < layoutWarmupEvals; step++ {
		rig.feedAP(nodeOne, -52, 30)
		rig.feedAP(nodeOne, -20, 5)
		rig.tick()
	}
	want := geometry.DefaultPathLoss().Distance(-52)
	if !near(rig.room().LinkM, want, 0.01) {
		t.Fatalf("leg %.3f want %.3f", rig.room().LinkM, want)
	}
}

func TestConstantRSSIPublishesExpectedLeg(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmNode(nodeOne, rssiForMeters(3), 25)
	if !near(rig.room().LinkM, 3, 0.01) {
		t.Fatalf("leg %.3f", rig.room().LinkM)
	}
}

func TestNodeNeedsTwentyApSamples(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmNode(nodeOne, rssiForMeters(3), layoutReadySamples-1)
	if rig.room().ESP1OK {
		t.Fatal("esp1 must not be ok with 19 samples")
	}
	rig.feedAP(nodeOne, rssiForMeters(3), 1)
	rig.tick()
	if !rig.room().ESP1OK {
		t.Fatal("esp1 must be ok with 20 samples")
	}
}

func TestPeerNeedsTwentySamplesAcrossBothDirections(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	rig.warmPeer(rssiForMeters(3), layoutReadySamples+1)
	if !rig.layout.peer.ready() {
		t.Fatal("peer with 21 samples must be ready")
	}
	other := newRig(t, 0)
	other.warmBoth(3, 3)
	other.warmPeer(rssiForMeters(3), layoutReadySamples-1)
	if other.layout.peer.total != 19 || other.layout.peer.ready() {
		t.Fatalf("peer with 19 samples must not be ready, total %d", other.layout.peer.total)
	}
}

func noisyStaticCommits(t *testing.T, seed int64) (int, int) {
	rig := newRig(t, 0)
	noise := rand.New(rand.NewSource(seed))
	for second := 0; second < staticRunSeconds; second++ {
		for sample := 0; sample < staticSamplesPerSecond; sample++ {
			rig.layout.Observe(CSIPacket{NodeID: nodeOne, RSSI: rssiForMeters(3) + noise.NormFloat64()*staticNoiseSDdB, HasRSSI: true})
			rig.layout.Observe(CSIPacket{NodeID: nodeTwo, RSSI: rssiForMeters(3) + noise.NormFloat64()*staticNoiseSDdB, HasRSSI: true})
		}
		rig.setLive(true, true)
		rig.tick()
	}
	return countCommits(rig.rooms, firstNode), countCommits(rig.rooms, secondNode)
}

func TestStaticNodesWithNoiseCommitRarely(t *testing.T) {
	stable := 0
	for seed := int64(1); seed <= staticSeedCount; seed++ {
		first, second := noisyStaticCommits(t, seed)
		if first <= 1+staticMaxExtraCommits && second <= 1+staticMaxExtraCommits {
			stable++
		}
	}
	if stable*100 < staticSeedCount*staticMinStablePercent {
		t.Fatalf("stable seeds %d of %d", stable, staticSeedCount)
	}
}

func TestWarmupHoldsNodeBackUntilWindowIsFull(t *testing.T) {
	rig := newRig(t, 0)
	rig.feedAP(nodeOne, rssiForMeters(3), layoutReadySamples+5)
	for evaluation := 1; evaluation < layoutWarmupEvals; evaluation++ {
		rig.tick()
		if rig.room().ESP1OK {
			t.Fatalf("esp1 ok at evaluation %d before the window is full", evaluation)
		}
	}
	rig.tick()
	if !rig.room().ESP1OK {
		t.Fatalf("esp1 must be ok at evaluation %d", layoutWarmupEvals)
	}
}

func TestLegStepCommitsWithinTwentyEvaluations(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmNode(nodeOne, rssiForMeters(3), 25)
	start := len(rig.rooms) - 1
	committedAt := 0
	for second := 1; second <= 30; second++ {
		rig.feedAP(nodeOne, rssiForMeters(4), 20)
		rig.tick()
		if committedAt == 0 && countCommits(rig.rooms[start:], firstNode) > 0 {
			committedAt = second
		}
	}
	if committedAt == 0 || committedAt > 20 {
		t.Fatalf("commit at evaluation %d", committedAt)
	}
	if committedAt < layoutCommitPersist {
		t.Fatalf("commit before persistence elapsed: %d", committedAt)
	}
}

func TestShortDeviationDoesNotCommit(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmNode(nodeOne, rssiForMeters(3), 25)
	start := len(rig.rooms) - 1
	for second := 0; second < layoutCommitPersist/3; second++ {
		rig.feedAP(nodeOne, rssiForMeters(4), 20)
		rig.tick()
	}
	for second := 0; second < 60; second++ {
		rig.feedAP(nodeOne, rssiForMeters(3), 20)
		rig.tick()
	}
	if got := countCommits(rig.rooms[start:], firstNode); got != 0 {
		t.Fatalf("commits after initial: %d", got)
	}
}

func TestSetLiveNeverPublishes(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	published := len(rig.rooms)
	for step := 0; step < 120; step++ {
		rig.clock.advance(500 * time.Millisecond)
		rig.setLive(true, true)
		if step%2 == 1 {
			rig.layout.Evaluate(rig.clock.now())
		}
	}
	if len(rig.rooms) != published {
		t.Fatalf("published %d extra rooms", len(rig.rooms)-published)
	}
}

func TestStaleNodeKeepsPositionFor120Seconds(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	rig.setLive(true, false)
	rig.ticks(120)
	after := rig.room()
	if !after.ESP2OK || secondNode(after) != secondNode(before) {
		t.Fatalf("esp2 must stay frozen at 120 s: %+v", after)
	}
	rig.tick()
	if rig.room().ESP2OK {
		t.Fatal("esp2 must leave the map after 120 s")
	}
	if rig.room().MapGeometry != geometryDefaultAngle {
		t.Fatalf("geometry %q", rig.room().MapGeometry)
	}
}

func TestStaleNodeReturnsWithoutWarmup(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	rig.setLive(true, false)
	rig.ticks(60)
	rig.setLive(true, true)
	rig.ticks(3)
	if !rig.room().ESP2OK || secondNode(rig.room()) != secondNode(before) {
		t.Fatalf("esp2 must resume frozen position: %+v", rig.room())
	}
}

func TestAbsentNodeNeedsWarmupAgain(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	rig.setLive(true, false)
	rig.ticks(125)
	rig.setLive(true, true)
	rig.warmNode(nodeTwo, rssiForMeters(3), layoutReadySamples-1)
	if rig.room().ESP2OK {
		t.Fatal("esp2 must warm up again")
	}
	rig.feedAP(nodeTwo, rssiForMeters(3), 1)
	rig.tick()
	if !rig.room().ESP2OK {
		t.Fatal("esp2 must return after 20 samples")
	}
}

func TestStaleNodeIgnoresCandidateChanges(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	rig.setLive(true, false)
	for second := 0; second < 40; second++ {
		rig.feedAP(nodeOne, rssiForMeters(5), 20)
		rig.tick()
	}
	if secondNode(rig.room()) != secondNode(before) {
		t.Fatal("stale node moved")
	}
}

func warmWithPeer(rig *layoutRig, legOne, legTwo, base float64) {
	rig.warmBoth(legOne, legTwo)
	rig.feedPeerSpread(rssiForMeters(base), layoutReadySamples)
	rig.tick()
}

func TestPeerWithinThresholdChangesOnlyLabel(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	rig.feedPeerSpread(rssiForMeters(2*3*math.Sin(layoutDefaultAngle)), layoutReadySamples)
	for evaluation := 1; evaluation <= layoutCommitPersist; evaluation++ {
		rig.tick()
		if evaluation < layoutCommitPersist && rig.room().MapGeometry != geometryDefaultAngle {
			t.Fatalf("label changed at %d", evaluation)
		}
	}
	room := rig.room()
	if room.MapGeometry != geometryTriangle {
		t.Fatalf("geometry %q", room.MapGeometry)
	}
	if firstNode(room) != firstNode(before) || secondNode(room) != secondNode(before) {
		t.Fatal("positions moved")
	}
}

func TestPeerOutsideThresholdCommitsAtPersistence(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	rig.feedPeerSpread(rssiForMeters(5), layoutReadySamples)
	for evaluation := 1; evaluation <= layoutCommitPersist; evaluation++ {
		rig.tick()
		if evaluation < layoutCommitPersist && (firstNode(rig.room()) != firstNode(before) || rig.room().MapGeometry != geometryDefaultAngle) {
			t.Fatalf("changed before persistence: %d", evaluation)
		}
	}
	room := rig.room()
	if room.MapGeometry != geometryTriangle || firstNode(room) == firstNode(before) {
		t.Fatalf("expected commit and triangle label: %+v", room)
	}
}

func TestPeerFlappingDoesNotChangeMap(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(3, 3)
	before := rig.room()
	for cycle := 0; cycle < 4; cycle++ {
		rig.feedPeerSpread(rssiForMeters(5), layoutReadySamples)
		rig.ticks(5)
		rig.clock.advance(layoutPeerTTL + time.Second)
		rig.layout.Evaluate(rig.clock.now())
		rig.ticks(4)
	}
	if rig.room() != before {
		t.Fatalf("map changed: %+v", rig.room())
	}
}

func TestMeasuredScaleUsesBaseline(t *testing.T) {
	rig := newRig(t, 2.0)
	warmWithPeer(rig, 3, 3, 1.5)
	rig.ticks(layoutCommitPersist)
	room := rig.room()
	if room.MapScale != scaleMeasured || room.MapGeometry != geometryTriangle {
		t.Fatalf("labels %q %q", room.MapScale, room.MapGeometry)
	}
	if !near(room.ESPLinkM, 2.0, 0.01) {
		t.Fatalf("link %.3f", room.ESPLinkM)
	}
	if !near(room.LinkM, 4.0, 0.02) {
		t.Fatalf("leg %.3f", room.LinkM)
	}
}

func TestEstimatedScaleFramesIntoRoom(t *testing.T) {
	rig := newRig(t, 0)
	rig.warmBoth(10, 10)
	room := rig.room()
	if room.MapScale != scaleEstimated {
		t.Fatalf("scale %q", room.MapScale)
	}
	first := geometry.Point{X: room.ESPX, Z: room.ESPZ}
	second := geometry.Point{X: room.ESP2X, Z: room.ESP2Z}
	span := math.Max(geometry.Distance(geometry.Point{}, first), math.Max(geometry.Distance(geometry.Point{}, second), geometry.Distance(first, second)))
	if !near(span, 7.5, 0.01) {
		t.Fatalf("span %.3f", span)
	}
}

func assertFrameOfReference(t *testing.T, room Room) {
	t.Helper()
	if room.TxX != 0 || room.TxZ != 0 || !room.TxOK {
		t.Fatalf("transmitter %+v", room)
	}
	if room.ESPX >= 0 || room.ESP2X <= 0 || room.ESPZ >= 0 || room.ESP2Z >= 0 {
		t.Fatalf("frame of reference broken: %+v", room)
	}
}

func TestFrameOfReferenceInEveryMode(t *testing.T) {
	defaultAngle := newRig(t, 0)
	defaultAngle.warmBoth(3, 4)
	assertFrameOfReference(t, defaultAngle.room())

	triangle := newRig(t, 0)
	warmWithPeer(triangle, 3, 4, 2.5)
	triangle.ticks(layoutCommitPersist)
	assertFrameOfReference(t, triangle.room())

	inconsistent := newRig(t, 0)
	warmWithPeer(inconsistent, 1, 1, 8)
	inconsistent.ticks(layoutCommitPersist)
	assertFrameOfReference(t, inconsistent.room())
}

func TestOnlyApRssiFeedsTheMap(t *testing.T) {
	rig := newRig(t, 0)
	for i := 0; i < 40; i++ {
		rig.layout.Observe(CSIPacket{NodeID: nodeOne})
		rig.layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: "ffeeddccbbaa", RSSI: -50})
		rig.layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macRouter, RSSI: -50})
	}
	rig.tick()
	if rig.layout.nodes[0].filter.total != 0 || rig.layout.peer.total != 0 || rig.room().ESP1OK {
		t.Fatal("non-ap samples must not count")
	}
}

func TestPeerSampleBeforeOtherNodeAnnouncesIsDropped(t *testing.T) {
	clock := &testClock{current: time.Unix(1_700_000_000, 0)}
	layout := NewLayoutWith(6, 4.5, 0, clock.now, func(Room) {})
	layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macTwo, RSSI: -50})
	if layout.peer.total != 0 {
		t.Fatal("peer sample before the other node announced its mac must be dropped")
	}
	layout.ObserveLink(LinkSample{NodeID: nodeTwo, SelfMAC: macTwo, SrcMAC: macRouter, RSSI: -50})
	layout.ObserveLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macTwo, RSSI: -50})
	if layout.peer.total != 1 {
		t.Fatalf("peer total %d", layout.peer.total)
	}
}

func TestIngestLinkStaysOutOfPipelines(t *testing.T) {
	layout := NewLayoutWith(6, 4.5, 0, time.Now, func(Room) {})
	processor := NewProcessor(nil, nil, layout)
	processor.IngestLink(LinkSample{NodeID: nodeOne, SelfMAC: macOne, SrcMAC: macTwo, RSSI: -60})
	if len(processor.in) != 0 {
		t.Fatal("link sample reached the packet channel")
	}
	if len(processor.syncer.SnapshotNode(nodeOne, 10)) != 0 {
		t.Fatal("link sample reached the syncer")
	}
}

func TestEvaluateIsDecoupledFromObservation(t *testing.T) {
	rig := newRig(t, 0)
	published := len(rig.rooms)
	rig.feedAP(nodeOne, rssiForMeters(3), 100)
	rig.setLive(true, true)
	if len(rig.rooms) != published {
		t.Fatal("observation or live update published a room")
	}
}
