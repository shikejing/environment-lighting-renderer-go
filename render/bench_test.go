package render

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkRenderWorkers measures throughput at different worker
// counts; time must not get worse as workers increase:
//
//	go test -bench=BenchmarkRenderWorkers -benchtime=1x ./render/
func BenchmarkRenderWorkers(b *testing.B) {
	env, sc := testSetup(11, 200)
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			cfg := testConfig(256, 192, 2)
			cfg.Workers = w
			r := New(cfg, env, sc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := r.Run(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
