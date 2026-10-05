// Package envmap provides HDR environment maps: procedural generation,
// Radiance .hdr (RGBE) I/O, and deterministic direction sampling.
package envmap

import (
	"fmt"
	"math"
	"os"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
	"github.com/shikejing/environment-lighting-renderer-go/internal/imageio"
)

// Map is an equirectangular HDR environment map (row 0 = top, +Y up).
type Map struct {
	Width, Height int
	Pixels        []geom.Vec3 // len = Width*Height, row-major
}

func New(w, h int) *Map { return &Map{Width: w, Height: h, Pixels: make([]geom.Vec3, w*h)} }

// Sample returns the radiance for a world-space direction (bilinear,
// horizontal wrap, vertical clamp). Deterministic.
func (m *Map) Sample(dir geom.Vec3) geom.Vec3 {
	d := geom.Norm(dir)
	u := 0.5 + math.Atan2(d.Z, d.X)/(2*math.Pi)
	v := 0.5 - math.Asin(clamp(d.Y, -1, 1))/math.Pi
	x := u*float64(m.Width) - 0.5
	y := v*float64(m.Height) - 0.5
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	wrap := func(i, n int) int { return ((i % n) + n) % n }
	clampi := func(i, n int) int {
		if i < 0 {
			return 0
		}
		if i > n-1 {
			return n - 1
		}
		return i
	}
	c00 := m.Pixels[clampi(y0, m.Height)*m.Width+wrap(x0, m.Width)]
	c10 := m.Pixels[clampi(y0, m.Height)*m.Width+wrap(x0+1, m.Width)]
	c01 := m.Pixels[clampi(y0+1, m.Height)*m.Width+wrap(x0, m.Width)]
	c11 := m.Pixels[clampi(y0+1, m.Height)*m.Width+wrap(x0+1, m.Width)]
	top := lerp(c00, c10, fx)
	bot := lerp(c01, c11, fx)
	return lerp(top, bot, fy)
}

func lerp(a, b geom.Vec3, t float64) geom.Vec3 {
	return geom.Add(geom.Scale(a, 1-t), geom.Scale(b, t))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// GenerateSky builds a deterministic procedural sky: gradient + sun disk +
// a few bright bands. sunIntensity may be extreme (e.g. 1e6) to exercise
// HDR tone mapping.
func GenerateSky(w, h int, sunIntensity float64) *Map {
	m := New(w, h)
	sunDir := geom.Norm(geom.Vec3{X: 0.5, Y: 0.6, Z: 0.3})
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u := (float64(x) + 0.5) / float64(w)
			v := (float64(y) + 0.5) / float64(h)
			theta := v * math.Pi
			phi := (u - 0.5) * 2 * math.Pi
			dir := geom.Vec3{
				X: math.Sin(theta) * math.Cos(phi),
				Y: math.Cos(theta),
				Z: math.Sin(theta) * math.Sin(phi),
			}
			// Vertical gradient: deep blue up, warm horizon.
			t := clamp(dir.Y*0.5+0.5, 0, 1)
			c := lerp(geom.Vec3{X: 0.6, Y: 0.5, Z: 0.4}, geom.Vec3{X: 0.1, Y: 0.2, Z: 0.6}, t)
			// Sun disk with soft edge.
			cosA := geom.Dot(dir, sunDir)
			if cosA > 0.999 {
				c = geom.Add(c, geom.Scale(geom.Vec3{X: 1, Y: 0.95, Z: 0.85}, sunIntensity))
			} else if cosA > 0.995 {
				f := (cosA - 0.995) / 0.004
				c = geom.Add(c, geom.Scale(geom.Vec3{X: 1, Y: 0.9, Z: 0.7}, f*sunIntensity*0.05))
			}
			m.Pixels[y*w+x] = c
		}
	}
	return m
}

// ---- Radiance RGBE (.hdr) I/O, flat (non-RLE) scanlines only. ----

// SaveRGBE writes the map in Radiance .hdr format (flat scanlines).
func SaveRGBE(path string, m *Map) error {
	buf := make([]byte, 0, 128+m.Width*m.Height*4)
	buf = append(buf, []byte("#?RADIANCE\n# environment-lighting-renderer-go\nFORMAT=32-bit_rle_rgbe\n\n")...)
	buf = append(buf, []byte(fmt.Sprintf("-Y %d +X %d\n", m.Height, m.Width))...)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			buf = appendRGBE(buf, m.Pixels[y*m.Width+x])
		}
	}
	return imageio.WriteFileAtomic(path, buf)
}

