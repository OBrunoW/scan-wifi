package geometry

import (
	"sort"
	"time"
)

type EMA struct {
	alpha   float64
	value   float64
	samples int
}

func NewEMA(alpha float64) EMA {
	return EMA{alpha: alpha}
}

func (e *EMA) Add(sample float64) {
	if e.samples == 0 {
		e.value = sample
	} else {
		e.value = e.alpha*sample + (1-e.alpha)*e.value
	}
	e.samples++
}

func (e EMA) Value() float64 { return e.value }

func (e EMA) Samples() int { return e.samples }

type timedSample struct {
	at    time.Time
	value float64
}

type MedianWindow struct {
	span    time.Duration
	samples []timedSample
}

func NewMedianWindow(span time.Duration) *MedianWindow {
	return &MedianWindow{span: span}
}

func (w *MedianWindow) Add(at time.Time, value float64) {
	w.samples = append(w.samples, timedSample{at: at, value: value})
	w.prune(at)
}

func (w *MedianWindow) Median(now time.Time) (float64, bool) {
	w.prune(now)
	if len(w.samples) == 0 {
		return 0, false
	}
	values := make([]float64, len(w.samples))
	for i, sample := range w.samples {
		values[i] = sample.value
	}
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle], true
	}
	return (values[middle-1] + values[middle]) / 2, true
}

func (w *MedianWindow) prune(now time.Time) {
	cutoff := now.Add(-w.span)
	keep := 0
	for keep < len(w.samples) && w.samples[keep].at.Before(cutoff) {
		keep++
	}
	if keep > 0 {
		w.samples = append(w.samples[:0], w.samples[keep:]...)
	}
}

type StablePoint struct {
	persist   int
	published Point
	has       bool
	pending   int
}

func NewStablePoint(persist int) *StablePoint {
	if persist < 1 {
		persist = 1
	}
	return &StablePoint{persist: persist}
}

func (s *StablePoint) Offer(candidate Point, threshold float64) (Point, bool) {
	if !s.has {
		s.published = candidate
		s.has = true
		return s.published, true
	}
	if Distance(candidate, s.published) < threshold {
		s.pending = 0
		return s.published, false
	}
	s.pending++
	if s.pending < s.persist {
		return s.published, false
	}
	s.published = candidate
	s.pending = 0
	return s.published, true
}

func (s *StablePoint) Published() (Point, bool) {
	return s.published, s.has
}
