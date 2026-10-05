// Package image provides HDR accumulation buffers, tone mapping and
// atomic image file output.
//
// Tone mapping of extreme values is fully deterministic:
//   - NaN (e.g. 0*Inf) is mapped to black.
//   - +Inf maps to white, -Inf to black.
//   - Negative channels clamp to 0 before mapping.
//   - "clamp" hard-clips at 1.0; "reinhard" compresses with x/(1+x);
//     "aces" uses the fitted ACES curve. All operators saturate to
//     white as input grows without bound.
package image

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// HDR is a float64 RGB accumulation buffer.
type HDR struct {
	Width, Height int
	Sum           []geom.Vec3 // accumulated radiance sums
	Samples       int         // accumulated sample rounds
}

func NewHDR(w, h int) *HDR { return &HDR{Width: w, Height: h, Sum: make([]geom.Vec3, w*h)} }

// Avg returns the mean radiance of pixel i (Sum/Samples).
func (h *HDR) Avg(i int) geom.Vec3 {
	if h.Samples <= 0 {
		return geom.Vec3{}
	}
	return h.Sum[i].Scale(1 / float64(h.Samples))
}

// TonemapParams controls HDR -> display conversion.
type TonemapParams struct {
	Exposure float64 // linear multiplier before mapping; <=0 treated as 1
	Gamma    float64 // display gamma; <=0 treated as 2.2
	Operator string  // "reinhard" | "clamp" | "aces"; unknown falls back to "reinhard"
}

func (p TonemapParams) withDefaults() TonemapParams {
	if p.Exposure <= 0 {
		p.Exposure = 1
	}
	if p.Gamma <= 0 {
		p.Gamma = 2.2
	}
	switch p.Operator {
	case "reinhard", "clamp", "aces":
	default:
		p.Operator = "reinhard"
	}
	return p
}

// mapChannel converts one linear HDR channel to [0,1] display value.
func mapChannel(v float64, p TonemapParams) float64 {
	switch {
	case math.IsNaN(v) || math.IsInf(v, -1):
		return 0
	case math.IsInf(v, 1):
		return 1
	}
	v *= p.Exposure
	if v < 0 {
		v = 0
	}
	var m float64
	switch p.Operator {
	case "clamp":
		m = v
	case "aces":
		// Stephen Hill fit of the ACES RRT/ODT, input scaled by 0.6.
		const a, b, c, d, e = 2.51, 0.03, 2.43, 0.59, 0.14
		x := v * 0.6
		m = (x*(a*x+b))/(x*(c*x+d)) + e
	default: // reinhard
		m = v / (1 + v)
	}
	if m < 0 {
		m = 0
	}
	if m > 1 {
		m = 1
	}
	return math.Pow(m, 1/p.Gamma)
}

// ToRGBA tone-maps the averaged HDR buffer into 8-bit RGBA scanlines
// (len = Width*Height*4).
func (h *HDR) ToRGBA(p TonemapParams) []byte {
	p = p.withDefaults()
	out := make([]byte, h.Width*h.Height*4)
	for i := range h.Sum {
		c := h.Avg(i)
		r := mapChannel(c.X, p)
		g := mapChannel(c.Y, p)
		b := mapChannel(c.Z, p)
		out[i*4+0] = byte(math.Round(r * 255))
		out[i*4+1] = byte(math.Round(g * 255))
		out[i*4+2] = byte(math.Round(b * 255))
		out[i*4+3] = 255
	}
	return out
}

// WritePNGAtomic writes the RGBA bytes as PNG atomically: the PNG is
// encoded to memory, written to a temp file in the same directory,
// fsynced, then renamed over path. An interrupted run therefore never
// leaves a half-written file at path.
func WritePNGAtomic(path string, w, h int, rgba []byte) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, rgba)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return WriteFileAtomic(path, buf.Bytes())
}

// WriteFileAtomic writes data to path via temp-file + rename in the
// same directory, so readers never observe a partially written file.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