func appendRGBE(buf []byte, c geom.Vec3) []byte {
	v := math.Max(c.X, math.Max(c.Y, c.Z))
	if v < 1e-32 || math.IsNaN(v) {
		return append(buf, 0, 0, 0, 0)
	}
	mant, exp := math.Frexp(v) // v = mant * 2^exp, mant in [0.5,1)
	scale := mant * 256.0 / v
	return append(buf,
		byte(clamp(c.X*scale, 0, 255)),
		byte(clamp(c.Y*scale, 0, 255)),
		byte(clamp(c.Z*scale, 0, 255)),
		byte(exp+128),
	)
}

// LoadRGBE reads a Radiance .hdr file (flat or RLE scanlines).
func LoadRGBE(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pos := 0
	readLine := func() (string, error) {
		for i := pos; i < len(data); i++ {
			if data[i] == '\n' {
				line := string(data[pos:i])
				pos = i + 1
				return line, nil
			}
		}
		return "", fmt.Errorf("unexpected EOF in header")
	}
	line, err := readLine()
	if err != nil {
		return nil, err
	}
	if line != "#?RADIANCE" && line != "#?RGBE" {
		return nil, fmt.Errorf("not a radiance hdr file: %q", line)
	}
	for {
		line, err = readLine()
		if err != nil {
			return nil, err
		}
		if line == "" {
			break
		}
	}
	line, err = readLine()
	if err != nil {
		return nil, err
	}
	var h, w int
	if _, err := fmt.Sscanf(line, "-Y %d +X %d", &h, &w); err != nil {
		return nil, fmt.Errorf("bad resolution line %q: %w", line, err)
	}
	if w <= 0 || h <= 0 || w > 1<<15 || h > 1<<15 {
		return nil, fmt.Errorf("bad dimensions %dx%d", w, h)
	}
	m := New(w, h)
	scan := make([][4]byte, w)
	for y := 0; y < h; y++ {
		if err := readScanline(data, &pos, scan, w); err != nil {
			return nil, fmt.Errorf("scanline %d: %w", y, err)
		}
		for x := 0; x < w; x++ {
			m.Pixels[y*w+x] = rgbeToVec(scan[x])
		}
	}
	return m, nil
}

func readScanline(data []byte, pos *int, scan [][4]byte, w int) error {
	p := *pos
	if p+4 > len(data) {
		return fmt.Errorf("EOF")
	}
	// New-style RLE marker: 2,2, width hi, width lo, width in [8,32767].
	if data[p] == 2 && data[p+1] == 2 && (data[p+2]&0x80) == 0 {
		rw := int(data[p+2])<<8 | int(data[p+3])
		if rw != w {
			return fmt.Errorf("RLE width %d != %d", rw, w)
		}
		p += 4
		for c := 0; c < 4; c++ {
			x := 0
			for x < w {
				if p+2 > len(data) {
					return fmt.Errorf("EOF in RLE")
				}
				n := int(data[p])
				p++
				if n > 128 { // run
					n -= 128
					v := data[p]
					p++
					for i := 0; i < n && x < w; i++ {
						scan[x][c] = v
						x++
					}
				} else { // literal
					for i := 0; i < n && x < w; i++ {
						scan[x][c] = data[p]
						p++
					}
				}
			}
		}
		*pos = p
		return nil
	}
	// Flat scanline.
	if p+4*w > len(data) {
		return fmt.Errorf("EOF in flat scanline")
	}
	for x := 0; x < w; x++ {
		copy(scan[x][:], data[p:p+4])
		p += 4
	}
	*pos = p
	return nil
}

func rgbeToVec(b [4]byte) geom.Vec3 {
	if b[3] == 0 {
		return geom.Vec3{}
	}
	f := math.Ldexp(1.0, int(b[3])-(128+8))
	// No +0.5 rounding offset: a zero byte must decode to exactly 0.
	return geom.Vec3{
		X: float64(b[0]) * f,
		Y: float64(b[1]) * f,
		Z: float64(b[2]) * f,
	}
}
