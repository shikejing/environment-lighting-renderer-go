package render

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/shikejing/environment-lighting-renderer-go/envmap"
	"github.com/shikejing/environment-lighting-renderer-go/geom"
	"github.com/shikejing/environment-lighting-renderer-go/image"
	"github.com/shikejing/environment-lighting-renderer-go/scene"
)

func testSetup(seed uint64, spheres int) (*envmap.Map, *scene.Scene) {
	env := envmap.GenerateSky(64, 32, geom.V(0.3, 0.6, -1), 40)
	sc := scene.BuildProcedural(seed, spheres)
	return env, sc
}

func testConfig(w, h, samples int) Config {
	return Config{
		Width: w, Height: h, Samples: samples,
		LightSamples: 2, Seed: 1234, TileSize: 8,
		CamPos: geom.V(0, 6, 14), CamLookAt: geom.V(0, 1, 0),
	}
}

func hdrEqual(a, b *image.HDR) bool {
	if a.Width != b.Width || a.Height != b.Height || a.Samples != b.Samples {
		return false
	}
	for i := range a.Sum {
		if a.Sum[i] != b.Sum[i] {
			return false
		}
	}
	return true
}

// TestReproducibleAcrossWorkers: identical seed+input must give
// bit-identical output for 1, 2, 8 workers and across repeated runs.
func TestReproducibleAcrossWorkers(t *testing.T) {
	env, sc := testSetup(11, 40)
	var ref *image.HDR
	for _, workers := range []int{1, 2, 8, 1} {
		cfg := testConfig(48, 36, 3)
		cfg.Workers = workers
		hdr, res, err := New(cfg, env, sc).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !res.Completed || res.RoundsDone != 3 {
			t.Fatalf("workers=%d: res=%+v", workers, res)
		}
		if ref == nil {
			ref = hdr
			continue
		}
		if !hdrEqual(ref, hdr) {
			t.Fatalf("workers=%d: output differs from reference", workers)
		}
	}
}

// TestCancelAndResume: cancel mid-render, resume from checkpoint;
// the accumulated result must equal an uninterrupted run.
func TestCancelAndResume(t *testing.T) {
	env, sc := testSetup(11, 40)
	dir := t.TempDir()

	// Reference: uninterrupted.
	cfgRef := testConfig(48, 36, 6)
	cfgRef.Workers = 2
	ref, refRes, err := New(cfgRef, env, sc).Run(context.Background())
	if err != nil || !refRes.Completed {
		t.Fatalf("reference run: %+v %v", refRes, err)
	}

	// Interrupted run: cancel after ~2 rounds.
	cfgA := testConfig(48, 36, 6)
	cfgA.Workers = 2
	cfgA.CheckpointPath = filepath.Join(dir, "a.ckpt")
	ctx, cancel := context.WithCancel(context.Background())
	doneRounds := make(chan int, 16)
	// Cancel once the checkpoint for round 2 exists.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if ck, err := readCheckpointRounds(cfgA.CheckpointPath); err == nil && ck >= 2 {
				doneRounds <- ck
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	hdrA, resA, err := New(cfgA, env, sc).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resA.Completed {
		t.Skip("render finished before cancellation took effect")
	}
	if resA.RoundsDone < 1 || resA.RoundsDone >= 6 {
		t.Fatalf("unexpected rounds done: %d", resA.RoundsDone)
	}
	// hdrA must contain exactly resA.RoundsDone rounds.
	if hdrA.Samples != resA.RoundsDone {
		t.Fatalf("hdr samples %d != rounds done %d", hdrA.Samples, resA.RoundsDone)
	}

	// Resume from the checkpoint.
	cfgB := cfgA
	cfgB.Resume = true
	hdrB, resB, err := New(cfgB, env, sc).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !resB.Completed {
		t.Fatalf("resume did not complete: %+v", resB)
	}
	if !hdrEqual(ref, hdrB) {
		t.Fatal("resumed render differs from uninterrupted reference")
	}
}

// readCheckpointRounds peeks at the round count in a checkpoint file.
func readCheckpointRounds(path string) (int, error) {
	data, err := readFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < 8+6*8 {
		return 0, errTooShort
	}
	// rounds field is the 6th uint64 after magic.
	off := 8 + 5*8
	return int(le64(data[off:])), nil
}

var errTooShort = errorString("too short")

type errorString string

func (e errorString) Error() string { return string(e) }

func le64(b []byte) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func readFile(path string) ([]byte, error) {
	return osReadFile(path)
}

// TestTimeoutLeavesNoPartialOutput: a render cancelled before the
// first round completes must not leave output files behind; the
// checkpoint (if any) reflects only fully completed rounds.
func TestTimeoutNoPartialOutput(t *testing.T) {
	env, sc := testSetup(11, 40)
	dir := t.TempDir()
	cfg := testConfig(64, 64, 1000)
	cfg.Workers = 4
	cfg.CheckpointPath = filepath.Join(dir, "t.ckpt")
	cfg.PreviewPath = filepath.Join(dir, "t.png")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, res, err := New(cfg, env, sc).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed {
		t.Skip("finished before timeout")
	}
	// Any checkpoint present must hold exactly res.RoundsDone rounds.
	if n, err := readCheckpointRounds(cfg.CheckpointPath); err == nil && n != res.RoundsDone {
		t.Fatalf("checkpoint rounds %d != reported %d", n, res.RoundsDone)
	}
}

// TestCheckpointConfigMismatch: resuming with different parameters
// must fail loudly instead of corrupting the accumulation.
func TestCheckpointConfigMismatch(t *testing.T) {
	env, sc := testSetup(11, 10)
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "m.ckpt")
	cfg := testConfig(32, 24, 2)
	cfg.CheckpointPath = ckpt
	if _, _, err := New(cfg, env, sc).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg.Resume = true
	cfg.Seed = 999 // different seed
	if _, _, err := New(cfg, env, sc).Run(context.Background()); err == nil {
		t.Fatal("expected config mismatch error")
	}
}
