package geometry

import "math"

type Point struct {
	X float64
	Z float64
}

func Distance(a, b Point) float64 {
	return math.Hypot(a.X-b.X, a.Z-b.Z)
}

func (p Point) Scale(factor float64) Point {
	return Point{X: p.X * factor, Z: p.Z * factor}
}

type PathLoss struct {
	RefDBm   float64
	Exponent float64
}

func DefaultPathLoss() PathLoss {
	return PathLoss{RefDBm: -40, Exponent: 2.5}
}

func (p PathLoss) Distance(rssiDBm float64) float64 {
	return math.Pow(10, (p.RefDBm-rssiDBm)/(10*p.Exponent))
}

func PlaceByAngle(leg, angle float64) Point {
	return Point{X: leg * math.Sin(angle), Z: -leg * math.Cos(angle)}
}

func ApexAngle(legA, legB, base float64) (float64, bool) {
	if legA <= 0 || legB <= 0 {
		return 0, false
	}
	cosine := (legA*legA + legB*legB - base*base) / (2 * legA * legB)
	consistent := cosine >= -1 && cosine <= 1
	cosine = math.Max(-1, math.Min(1, cosine))
	return math.Acos(cosine), consistent
}

func PlaceTriangle(legA, legB, base float64) (Point, Point, bool) {
	apex, consistent := ApexAngle(legA, legB, base)
	return PlaceByAngle(legA, -apex/2), PlaceByAngle(legB, apex/2), consistent
}

func FrameFactor(maxSpan, roomW, roomD float64) float64 {
	diagonal := math.Hypot(roomW, roomD)
	if maxSpan <= 0 || diagonal <= 0 || maxSpan <= diagonal {
		return 1
	}
	return diagonal / maxSpan
}
