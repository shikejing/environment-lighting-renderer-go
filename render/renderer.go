// Package render implements a deterministic, progressive, tiled
// environment-lighting renderer.
//
// Reproducibility model: every (pixel, round) pair owns an independent
// RNG stream derived from (Seed, pixelIndex, round). Each pixel's
// samples are accumulated in round order, so the final image is
// bit-identical for any worker count, any scheduling, and any split
// of the work across cancelled/resumed runs.
package render

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"

	"github.com/shikejing/environment-lighting-renderer-go/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/geom"
	"github.com/shikejing/environment-lighting-renderer-go/image"
	"github.com/shikejing/environment-lighting-renderer-go/scene"
)

// Config controls a render. Results depend only on Config fields and
// scene/env inputs — never on worker count or scheduling.
type Config struct {
	Width, Height  int
	Samples        int    // total sample rounds per pixel
	LightSamples   int    // env light samples per round (default 1)
	Seed           uint64 // master RNG seed
	TileSize       int    // tile edge in pixels (default 16)
	Workers        int    // parallel workers; does NOT affect output
	CamPos         geom.Vec3
	CamLookAt      geom.Vec3
	FovDeg         float64
	CheckpointPath string // if non-empty, progress is saved here after each round
	Resume         bool   // continue from CheckpointPath if it exists
	PreviewPath    string // if non-empty, a tone-mapped preview is written after each round
	Exposure       float64
	TonemapOp      string
}

func (c *Config) defaults() {
	if c.LightSamples <= 0 {
		c.LightSamples = 1
	}
	if c.TileSize <= 0 {
		c.TileSize = 16
	}
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.FovDeg <= 0 {
		c.FovDeg = 50
	}
}

// Result summarizes a finished (or interrupted) render.
type Result struct {
	RoundsDone int  // fully accumulated rounds
	Completed  bool // true iff all requested rounds finished
}

// Renderer runs progressive renders over a scene.
type Renderer struct {
	cfg Config
	env *envmap.Map
	sc  *scene.Scene
}

func New(cfg Config, env *envmap.Map, sc *scene.Scene) *Renderer {
	cfg.defaults()
	return &Renderer{cfg: cfg, env: env, sc: sc}
}

// samplePixel computes one round of samples for one pixel. It is a
// pure function of (cfg, env, bvh, pixel, round).
func (r *Renderer) samplePixel(bvh *scene.BVH, cam camera, px, py, round int) geom.Vec3 {
	cfg := &r.cfg
	rng := newRNG(cfg.Seed, py*cfg.Width+px, round)
	// Jittered camera ray.
	jx, jy := rng.float(), rng.float()
	u := (float64(px) + jx) / float64(cfg.Width)
	v := (float64(cfg.Height-1-py) + jy) / float64(cfg.Height) // row 0 = top
	ray := cam.ray(u, v)
	hit, ok := bvh.Intersect(ray, scene.ShadowEpsilon, math.Inf(1))
	if !ok {
		return r.env.Sample(ray.Dir)
	}
	// Direct environment lighting with cosine-weighted sampling:
	//   L_o = albedo * (1/N) * sum( L_env(dir_i) * visible_i )
	// because BRDF = albedo/pi and pdf = cos/pi cancel.
	acc := geom.Vec3{}
	for k := 0; k < cfg.LightSamples; k++ {
		dir := cosineHemisphere(rng, hit.Normal)
		shadowRay := geom.Ray{Orig: hit.Point.Add(hit.Normal.Scale(scene.ShadowEpsilon)), Dir: dir}
		if bvh.Occluded(shadowRay, scene.ShadowEpsilon, math.Inf(1)) {
			continue
		}
		acc = acc.Add(r.env.Sample(dir))
	}
	return acc.Scale(1 / float64(cfg.LightSamples)).Mul(albedoOf(bvh, hit))
}

// albedoOf looks up the albedo of the hit primitive by id.
func albedoOf(bvh *scene.BVH, h scene.Hit) geom.Vec3 {
	return bvh.AlbedoOf(h.ID)
}

// cosineHemisphere samples a cosine-weighted direction around n.
func cosineHemisphere(r *rng, n geom.Vec3) geom.Vec3 {
	r1, r2 := r.float(), r.float()
	phi := 2 * math.Pi * r1
	sr := math.Sqrt(r2)
	x := sr * math.Cos(phi)
	y := sr * math.Sin(phi)
	z := math.Sqrt(math.Max(0, 1-r2))
	// Orthonormal basis around n.
	var t geom.Vec3
	if math.Abs(n.X) > 0.9 {
		t = geom.V(0, 1, 0)
	} else {
		t = geom.V(1, 0, 0)
	}
	b := n.Cross(t).Normalize()
	u := b.Cross(n)
	return u.Scale(x).Add(b.Scale(y)).Add(n.Scale(z)).Normalize()
}

// tile is a rectangular pixel block.
type tile struct{ x0, y0, x1, y1 int }

func (r *Renderer) tiles() []tile {
	cfg := &r.cfg
	var ts []tile
	for y := 0; y < cfg.Height; y += cfg.TileSize {
		for x := 0; x < cfg.Width; x += cfg.TileSize {
			t := tile{x, y, min(x+cfg.TileSize, cfg.Width), min(y+cfg.TileSize, cfg.Height)}
			ts = append(ts, t)
		}
	}
	return ts
}

