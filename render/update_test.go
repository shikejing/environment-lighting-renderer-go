package render

import (
	"context"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
	"github.com/shikejing/environment-lighting-renderer-go/scene"
)

// TestRenderAfterIncrementalUpdateEqualsRebuild: rendering a scene
// that was modified through incremental Insert/Remove/Replace must
// produce exactly the same image as rendering a freshly rebuilt scene
// containing the same primitives.
func TestRenderAfterIncrementalUpdateEqualsRebuild(t *testing.T) {
	env, sc := testSetup(21, 60)

	// Capture the live primitive set, then mutate the scene.
	var snapshot []scene.Primitive
	sc.Update(func(b *scene.BVH) {
		snapshot = b.Prims()
		// Remove every 4th primitive, move every 5th, add new ones.
		var removeIDs []int
		for i, p := range snapshot {
			id := pID(p)
			if i%4 == 0 {
				removeIDs = append(removeIDs, id)
			}
		}
		for _, id := range removeIDs {
			b.Remove(id)
		}
		live := b.Prims()
		for i, p := range live {
			if i%5 == 0 {
				if s, ok := p.(*scene.Sphere); ok {
					ns := &scene.Sphere{
						Center: s.Center.Add(geom.V(0.4, 0.1, -0.3)),
						Radius: s.Radius,
						Color:  s.Color,
					}
					b.Replace(pID(p), ns)
				}
			}
		}
		for i := 0; i < 8; i++ {
			b.Insert(&scene.Sphere{
				Center: geom.V(float64(i)*1.5-5, 0.6, 6),
				Radius: 0.6,
				Color:  geom.V(0.8, 0.4, 0.3),
			})
		}
	})

	// Fresh scene rebuilt from the mutated live set.
	scFresh := scene.NewScene()
	sc.Update(func(b *scene.BVH) { snapshot = b.Prims() })
	scFresh.Update(func(b *scene.BVH) { b.Rebuild(scene.ClonePrims(snapshot)) })

	cfg := testConfig(48, 36, 3)
	cfg.Workers = 2
	hdrA, _, err := New(cfg, env, sc).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hdrB, _, err := New(cfg, env, scFresh).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hdrEqual(hdrA, hdrB) {
		t.Fatal("render after incremental update differs from render of rebuilt scene")
	}
}

func pID(p scene.Primitive) int {
	type ider interface{ ID() int }
	return p.(ider).ID()
}
