// Package render implements a deterministic, progressive, tile-based
// environment-lighting renderer.
//
// Sampling model: for each pixel and each sample round r, a per-(pixel,
// round) RNG is seeded by HashSeed(seed, x, y, r). Camera rays that miss
// the scene sample the environment map directly; rays that hit a surface
// estimate direct environment lighting with one cosine-weighted hemisphere
// direction (shadow ray against the BVH), multiplied by surface albedo.
// Because every sample's random numbers depend only on (seed, pixel,
// round), results are bit-identical for any thread count and across
// interruption/resume: accumulation adds rounds 0..N-1 in order per pixel.
package render

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/shikejing/environment-lighting-renderer-go/internal/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
	"github.com/shikejing/environment-lighting-renderer-go/internal/imageio"
	"github.com/shikejing/environment-lighting-renderer-go/internal/scene"
)

// Config controls a render.
type Config struct {
	Width, Height int
	Samples       int    // total sample rounds per pixel
	TileSize      int    // tile edge in pixels; default 32
	Seed          uint64 // master seed
	Threads       int    // worker count; 0 = NumCPU
	// CheckpointPath, if non-empty, is written atomically after every
	// completed round so the render can be resumed.
	CheckpointPath string
	// PreviewPath, if non-empty, gets an atomically written PNG after
	// every completed round (progressive tiled output).
	PreviewPath string
	// ToneMap is used for preview/final PNG output.
	ToneMap ToneMapper
	// OnRound, if set, is called after each completed round with the
	// 1-based number of completed rounds.
	OnRound func(done int)
}

// ToneMapper converts an averaged HDR pixel to 8-bit RGBA.
type ToneMapper interface {
	ToRGBA(c geom.Vec3) [4]uint8
}

// ToneMapFunc adapts a function to ToneMapper.
type ToneMapFunc func(c geom.Vec3) [4]uint8

func (f ToneMapFunc) ToRGBA(c geom.Vec3) [4]uint8 { return f(c) }

// Result is the outcome of a render run.
type Result struct {
	Accum       []geom.Vec3 // summed radiance per pixel
	RoundsDone  int         // completed sample rounds
	Completed   bool        // true iff all requested rounds finished
	Interrupted bool        // true if ctx was cancelled/timed out
}

// Average returns the mean radiance per pixel over completed rounds.
func (r *Result) Average() []geom.Vec3 {
	out := make([]geom.Vec3, len(r.Accum))
	if r.RoundsDone == 0 {
		return out
	}
	inv := 1 / float64(r.RoundsDone)
	for i, c := range r.Accum {
		out[i] = geom.Scale(c, inv)
	}
	return out
}

// Renderer renders a scene snapshot with an environment map.
type Renderer struct {
	cfg   Config
	snap  scene.Snapshot
	env   *envmap.Map
	cam   camera
	accum []geom.Vec3
	done  int
}

func New(cfg Config, snap scene.Snapshot, env *envmap.Map) (*Renderer, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("bad dimensions %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Samples <= 0 {
		return nil, fmt.Errorf("samples must be > 0")
	}
	if cfg.TileSize <= 0 {
		cfg.TileSize = 32
	}
	if cfg.Threads <= 0 {
		cfg.Threads = runtime.NumCPU()
	}
	r := &Renderer{
		cfg:   cfg,
		snap:  snap,
		env:   env,
		cam:   defaultCamera(cfg.Width, cfg.Height),
		accum: make([]geom.Vec3, cfg.Width*cfg.Height),
	}
	return r, nil
}

// RoundsDone reports how many rounds have completed so far.
func (r *Renderer) RoundsDone() int { return r.done }

// Run renders until all rounds complete or ctx is cancelled. It always
// returns the accumulated state; only fully completed rounds are included
// in RoundsDone and in any checkpoint.
func (r *Renderer) Run(ctx context.Context) (*Result, error) {
	for r.done < r.cfg.Samples {
		if err := ctx.Err(); err != nil {
			return r.result(false, true), nil
		}
		if err := r.runRound(ctx, r.done); err != nil {
			return r.result(false, true), nil
		}
		r.done++
		if r.cfg.CheckpointPath != "" {
			if err := r.SaveCheckpoint(r.cfg.CheckpointPath); err != nil {
				return r.result(false, false), fmt.Errorf("checkpoint: %w", err)
			}
		}
		if r.cfg.PreviewPath != "" && r.cfg.ToneMap != nil {
			if err := r.writePreview(); err != nil {
				return r.result(false, false), fmt.Errorf("preview: %w", err)
			}
		}
		if r.cfg.OnRound != nil {
			r.cfg.OnRound(r.done)
		}
	}
	return r.result(true, false), nil
}

func (r *Renderer) result(completed, interrupted bool) *Result {
	return &Result{Accum: r.accum, RoundsDone: r.done, Completed: completed, Interrupted: interrupted}
}

