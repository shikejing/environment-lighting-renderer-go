// Package envmap loads and samples equirectangular HDR environment maps.
//
// Maps are stored in PFM (Portable Float Map) format, little-endian,
// color ("PF"). Pixel row 0 in the file is the BOTTOM row per the PFM
// convention; internally row 0 is the TOP (zenith, +Y) row.
package envmap

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// Map is an equirectangular HDR environment map.
type Map struct {
	Width, Height int
	Pixels        []geom.Vec3 // row-major, row 0 = top = +Y (zenith)
}

// LoadPFM reads a little-endian color PFM ("PF") file.
func LoadPFM(path string) (*Map, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var magic string
	if _, err := fmt.Fscanf(r, "%s\n", &magic); err != nil {
		return nil, fmt.Errorf("pfm: bad magic: %w", err)
	}
	if magic != "PF" {
		return nil, fmt.Errorf("pfm: unsupported magic %q (want PF)", magic)
	}
	var w, h int
	var scale float64
	if _, err := fmt.Fscanf(r, "%d %d\n", &w, &h); err != nil {
		return nil, fmt.Errorf("pfm: bad dimensions: %w", err)
	}
	if _, err := fmt.Fscanf(r, "%f\n", &scale); err != nil {
		return nil, fmt.Errorf("pfm: bad scale: %w", err)
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("pfm: invalid dimensions %dx%d", w, h)
	}
	if scale >= 0 {
		return nil, fmt.Errorf("pfm: only little-endian (negative scale) supported")
	}
	m := &Map{Width: w, Height: h, Pixels: make([]geom.Vec3, w*h)}
	buf := make([]byte, 4)
	readF := func() (float32, error) {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		return math.Float32frombits(binary.LittleEndian.Uint32(buf)), nil
	}
	// File rows are bottom-to-top.
	for row := h - 1; row >= 0; row-- {
		for col := 0; col < w; col++ {
			var c [3]float32
			for k := 0; k < 3; k++ {
				v, err := readF()
				if err != nil {
					return nil, fmt.Errorf("pfm: truncated pixel data: %w", err)
				}
				c[k] = v
			}
			m.Pixels[row*w+col] = geom.V(float64(c[0]), float64(c[1]), float64(c[2]))
		}
	}
	return m, nil
}

// SavePFM writes the map as a little-endian color PFM.
func (m *Map) SavePFM(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	if _, err := fmt.Fprintf(w, "PF\n%d %d\n-1.0\n", m.Width, m.Height); err != nil {
		return err
	}
	buf := make([]byte, 12)
	putF := func(v float32, off int) {
		binary.LittleEndian.PutUint32(buf[off:], math.Float32bits(v))
	}
	for row := m.Height - 1; row >= 0; row-- {
		for col := 0; col < m.Width; col++ {
			p := m.Pixels[row*m.Width+col]
			putF(float32(p.X), 0)
			putF(float32(p.Y), 4)
			putF(float32(p.Z), 8)
			if _, err := w.Write(buf); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

// Sample returns the radiance in the given (normalized) direction,
// using bilinear interpolation. u wraps, v clamps at the poles.
func (m *Map) Sample(dir geom.Vec3) geom.Vec3 {
	// Equirectangular mapping: phi around +Y from -Z, theta from +Y down.
	phi := math.Atan2(dir.X, -dir.Z)        // [-pi, pi]
	theta := math.Acos(clamp(dir.Y, -1, 1)) // [0, pi], 0 = zenith
	u := (phi + math.Pi) / (2 * math.Pi)    // [0, 1]
	v := theta / math.Pi                    // [0, 1]
	x := u*float64(m.Width) - 0.5
	y := v*float64(m.Height) - 0.5
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	wrap := func(i, n int) int {
		i %= n
		if i < 0 {
			i += n
		}
		return i
	}
	clampI := func(i, n int) int {
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	}
	c00 := m.Pixels[clampI(y0, m.Height)*m.Width+wrap(x0, m.Width)]
	c10 := m.Pixels[clampI(y0, m.Height)*m.Width+wrap(x0+1, m.Width)]
	c01 := m.Pixels[clampI(y0+1, m.Height)*m.Width+wrap(x0, m.Width)]
	c11 := m.Pixels[clampI(y0+1, m.Height)*m.Width+wrap(x0+1, m.Width)]
	top := c00.Scale(1 - fx).Add(c10.Scale(fx))
	bot := c01.Scale(1 - fx).Add(c11.Scale(fx))
	return top.Scale(1 - fy).Add(bot.Scale(fy))
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

// GenerateSky procedurally builds a deterministic sky map:
// a horizon-to-zenith gradient plus a sun disk with the given
// radiance in direction sunDir.
func GenerateSky(width, height int, sunDir geom.Vec3, sunRadiance float64) *Map {
	m := &Map{Width: width, Height: height, Pixels: make([]geom.Vec3, width*height)}
	sun := sunDir.Normalize()
	const cosSun = 0.9995 // ~1.8 degree disk
	zenith := geom.V(0.25, 0.45, 0.9)
	horizon := geom.V(0.9, 0.85, 0.75)
	ground := geom.V(0.30, 0.28, 0.26)
	for row := 0; row < height; row++ {
		v := (float64(row) + 0.5) / float64(height)
		theta := v * math.Pi
		y := math.Cos(theta)
		for col := 0; col < width; col++ {
			u := (float64(col) + 0.5) / float64(width)
			phi := u*2*math.Pi - math.Pi
			dir := geom.V(math.Sin(theta)*math.Sin(phi), y, -math.Sin(theta)*math.Cos(phi))
			var c geom.Vec3
			if y >= 0 {
				t := math.Pow(1-y, 1.4)
				c = zenith.Scale(1 - t).Add(horizon.Scale(t))
			} else {
				c = ground
			}
			if dir.Dot(sun) > cosSun {
				c = c.Add(geom.V(sunRadiance, sunRadiance*0.95, sunRadiance*0.85))
			}
			m.Pixels[row*width+col] = c
		}
	}
	return m
}