// renderRound accumulates one full round of samples into hdr.
// Tiles are pulled from an atomic counter, so a slow tile only
// occupies its own worker; work stealing keeps cores busy.
// Returns false if ctx was cancelled mid-round (partial tile
// results of the interrupted round are discarded).
func (r *Renderer) renderRound(ctx context.Context, bvh *scene.BVH, cam camera, hdr *image.HDR, round int) bool {
	cfg := &r.cfg
	ts := r.tiles()
	var next int64 = -1
	var wg sync.WaitGroup
	cancelled := atomic.Bool{}
	worker := func() {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				cancelled.Store(true)
				return
			}
			i := atomic.AddInt64(&next, 1)
			if i >= int64(len(ts)) {
				return
			}
			t := ts[i]
			for py := t.y0; py < t.y1; py++ {
				for px := t.x0; px < t.x1; px++ {
					c := r.samplePixel(bvh, cam, px, py, round)
					idx := py*cfg.Width + px
					hdr.Sum[idx] = hdr.Sum[idx].Add(c)
				}
			}
		}
	}
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	return !cancelled.Load()
}

// Run renders progressively until done, ctx cancellation, or deadline.
// Completed rounds are always consistent: a cancelled run simply has
// fewer rounds. If cfg.Resume is set and a valid checkpoint exists,
// rendering continues from it; the accumulated result is identical to
// an uninterrupted run.
func (r *Renderer) Run(ctx context.Context) (*image.HDR, Result, error) {
	cfg := &r.cfg
	hdr := image.NewHDR(cfg.Width, cfg.Height)
	startRound := 0
	if cfg.Resume && cfg.CheckpointPath != "" {
		done, err := r.loadCheckpoint(hdr)
		if err != nil {
			return nil, Result{}, err
		}
		startRound = done
	}
	if startRound >= cfg.Samples {
		return hdr, Result{RoundsDone: startRound, Completed: true}, nil
	}

	// Hold a read lock for the whole render: the BVH is a consistent
	// snapshot; concurrent scene updates block until we finish.
	bvh, release := r.sc.Acquire()
	defer release()

	aspect := float64(cfg.Width) / float64(cfg.Height)
	cam := newCamera(cfg.CamPos, cfg.CamLookAt, cfg.FovDeg, aspect)

	res := Result{RoundsDone: startRound}
	for round := startRound; round < cfg.Samples; round++ {
		if ctx.Err() != nil {
			return hdr, res, nil
		}
		if !r.renderRound(ctx, bvh, cam, hdr, round) {
			return hdr, res, nil // round discarded; progress stays at res.RoundsDone
		}
		hdr.Samples = round + 1
		res.RoundsDone = round + 1
		if cfg.CheckpointPath != "" {
			if err := r.saveCheckpoint(hdr); err != nil {
				return hdr, res, fmt.Errorf("checkpoint: %w", err)
			}
		}
		if cfg.PreviewPath != "" {
			rgba := hdr.ToRGBA(image.TonemapParams{Exposure: cfg.Exposure, Operator: cfg.TonemapOp})
			if err := image.WritePNGAtomic(cfg.PreviewPath, cfg.Width, cfg.Height, rgba); err != nil {
				return hdr, res, fmt.Errorf("preview: %w", err)
			}
		}
	}
	res.Completed = true
	return hdr, res, nil
}

// ---- checkpointing ----

const ckptMagic = "ELRCKPT1"

// saveCheckpoint writes the accumulator state atomically.
func (r *Renderer) saveCheckpoint(hdr *image.HDR) error {
	cfg := &r.cfg
	buf := make([]byte, 0, 64+len(hdr.Sum)*24)
	buf = append(buf, ckptMagic...)
	var tmp [8]byte
	putU := func(v uint64) { binary.LittleEndian.PutUint64(tmp[:], v); buf = append(buf, tmp[:]...) }
	putU(uint64(cfg.Width))
	putU(uint64(cfg.Height))
	putU(uint64(cfg.Samples))
	putU(uint64(cfg.LightSamples))
	putU(cfg.Seed)
	putU(uint64(hdr.Samples))
	for _, c := range hdr.Sum {
		putU(math.Float64bits(c.X))
		putU(math.Float64bits(c.Y))
		putU(math.Float64bits(c.Z))
	}
	return image.WriteFileAtomic(cfg.CheckpointPath, buf)
}

// loadCheckpoint reads a checkpoint into hdr and returns the number of
// completed rounds. A missing file means "start from zero"; a mismatched
// configuration is an error (resuming with different parameters would
// silently corrupt the accumulation).
func (r *Renderer) loadCheckpoint(hdr *image.HDR) (int, error) {
	cfg := &r.cfg
	data, err := os.ReadFile(cfg.CheckpointPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(data) < 8+6*8 || string(data[:8]) != ckptMagic {
		return 0, fmt.Errorf("checkpoint: bad magic")
	}
	off := 8
	getU := func() uint64 { v := binary.LittleEndian.Uint64(data[off:]); off += 8; return v }
	w := int(getU())
	h := int(getU())
	samples := int(getU())
	lightSamples := int(getU())
	seed := getU()
	rounds := int(getU())
	if w != cfg.Width || h != cfg.Height || samples != cfg.Samples ||
		lightSamples != cfg.LightSamples || seed != cfg.Seed {
		return 0, fmt.Errorf("checkpoint: config mismatch (file %dx%d s=%d ls=%d seed=%d)",
			w, h, samples, lightSamples, seed)
	}
	want := 8 + 6*8 + w*h*3*8
	if len(data) != want {
		return 0, fmt.Errorf("checkpoint: truncated (got %d bytes, want %d)", len(data), want)
	}
	for i := range hdr.Sum {
		hdr.Sum[i] = geom.V(
			math.Float64frombits(getU()),
			math.Float64frombits(getU()),
			math.Float64frombits(getU()),
		)
	}
	hdr.Samples = rounds
	return rounds, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
