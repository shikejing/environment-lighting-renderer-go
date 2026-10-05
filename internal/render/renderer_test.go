package render

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shikejing/environment-lighting-renderer-go/internal/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
	"github.com/shikejing/environment-lighting-renderer-go/internal/scene"
)

func testSetup() (scene.Snapshot, *envmap.Map) {
	sc := scene.Generate(42, 6)
	env := envmap.GenerateSky(128, 64, 300)
	return sc.Snapshot(), env
}

func baseCfg(w, h, samples int, threads int) Config {
	return Config{Width: w, Height: h, Samples: samples, TileSize: 16, Seed: 99, Threads: threads}
}

func accumEqual(a, b []geom.Vec3) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeterministicAcrossThreads: identical accumulations for 1, 2, 4, 8
// threads, and identical across repeated runs.
func TestDeterministicAcrossThreads(t *testing.T) {
	snap, env := testSetup()
	var ref []geom.Vec3
	for _, threads := range []int{1, 2, 4, 8, 1} {
		r, err := New(baseCfg(64, 48, 4, threads), snap, env)
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.Run(context.Background())
		if err != nil || !res.Completed {
			t.Fatalf("threads=%d: %v completed=%v", threads, err, res.Completed)
		}
		if ref == nil {
			ref = res.Accum
			continue
		}
		if !accumEqual(ref, res.Accum) {
			t.Fatalf("threads=%d: accumulation differs from reference", threads)
		}
	}
}

// TestCancelResumeMatchesUninterrupted: render cancelled mid-way, resumed
// from checkpoint, must equal a single uninterrupted run bit-for-bit.
func TestCancelResumeMatchesUninterrupted(t *testing.T) {
	snap, env := testSetup()
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "ckpt.bin")

	// Uninterrupted reference.
	cfg := baseCfg(64, 48, 6, 4)
	r, err := New(cfg, snap, env)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Run(context.Background())
	if err != nil || !ref.Completed {
		t.Fatalf("reference run: %v", err)
	}

	// Interrupted run: cancel after 2 rounds.
	cfg.CheckpointPath = ckpt
	ctx, cancel := context.WithCancel(context.Background())
	cfg.OnRound = func(done int) {
		if done == 2 {
			cancel()
		}
	}
	r1, err := New(cfg, snap, env)
	if err != nil {
		t.Fatal(err)
	}
	part, err := r1.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if part.Completed || part.RoundsDone != 2 {
		t.Fatalf("expected interruption at 2 rounds, got %+v", part)
	}

	// Checkpoint must be loadable and consistent.
	cfg.OnRound = nil
	r2, err := ResumeRenderer(cfg, snap, env, ckpt)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r2.RoundsDone() != 2 {
		t.Fatalf("resumed at round %d, want 2", r2.RoundsDone())
	}
	res, err := r2.Run(context.Background())
	if err != nil || !res.Completed {
		t.Fatalf("resumed run: %v completed=%v", err, res.Completed)
	}
	if !accumEqual(ref.Accum, res.Accum) {
		t.Fatal("resumed accumulation differs from uninterrupted reference")
	}
}

// TestTimeoutLeavesNoPartialOutput: a render killed by timeout must not
// leave a partially written checkpoint; the checkpoint on disk must be
// well-formed and reflect a whole number of completed rounds.
func TestTimeoutCheckpointIntegrity(t *testing.T) {
	snap, env := testSetup()
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "ckpt.bin")

	cfg := baseCfg(128, 128, 1000, 4)
	cfg.CheckpointPath = ckpt
	r, err := New(cfg, snap, env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res, err := r.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed {
		t.Skip("machine too fast to time out")
	}
	if !res.Interrupted {
		t.Fatal("expected interrupted result")
	}
	// Checkpoint file must exist and parse to exactly res.RoundsDone rounds.
	fi, err := os.Stat(ckpt)
	if err != nil {
		t.Fatalf("checkpoint missing: %v", err)
	}
	wantSize := int64(8 + 5*8 + cfg.Width*cfg.Height*24)
	if fi.Size() != wantSize {
		t.Fatalf("checkpoint size %d, want %d (partial write?)", fi.Size(), wantSize)
	}
	r2, err := ResumeRenderer(cfg, snap, env, ckpt)
	if err != nil {
		t.Fatalf("reload checkpoint: %v", err)
	}
	if r2.RoundsDone() != res.RoundsDone {
		t.Fatalf("checkpoint rounds %d != reported %d", r2.RoundsDone(), res.RoundsDone)
	}
	// No stray temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "ckpt.bin" {
			t.Fatalf("unexpected leftover file %s", e.Name())
		}
	}
}

// TestResumeMismatchRejected: checkpoints from a different config must be
// rejected rather than silently mis-accumulated.
func TestResumeMismatchRejected(t *testing.T) {
	snap, env := testSetup()
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "ckpt.bin")
	cfg := baseCfg(32, 32, 4, 2)
	cfg.CheckpointPath = ckpt
	r, _ := New(cfg, snap, env)
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad := baseCfg(32, 32, 8, 2) // different sample count
	if _, err := ResumeRenderer(bad, snap, env, ckpt); err == nil {
		t.Fatal("expected mismatch error")
	}
	bad2 := baseCfg(32, 32, 4, 2)
	bad2.Seed = 1 // different seed
	if _, err := ResumeRenderer(bad2, snap, env, ckpt); err == nil {
		t.Fatal("expected seed mismatch error")
	}
}

// TestConcurrentRenderDuringUpdate: hammer the scene with updates while a
// render reads a snapshot; the render must complete with valid results and
// never observe a torn structure (run with -race).
func TestConcurrentRenderDuringUpdate(t *testing.T) {
	sc := scene.Generate(3, 5)
	env := envmap.GenerateSky(64, 32, 100)
	done := make(chan struct{})
	go func() {
		defer close(done)
		rng := uint64(1)
		for i := 0; i < 200; i++ {
			rng = rng*6364136223846793005 + 1442695040888963407
			id := scene.ID(rng>>33)%50 + 1
			sp := scene.Sphere{
				Center: geom.Vec3{X: float64(i % 7), Y: 1, Z: float64(i % 5)},
				Radius: 0.5,
			}
			if !sc.Update(id, sp) {
				sc.Add(sp)
			}
		}
	}()
	snap := sc.Snapshot()
	cfg := baseCfg(32, 24, 2, 4)
	r, err := New(cfg, snap, env)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background())
	if err != nil || !res.Completed {
		t.Fatalf("render during updates: %v", err)
	}
	<-done
	if err := sc.Snapshot().Validate(); err != nil {
		t.Fatalf("scene invalid after updates: %v", err)
	}
}
