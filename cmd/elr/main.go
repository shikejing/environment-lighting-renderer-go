// Command elr renders a procedural scene lit by a local HDR environment
// map into a PNG, deterministically and progressively.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shikejing/environment-lighting-renderer-go/internal/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
	"github.com/shikejing/environment-lighting-renderer-go/internal/render"
	"github.com/shikejing/environment-lighting-renderer-go/internal/scene"
	"github.com/shikejing/environment-lighting-renderer-go/internal/tonemap"
)

func main() {
	var (
		out        = flag.String("out", "render.png", "output PNG path")
		envPath    = flag.String("env", "", "environment map .hdr path (empty = procedural sky)")
		envOut     = flag.String("env-out", "", "if set, save the (generated or loaded) env map here")
		envW       = flag.Int("env-w", 512, "generated env map width")
		envH       = flag.Int("env-h", 256, "generated env map height")
		sun        = flag.Float64("sun", 200, "sun disk intensity (may be extreme, e.g. 1e6)")
		grid       = flag.Int("grid", 12, "procedural scene grid size (grid^2 spheres)")
		sceneSeed  = flag.Uint64("scene-seed", 42, "procedural scene seed")
		w          = flag.Int("w", 320, "image width")
		h          = flag.Int("h", 240, "image height")
		samples    = flag.Int("samples", 16, "sample rounds per pixel")
		seed       = flag.Uint64("seed", 1234, "render master seed")
		threads    = flag.Int("threads", 0, "worker threads (0 = NumCPU)")
		tile       = flag.Int("tile", 32, "tile size in pixels")
		checkpoint = flag.String("checkpoint", "", "checkpoint file path (enables resume)")
		resume     = flag.Bool("resume", false, "resume from checkpoint")
		preview    = flag.String("preview", "", "progressive preview PNG path")
		timeout    = flag.Duration("timeout", 0, "abort render after this duration (0 = none)")
		tm         = flag.String("tonemap", "aces", "tone mapping: clamp|reinhard|aces")
		exposure   = flag.Float64("exposure", 1, "linear exposure multiplier")
		gamma      = flag.Float64("gamma", 2.2, "display gamma")
	)
	flag.Parse()

	op, ok := tonemap.ParseOperator(*tm)
	if !ok {
		log.Fatalf("unknown tonemap %q", *tm)
	}
	tmcfg := tonemap.Config{Exposure: *exposure, Gamma: *gamma, Op: op}

	// Environment map.
	var env *envmap.Map
	var err error
	if *envPath != "" {
		env, err = envmap.LoadRGBE(*envPath)
		if err != nil {
			log.Fatalf("load env map: %v", err)
		}
	} else {
		env = envmap.GenerateSky(*envW, *envH, *sun)
	}
	if *envOut != "" {
		if err := envmap.SaveRGBE(*envOut, env); err != nil {
			log.Fatalf("save env map: %v", err)
		}
	}

	sc := scene.Generate(*sceneSeed, *grid)
	snap := sc.Snapshot()
	log.Printf("scene: %d spheres (snapshot v%d), env %dx%d", snap.Count(), snap.Version, env.Width, env.Height)

	cfg := render.Config{
		Width: *w, Height: *h, Samples: *samples, TileSize: *tile,
		Seed: *seed, Threads: *threads,
		CheckpointPath: *checkpoint,
		PreviewPath:    *preview,
		ToneMap: render.ToneMapFunc(func(c geom.Vec3) [4]uint8 {
			rgba := tonemap.ToRGBA(c, tmcfg)
			return [4]uint8{rgba.R, rgba.G, rgba.B, rgba.A}
		}),
		OnRound: func(done int) { log.Printf("round %d/%d complete", done, *samples) },
	}

	var r *render.Renderer
	if *resume {
		if *checkpoint == "" {
			log.Fatal("-resume requires -checkpoint")
		}
		r, err = render.ResumeRenderer(cfg, snap, env, *checkpoint)
		if err != nil {
			log.Fatalf("resume: %v", err)
		}
		log.Printf("resumed from round %d/%d", r.RoundsDone(), *samples)
	} else {
		r, err = render.New(cfg, snap, env)
		if err != nil {
			log.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Print("interrupted, finishing current round...")
		cancel()
	}()

	start := time.Now()
	res, err := r.Run(ctx)
	if err != nil {
		log.Fatalf("render: %v", err)
	}
	elapsed := time.Since(start)
	log.Printf("render done: %d/%d rounds in %s (completed=%v interrupted=%v)",
		res.RoundsDone, *samples, elapsed.Round(time.Millisecond), res.Completed, res.Interrupted)

	if !res.Completed {
		log.Printf("partial progress kept in checkpoint; not writing final output %s", *out)
		os.Exit(2)
	}

	avg := res.Average()
	pix := make([][4]uint8, len(avg))
	for i, c := range avg {
		pix[i] = cfg.ToneMap.ToRGBA(c)
	}
	if err := writePNG(*out, *w, *h, pix); err != nil {
		log.Fatalf("write output: %v", err)
	}
	log.Printf("wrote %s", *out)
}
