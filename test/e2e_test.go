// Package e2e runs the CLI binary and compares outputs.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "elr")
	cmd := exec.Command("go", "build", "-o", bin, "../cmd/elr")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return bin
}

func run(t *testing.T, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %v: %v\n%s", args, err, out)
	}
}

// TestCLIReproducibleAcrossThreads: PNG output is byte-identical for
// different thread counts and repeated runs.
func TestCLIReproducibleAcrossThreads(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	var ref []byte
	for i, threads := range []string{"1", "4", "2", "1"} {
		out := filepath.Join(dir, "out"+string(rune('a'+i))+".png")
		run(t, bin, "-w", "96", "-h", "72", "-samples", "4", "-grid", "6",
			"-threads", threads, "-seed", "777", "-out", out)
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			ref = data
			continue
		}
		if !bytes.Equal(ref, data) {
			t.Fatalf("threads=%s: output differs", threads)
		}
	}
}

// TestCLIResume: interrupted then resumed render equals uninterrupted.
func TestCLIResume(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "full.png")
	run(t, bin, "-w", "256", "-h", "192", "-samples", "40", "-grid", "8",
		"-seed", "5", "-out", full)

	ckpt := filepath.Join(dir, "ckpt.bin")
	partial := filepath.Join(dir, "partial.png")
	// Timeout kills the render mid-way; exit code 2 means partial.
	cmd := exec.Command(bin, "-w", "256", "-h", "192", "-samples", "40", "-grid", "8",
		"-seed", "5", "-timeout", "80ms", "-checkpoint", ckpt, "-out", partial)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("expected exit code 2, got %v\n%s", err, out)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("partial output file must not exist")
	}
	resumed := filepath.Join(dir, "resumed.png")
	run(t, bin, "-w", "256", "-h", "192", "-samples", "40", "-grid", "8",
		"-seed", "5", "-checkpoint", ckpt, "-resume", "-out", resumed)

	a, _ := os.ReadFile(full)
	b, _ := os.ReadFile(resumed)
	if !bytes.Equal(a, b) {
		t.Fatal("resumed output differs from uninterrupted")
	}
}
