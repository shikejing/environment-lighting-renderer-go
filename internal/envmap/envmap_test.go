package envmap

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// TestRGBERoundTrip: save then load preserves values within RGBE precision.
func TestRGBERoundTrip(t *testing.T) {
	m := GenerateSky(64, 32, 1234.5)
	// Add some exact values including zero and tiny/huge.
	m.Pixels[0] = geom.Vec3{}
	m.Pixels[1] = geom.Vec3{X: 1e-20, Y: 2.5, Z: 1e6}
	path := filepath.Join(t.TempDir(), "sky.hdr")
	if err := SaveRGBE(path, m); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRGBE(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != m.Width || got.Height != m.Height {
		t.Fatalf("dims %dx%d, want %dx%d", got.Width, got.Height, m.Width, m.Height)
	}
	for i, want := range m.Pixels {
		g := got.Pixels[i]
		// RGBE shares one exponent per pixel: tolerance is relative to
		// the largest component.
		maxc := math.Max(want.X, math.Max(want.Y, want.Z))
		tol := 0.01*maxc + 1e-30
		for _, pair := range [][2]float64{{g.X, want.X}, {g.Y, want.Y}, {g.Z, want.Z}} {
			gv, wv := pair[0], pair[1]
			if math.Abs(gv-wv) > tol {
				t.Fatalf("pixel %d: got %v want %v (tol %v)", i, gv, wv, tol)
			}
		}
	}
}

// TestSampleDeterministic: sampling is pure and direction-dependent.
func TestSampleDeterministic(t *testing.T) {
	m := GenerateSky(32, 16, 100)
	d := geom.Vec3{X: 0.3, Y: 0.5, Z: -0.2}
	if m.Sample(d) != m.Sample(d) {
		t.Fatal("non-deterministic sample")
	}
	if m.Sample(geom.Vec3{X: 0, Y: 1, Z: 0}) == m.Sample(geom.Vec3{X: 0, Y: -1, Z: 0}) {
		t.Fatal("zenith and horizon should differ")
	}
}

// TestExtremeSun: extreme sun intensity must not produce NaN in sampling.
func TestExtremeSun(t *testing.T) {
	m := GenerateSky(32, 16, 1e30)
	for i := 0; i < 1000; i++ {
		y := -1 + 2*float64(i)/999
		c := m.Sample(geom.Vec3{X: 0.5, Y: y, Z: 0.3})
		for _, v := range []float64{c.X, c.Y, c.Z} {
			if math.IsNaN(v) {
				t.Fatalf("NaN at y=%v", y)
			}
		}
	}
}
