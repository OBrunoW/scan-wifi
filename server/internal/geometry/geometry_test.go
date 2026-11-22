package geometry

import (
	"math"
	"testing"
)

const tolerance = 1e-9

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestPathLossInvertsLogDistanceModel(t *testing.T) {
	model := DefaultPathLoss()
	for _, meters := range []float64{0.5, 1, 2, 3.7, 8} {
		rssi := model.RefDBm - 10*model.Exponent*math.Log10(meters)
		if got := model.Distance(rssi); !near(got, meters, 1e-9) {
			t.Fatalf("distance for %.1f m: got %.6f", meters, got)
		}
	}
}

func TestPathLossRatioIsIndependentOfReference(t *testing.T) {
	low := PathLoss{RefDBm: -35, Exponent: 2.5}
	high := PathLoss{RefDBm: -48, Exponent: 2.5}
	ratioLow := low.Distance(-60) / low.Distance(-52)
	ratioHigh := high.Distance(-60) / high.Distance(-52)
	if !near(ratioLow, ratioHigh, tolerance) {
		t.Fatalf("ratio depends on reference: %f vs %f", ratioLow, ratioHigh)
	}
}

func TestPlaceTriangleRecoversSyntheticLayout(t *testing.T) {
	transmitter := Point{}
	nodeA := Point{X: -1.2, Z: -2.5}
	nodeB := Point{X: 1.8, Z: -2.0}
	legA := Distance(transmitter, nodeA)
	legB := Distance(transmitter, nodeB)
	base := Distance(nodeA, nodeB)

	placedA, placedB, consistent := PlaceTriangle(legA, legB, base)
	if !consistent {
		t.Fatal("expected consistent triangle")
	}
	if !near(Distance(transmitter, placedA), legA, 1e-9) {
		t.Fatalf("leg A mismatch")
	}
	if !near(Distance(transmitter, placedB), legB, 1e-9) {
		t.Fatalf("leg B mismatch")
	}
	if !near(Distance(placedA, placedB), base, 1e-9) {
		t.Fatalf("base mismatch: got %f want %f", Distance(placedA, placedB), base)
	}
	if placedA.X >= 0 || placedB.X <= 0 {
		t.Fatalf("expected A left and B right of the bisector: %+v %+v", placedA, placedB)
	}
}

func TestPlaceTriangleClampsInconsistentDistances(t *testing.T) {
	placedA, placedB, consistent := PlaceTriangle(1, 1, 5)
	if consistent {
		t.Fatal("expected inconsistent triangle")
	}
	if !near(Distance(placedA, placedB), 2, 1e-9) {
		t.Fatalf("expected collinear clamp with base 2, got %f", Distance(placedA, placedB))
	}
}

func TestApexAngleRejectsZeroLegs(t *testing.T) {
	if _, ok := ApexAngle(0, 1, 1); ok {
		t.Fatal("expected rejection")
	}
}

func TestFrameFactorShrinksOnlyWhenSpanExceedsDiagonal(t *testing.T) {
	if got := FrameFactor(4, 6, 4.5); got != 1 {
		t.Fatalf("span inside room should not scale, got %f", got)
	}
	got := FrameFactor(15, 6, 4.5)
	if !near(got, 7.5/15, tolerance) {
		t.Fatalf("expected 0.5, got %f", got)
	}
	if got := FrameFactor(15, 0, 0); got != 1 {
		t.Fatalf("unknown room should not scale, got %f", got)
	}
}

func TestPlaceByAngleUsesNegativeZForward(t *testing.T) {
	p := PlaceByAngle(2, 0)
	if !near(p.X, 0, tolerance) || !near(p.Z, -2, tolerance) {
		t.Fatalf("got %+v", p)
	}
}