// runRound executes one full sample round, tile by tile, with workers
// pulling tiles from an atomic queue (work stealing for load balance).
func (r *Renderer) runRound(ctx context.Context, round int) error {
	ts := r.cfg.TileSize
	nx := (r.cfg.Width + ts - 1) / ts
	ny := (r.cfg.Height + ts - 1) / ts
	total := nx * ny
	var next atomic.Int64
	var cancelled atomic.Bool
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for {
			if cancelled.Load() || ctx.Err() != nil {
				return
			}
			i := int(next.Add(1) - 1)
			if i >= total {
				return
			}
			tx, ty := i%nx, i/nx
			r.renderTile(tx*ts, ty*ts, ts, round)
		}
	}
	n := r.cfg.Threads
	if n > total {
		n = total
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// renderTile shades one tile for the given round. Each pixel's sample uses
// its own RNG seeded by (seed, x, y, round) — independent of scheduling.
func (r *Renderer) renderTile(x0, y0, size, round int) {
	w, h := r.cfg.Width, r.cfg.Height
	for y := y0; y < y0+size && y < h; y++ {
		for x := x0; x < x0+size && x < w; x++ {
			rng := splitmix{state: scene.HashSeed(r.cfg.Seed, x, y, round)}
			r.accum[y*w+x] = geom.Add(r.accum[y*w+x], r.samplePixel(x, y, &rng))
		}
	}
}

// samplePixel computes one environment-lighting sample for a pixel.
func (r *Renderer) samplePixel(x, y int, rng *splitmix) geom.Vec3 {
	ray := r.cam.ray(x, y, rng.next(), rng.next())
	hit, ok := r.snap.Intersect(ray, 1e-4, math.Inf(1))
	if !ok {
		return r.env.Sample(ray.Dir)
	}
	s := r.snap.Sphere(hit.ID)
	p := ray.At(hit.T)
	// Cosine-weighted hemisphere sample around the normal.
	dir := cosineHemisphere(hit.Normal, rng.next(), rng.next())
	shadow := geom.Ray{Orig: geom.Add(p, geom.Scale(hit.Normal, 1e-4)), Dir: dir}
	if r.snap.Occluded(shadow, 1e-4, math.Inf(1)) {
		return geom.Vec3{}
	}
	// Cosine-weighted sampling: throughput = albedo * env(dir); the
	// cosine and pdf cancel.
	return geom.Mul(s.Albedo, r.env.Sample(dir))
}

// cosineHemisphere returns a cosine-weighted direction around n.
func cosineHemisphere(n geom.Vec3, u1, u2 float64) geom.Vec3 {
	// Build an orthonormal basis around n.
	var t geom.Vec3
	if math.Abs(n.Y) < 0.999 {
		t = geom.Norm(geom.Cross(n, geom.Vec3{X: 0, Y: 1, Z: 0}))
	} else {
		t = geom.Norm(geom.Cross(n, geom.Vec3{X: 1, Y: 0, Z: 0}))
	}
	b := geom.Cross(n, t)
	phi := 2 * math.Pi * u1
	r := math.Sqrt(u2)
	x := r * math.Cos(phi)
	y := r * math.Sin(phi)
	z := math.Sqrt(math.Max(0, 1-u2))
	return geom.Norm(geom.Add(geom.Add(geom.Scale(t, x), geom.Scale(b, y)), geom.Scale(n, z)))
}

// ---- checkpoint ----

const checkpointMagic = "ELRCKPT1"

// SaveCheckpoint atomically writes the accumulated state.
func (r *Renderer) SaveCheckpoint(path string) error {
	buf := make([]byte, 0, 8+5*8+len(r.accum)*24)
	buf = append(buf, checkpointMagic...)
	put := func(v uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		buf = append(buf, b[:]...)
	}
	put(uint64(r.cfg.Width))
	put(uint64(r.cfg.Height))
	put(uint64(r.cfg.Samples))
	put(r.cfg.Seed)
	put(uint64(r.done))
	for _, c := range r.accum {
		put(math.Float64bits(c.X))
		put(math.Float64bits(c.Y))
		put(math.Float64bits(c.Z))
	}
	return imageio.WriteFileAtomic(path, buf)
}

// ResumeRenderer loads a checkpoint and returns a renderer primed with the
// accumulated state, continuing at the next round. The checkpoint's
// dimensions, total samples and seed must match cfg exactly.
func ResumeRenderer(cfg Config, snap scene.Snapshot, env *envmap.Map, checkpointPath string) (*Renderer, error) {
	r, err := New(cfg, snap, env)
	if err != nil {
		return nil, err
	}
	data, err := readFile(checkpointPath)
	if err != nil {
		return nil, err
	}
	if len(data) < 8+5*8 || string(data[:8]) != checkpointMagic {
		return nil, fmt.Errorf("bad checkpoint magic")
	}
	pos := 8
	get := func() uint64 {
		v := binary.LittleEndian.Uint64(data[pos:])
		pos += 8
		return v
	}
	w, h, samples, seed, done := get(), get(), get(), get(), get()
	if int(w) != cfg.Width || int(h) != cfg.Height || int(samples) != cfg.Samples || seed != cfg.Seed {
		return nil, fmt.Errorf("checkpoint mismatch: %dx%d samples=%d seed=%d, want %dx%d samples=%d seed=%d",
			w, h, samples, seed, cfg.Width, cfg.Height, cfg.Samples, cfg.Seed)
	}
	if int(done) > cfg.Samples {
		return nil, fmt.Errorf("checkpoint has %d rounds > samples %d", done, cfg.Samples)
	}
	want := 8 + 5*8 + int(w)*int(h)*24
	if len(data) != want {
		return nil, fmt.Errorf("checkpoint size %d, want %d", len(data), want)
	}
	for i := range r.accum {
		r.accum[i] = geom.Vec3{
			X: math.Float64frombits(get()),
			Y: math.Float64frombits(get()),
			Z: math.Float64frombits(get()),
		}
	}
	r.done = int(done)
	return r, nil
}

func (r *Renderer) writePreview() error {
	avg := (&Result{Accum: r.accum, RoundsDone: r.done}).Average()
	pix := make([][4]uint8, len(avg))
	for i, c := range avg {
		pix[i] = r.cfg.ToneMap.ToRGBA(c)
	}
	return writePNG(r.cfg.PreviewPath, r.cfg.Width, r.cfg.Height, pix)
}

// splitmix is a deterministic per-sample PRNG.
type splitmix struct{ state uint64 }

func (s *splitmix) next() float64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	return float64(z>>11) * (1.0 / 9007199254740992.0)
}
