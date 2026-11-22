package geometry

import (
	"math/rand"
	"testing"
	"time"
)

func TestEMAConvergesToConstantSignal(t *testing.T) {
	filter := NewEMA(0.1)
	for i := 0; i < 200; i++ {
		filter.Add(-55)
	}
	if !near(filter.Value(), -55, 1e-9) || filter.Samples() != 200 {
		t.Fatalf("got %f after %d", filter.Value(), filter.Samples())
	}
}

func TestStablePointIgnoresJitterBelowThreshold(t *testing.T) {
	hold := NewStablePoint(8)
	origin := Point{X: 1, Z: -2}
	hold.Offer(origin, 0.25)
	noise := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		jitter := Point{X: origin.X + (noise.Float64()-0.5)*0.3, Z: origin.Z + (noise.Float64()-0.5)*0.3}
		if _, changed := hold.Offer(jitter, 0.25); changed {
			t.Fatalf("published jitter at step %d", i)
		}
	}
}

func TestStablePointIgnoresShortSpike(t *testing.T) {
	hold := NewStablePoint(8)
	origin := Point{X: 0, Z: -1}
	hold.Offer(origin, 0.25)
	for i := 0; i < 7; i++ {
		hold.Offer(Point{X: 2, Z: -1}, 0.25)
	}
	hold.Offer(origin, 0.25)
	for i := 0; i < 7; i++ {
		if _, changed := hold.Offer(Point{X: 2, Z: -1}, 0.25); changed {
			t.Fatal("spike interrupted by a stable sample must restart persistence")
		}
	}
	if got, _ := hold.Published(); got != origin {
		t.Fatalf("expected origin, got %+v", got)
	}
}

func TestStablePointFollowsPersistentMove(t *testing.T) {
	hold := NewStablePoint(8)
	hold.Offer(Point{X: 0, Z: -1}, 0.25)
	moved := Point{X: 1, Z: -1}
	changedAt := -1
	for i := 0; i < 8; i++ {
		if _, changed := hold.Offer(moved, 0.25); changed {
			changedAt = i
		}
	}
	if changedAt != 7 {
		t.Fatalf("expected change on 8th sample, got %d", changedAt)
	}
	if got, _ := hold.Published(); got != moved {
		t.Fatalf("got %+v", got)
	}
}

func TestStablePointUsesThresholdOfEachOffer(t *testing.T) {
	hold := NewStablePoint(2)
	hold.Offer(Point{X: 0, Z: -1}, 0.25)
	moved := Point{X: 0.4, Z: -1}
	hold.Offer(moved, 0.5)
	if _, changed := hold.Offer(moved, 0.5); changed {
		t.Fatal("offer below the per-call threshold must not commit")
	}
	hold.Offer(moved, 0.25)
	if _, changed := hold.Offer(moved, 0.25); !changed {
		t.Fatal("offer above the per-call threshold must commit after persistence")
	}
}

func TestMedianWindowRejectsOutliers(t *testing.T) {
	start := time.Unix(1000, 0)
	window := NewMedianWindow(5 * time.Second)
	for i := 0; i < 30; i++ {
		window.Add(start, -52)
	}
	for i := 0; i < 5; i++ {
		window.Add(start, -20)
	}
	median, ok := window.Median(start)
	if !ok || median != -52 {
		t.Fatalf("got %f %v", median, ok)
	}
}

func TestMedianWindowDropsExpiredSamples(t *testing.T) {
	start := time.Unix(1000, 0)
	window := NewMedianWindow(5 * time.Second)
	window.Add(start, -40)
	window.Add(start.Add(4*time.Second), -60)
	median, _ := window.Median(start.Add(6 * time.Second))
	if median != -60 {
		t.Fatalf("got %f", median)
	}
	if _, ok := window.Median(start.Add(20 * time.Second)); ok {
		t.Fatal("window must be empty")
	}
}

func TestMedianWindowAveragesMiddlePairOnEvenCount(t *testing.T) {
	start := time.Unix(1000, 0)
	window := NewMedianWindow(5 * time.Second)
	window.Add(start, -50)
	window.Add(start, -60)
	if median, _ := window.Median(start); median != -55 {
		t.Fatalf("got %f", median)
	}
}
