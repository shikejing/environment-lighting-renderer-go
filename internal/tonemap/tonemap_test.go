package tonemap

import (
	"math"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

var ops = []Operator{OpClamp, OpReinhard, OpACES}

// TestExtremeValues: NaN, +/-Inf, negatives and huge values must map
// deterministically into [0,1] for every operator.
func TestExtremeValues(t *testing.T) {
	extremes := []geom.Vec3{
		{X: math.NaN(), Y: math.NaN(), Z: math.NaN()},
		{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)},
		{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)},
		{X: -5, Y: -1e-9, Z: -math.MaxFloat64},
		{X: math.MaxFloat64, Y: 1e308, Z: 1e300},
		{X: 1e6, Y: 1e-6, Z: 0},
		{X: math.Inf(1), Y: math.NaN(), Z: -1},
	}
	for _, op := range ops {
		cfg := Config{Exposure: 1, Gamma: 2.2, Op: op}
		for i, c := range extremes {
			m1 := Map(c, cfg)
			m2 := Map(c, cfg)
			if m1 != m2 {
				t.Fatalf("op=%v case %d: non-deterministic: %v vs %v", op, i, m1, m2)
			}
			for _, v := range []float64{m1.X, m1.Y, m1.Z} {
				if math.IsNaN(v) || v < 0 || v > 1 {
					t.Fatalf("op=%v case %d: mapped value %v out of [0,1]", op, i, v)
				}
			}
		}
	}
}

// TestExtremeValuePolicy documents and pins the exact behavior.
func TestExtremeValuePolicy(t *testing.T) {
	cfg := Config{Exposure: 1, Gamma: 1, Op: OpClamp}
	cases := []struct {
		in   float64
		want float64
	}{
		{math.NaN(), 0},
		{math.Inf(-1), 0},
		{-3, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{2, 1}, // clamped
		{math.Inf(1), 1},
	}
	for _, c := range cases {
		got := Map(geom.Vec3{X: c.in, Y: c.in, Z: c.in}, cfg).X
		if got != c.want {
			t.Errorf("Map(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestMonotonicity: tone curves must be monotonically non-decreasing.
func TestMonotonicity(t *testing.T) {
	for _, op := range ops {
		cfg := Config{Exposure: 1, Gamma: 2.2, Op: op}
		prev := 0.0
		for i := 0; i <= 1000; i++ {
			x := math.Pow(10, -3+9*float64(i)/1000) // 1e-3 .. 1e6
			v := Map(geom.Vec3{X: x, Y: x, Z: x}, cfg).X
			if v < prev {
				t.Fatalf("op=%v: not monotonic at %v: %v < %v", op, x, v, prev)
			}
			prev = v
		}
	}
}

// TestExposureAndGammaConfigurable.
func TestExposureAndGammaConfigurable(t *testing.T) {
	c := geom.Vec3{X: 0.25, Y: 0.25, Z: 0.25}
	a := Map(c, Config{Exposure: 1, Gamma: 1, Op: OpClamp})
	b := Map(c, Config{Exposure: 2, Gamma: 1, Op: OpClamp})
	if b.X <= a.X {
		t.Fatal("higher exposure must brighten")
	}
	g := Map(c, Config{Exposure: 1, Gamma: 2.2, Op: OpClamp})
	if g.X <= a.X {
		t.Fatal("gamma 2.2 must lift mid-tones vs gamma 1")
	}
}
