package image

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

func TestTonemapExtremes(t *testing.T) {
	p := TonemapParams{Exposure: 1, Gamma: 2.2, Operator: "reinhard"}
	cases := []struct {
		in   float64
		want byte
	}{
		{math.NaN(), 0},
		{math.Inf(1), 255},
		{math.Inf(-1), 0},
		{-5, 0},
		{0, 0},
		{1e300, 255}, // huge but finite saturates to white
	}
	for _, c := range cases {
		h := NewHDR(1, 1)
		h.Samples = 1
		h.Sum[0] = geom.V(c.in, c.in, c.in)
		rgba := h.ToRGBA(p)
		if rgba[0] != c.want {
			t.Errorf("map(%v) = %d, want %d", c.in, rgba[0], c.want)
		}
	}
}

func TestTonemapOperators(t *testing.T) {
	for _, op := range []string{"reinhard", "clamp", "aces", "bogus"} {
		h := NewHDR(1, 1)
		h.Samples = 1
		h.Sum[0] = geom.V(0.5, 2.0, 0.0)
		rgba := h.ToRGBA(TonemapParams{Operator: op})
		for k := 0; k < 3; k++ {
			v := rgba[k]
			if v > 255 { // impossible for byte, sanity only
				t.Fatalf("op %s ch %d out of range", op, k)
			}
		}
	}
	// Exposure brightens.
	h := NewHDR(1, 1)
	h.Samples = 1
	h.Sum[0] = geom.V(0.25, 0.25, 0.25)
	lo := h.ToRGBA(TonemapParams{Exposure: 1})[0]
	hi := h.ToRGBA(TonemapParams{Exposure: 4})[0]
	if hi <= lo {
		t.Fatalf("exposure did not brighten: %d vs %d", lo, hi)
	}
}

func TestWritePNGAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.png")
	rgba := make([]byte, 4*4*4)
	for i := range rgba {
		rgba[i] = byte(i)
	}
	if err := WritePNGAtomic(path, 4, 4, rgba); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("output missing")
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "out.png" {
			t.Fatalf("unexpected leftover file %s", e.Name())
		}
	}
	// Overwrite works (rename replaces).
	if err := WritePNGAtomic(path, 4, 4, rgba); err != nil {
		t.Fatal(err)
	}
}
