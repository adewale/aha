package corpus

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

func TestSpread(t *testing.T) {
	cases := []struct {
		sessions, projects int
		want               float64
	}{
		{1, 1, 1},
		{3, 1, 3},
		{1, 3, 2},
		{3, 2, 3.5},
		{0, 0, 0}, // degenerate: 1 + (-1) + 0.5*(-1) = -0.5 -> clamped to 0
	}
	for _, c := range cases {
		got := spread(c.sessions, c.projects)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("spread(%d,%d) = %v, want %v", c.sessions, c.projects, got, c.want)
		}
	}
}

func TestWilsonLowerBoundTrialsZero(t *testing.T) {
	if got := wilsonLowerBound(0, 0, 1.96); got != 0 {
		t.Errorf("wilsonLowerBound(0,0,1.96) = %v, want 0", got)
	}
	if got := wilsonLowerBound(0, -5, 1.96); got != 0 {
		t.Errorf("wilsonLowerBound with negative trials = %v, want 0", got)
	}
}

func TestWilsonLowerBoundMonotonicSupport(t *testing.T) {
	// At a fixed point estimate of p=1.0, more trials => higher (less penalized)
	// lower bound, all still strictly below 1.
	w1 := wilsonLowerBound(1, 1, 1.96)
	w10 := wilsonLowerBound(10, 10, 1.96)
	w100 := wilsonLowerBound(100, 100, 1.96)
	if !(w1 < w10 && w10 < w100) {
		t.Errorf("expected w1 < w10 < w100, got %v, %v, %v", w1, w10, w100)
	}
	for _, w := range []float64{w1, w10, w100} {
		if w < 0 || w >= 1 {
			t.Errorf("wilson bound %v not in [0,1)", w)
		}
	}
	// Same point estimate p=1.0, strictly more trials => strictly higher bound.
	if !(wilsonLowerBound(5, 5, 1.96) < wilsonLowerBound(50, 50, 1.96)) {
		t.Errorf("expected wilson(5,5) < wilson(50,50)")
	}
}

func TestWilsonLowerBoundNumeric(t *testing.T) {
	// Hand-computed (see Python verification): successes=8, trials=10, z=1.96.
	const want = 0.49015684672072346
	got := wilsonLowerBound(8, 10, 1.96)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("wilsonLowerBound(8,10,1.96) = %v, want %v", got, want)
	}
}

func TestPathScore(t *testing.T) {
	// Composition: confidence * spread(sessions, projects).
	if got := pathScore(0.5, 3, 1); math.Abs(got-1.5) > 1e-9 {
		t.Errorf("pathScore(0.5,3,1) = %v, want 1.5", got)
	}
	if got := pathScore(0.8, 1, 1); math.Abs(got-0.8) > 1e-9 {
		t.Errorf("pathScore(0.8,1,1) = %v, want 0.8", got)
	}
}

func TestWilsonLowerBoundProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 10_000).Draw(rt, "trials")
		s := rapid.IntRange(0, n).Draw(rt, "successes")
		w := wilsonLowerBound(s, n, 1.96)
		if w < 0 || w > 1 {
			rt.Fatalf("wilson(%d,%d) = %v not in [0,1]", s, n, w)
		}
		// The lower bound never exceeds the point estimate.
		phat := float64(s) / float64(n)
		if w > phat+1e-12 {
			rt.Fatalf("wilson(%d,%d) = %v exceeds point estimate %v", s, n, w, phat)
		}
	})
}
