package envmap

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

func TestPFMRoundTrip(t *testing.T) {
	m := GenerateSky(64, 32, geom.V(0, 1, 0), 100)
	path := filepath.Join(t.TempDir(), "sky.pfm")
	if err := m.SavePFM(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPFM(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != m.Width || got.Height != m.Height {
		t.Fatalf("dims %dx%d", got.Width, got.Height)
	}
	for i, p := range m.Pixels {
		d := p.Sub(got.Pixels[i]).Len()
		if d > 1e-5 {
			t.Fatalf("pixel %d differs by %v", i, d)
		}
	}
}

func TestSampleSun(t *testing.T) {
	sunDir := geom.V(0, 0.3, -1)
	m := GenerateSky(256, 128, sunDir, 50)
	c := m.Sample(sunDir.Normalize())
	if c.X < 40 {
		t.Fatalf("sun direction radiance too low: %v", c)
	}
	away := m.Sample(sunDir.Neg().Normalize())
	if away.X > 5 {
		t.Fatalf("anti-sun radiance too high: %v", away)
	}
	// Zenith / horizon continuity: sampling is finite everywhere.
	for i := 0; i < 1000; i++ {
		a := float64(i) / 1000 * 2 * math.Pi
		d := geom.V(math.Cos(a), math.Sin(a*3), math.Sin(a)).Normalize()
		s := m.Sample(d)
		if math.IsNaN(s.X) || math.IsInf(s.X, 0) {
			t.Fatalf("non-finite sample at dir %v", d)
		}
	}
}
