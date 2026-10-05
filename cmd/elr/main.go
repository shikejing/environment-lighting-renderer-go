// Command elr is the CLI for the environment lighting renderer.
//
// Subcommands:
//
//	elr gen-env -out sky.pfm [-w 512 -h 256 -sun 0.3,0.6,-1 -sun-radiance 40]
//	elr render -env sky.pfm -out out.png [options]
//
// Render options include -seed, -spheres, -samples, -workers, -tile,
// -timeout, -checkpoint/-resume, -preview, -exposure, -tonemap.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shikejing/environment-lighting-renderer-go/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/geom"
	"github.com/shikejing/environment-lighting-renderer-go/image"
	"github.com/shikejing/environment-lighting-renderer-go/render"
	"github.com/shikejing/environment-lighting-renderer-go/scene"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen-env":
		err = cmdGenEnv(os.Args[2:])
	case "render":
		err = cmdRender(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "elr:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  elr gen-env -out sky.pfm [-w 512 -h 256 -sun x,y,z -sun-radiance 40]
  elr render -env sky.pfm -out out.png [options]
`)
}

func parseVec(s string) (geom.Vec3, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return geom.Vec3{}, fmt.Errorf("want x,y,z, got %q", s)
	}
	var v [3]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return geom.Vec3{}, err
		}
		v[i] = f
	}
	return geom.V(v[0], v[1], v[2]), nil
}

func cmdGenEnv(args []string) error {
	fs := flag.NewFlagSet("gen-env", flag.ContinueOnError)
	out := fs.String("out", "sky.pfm", "output PFM path")
	w := fs.Int("w", 512, "width")
	h := fs.Int("h", 256, "height")
	sun := fs.String("sun", "0.3,0.6,-1", "sun direction")
	sunRad := fs.Float64("sun-radiance", 40, "sun disk radiance")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sunDir, err := parseVec(*sun)
	if err != nil {
		return err
	}
	m := envmap.GenerateSky(*w, *h, sunDir, *sunRad)
	if err := m.SavePFM(*out); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%dx%d)\n", *out, *w, *h)
	return nil
}

func cmdRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	envPath := fs.String("env", "sky.pfm", "environment map (PFM)")
	out := fs.String("out", "out.png", "final output PNG")
	w := fs.Int("w", 640, "image width")
	h := fs.Int("h", 360, "image height")
	samples := fs.Int("samples", 16, "sample rounds per pixel")
	lightSamples := fs.Int("light-samples", 1, "env light samples per round")
	seed := fs.Uint64("seed", 1234, "master RNG seed")
	sceneSeed := fs.Uint64("scene-seed", 7, "procedural scene seed")
	spheres := fs.Int("spheres", 300, "number of procedural spheres")
	workers := fs.Int("workers", runtime.NumCPU(), "worker threads (does not affect output)")
	tile := fs.Int("tile", 16, "tile size in pixels")
	timeout := fs.Duration("timeout", 0, "max render time (0 = none)")
	checkpoint := fs.String("checkpoint", "", "checkpoint file for progress save/resume")
	resume := fs.Bool("resume", false, "resume from checkpoint if present")
	preview := fs.String("preview", "", "write a progressive preview PNG after each round")
	exposure := fs.Float64("exposure", 1, "exposure multiplier")
	tonemap := fs.String("tonemap", "reinhard", "tone map operator: reinhard|clamp|aces")
	camPos := fs.String("cam", "0,6,16", "camera position")
	camLook := fs.String("look", "0,1,0", "camera look-at")
	fov := fs.Float64("fov", 50, "vertical field of view (degrees)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := envmap.LoadPFM(*envPath)
	if err != nil {
		return fmt.Errorf("load env map: %w", err)
	}
	pos, err := parseVec(*camPos)
	if err != nil {
		return err
	}
	look, err := parseVec(*camLook)
	if err != nil {
		return err
	}
	sc := scene.BuildProcedural(*sceneSeed, *spheres)

	cfg := render.Config{
		Width: *w, Height: *h, Samples: *samples,
		LightSamples: *lightSamples, Seed: *seed,
		TileSize: *tile, Workers: *workers,
		CamPos: pos, CamLookAt: look, FovDeg: *fov,
		CheckpointPath: *checkpoint, Resume: *resume,
		PreviewPath: *preview,
		Exposure:    *exposure, TonemapOp: *tonemap,
	}
	r := render.New(cfg, env, sc)

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	// Cancel on SIGINT/SIGTERM: progress so far stays in the checkpoint.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		<-sig
		stop()
	}()

	start := time.Now()
	hdr, res, err := r.Run(ctx)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	// Final output is only written from fully accumulated rounds and
	// only via an atomic rename, so an interrupted run never leaves a
	// half-written image behind.
	rgba := hdr.ToRGBA(image.TonemapParams{Exposure: *exposure, Operator: *tonemap})
	if err := image.WritePNGAtomic(*out, hdr.Width, hdr.Height, rgba); err != nil {
		return err
	}
	status := "completed"
	if !res.Completed {
		status = "interrupted (partial)"
	}
	fmt.Printf("%s: %d/%d rounds in %v -> %s\n", status, res.RoundsDone, *samples, elapsed.Round(time.Millisecond), *out)
	return nil
}
