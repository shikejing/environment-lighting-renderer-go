package scene

import (
	"math"
	"math/rand"
	"sync"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// Scene owns a BVH and serializes renders (read) against
// incremental updates (write). A render that starts while an
// update is in progress blocks until the update completes, so it
// never observes a half-updated structure; a render already in
// progress keeps a consistent snapshot for its whole duration.
type Scene struct {
	mu  sync.RWMutex
	bvh *BVH
}

func NewScene() *Scene { return &Scene{bvh: NewBVH()} }

// Acquire locks the scene for reading and returns the BVH plus an
// unlock function that must be called when the render is done.
func (s *Scene) Acquire() (*BVH, func()) {
	s.mu.RLock()
	return s.bvh, s.mu.RUnlock
}

// Update applies fn to the BVH under an exclusive write lock.
// Typical fns are sequences of Insert/Remove/Replace calls.
func (s *Scene) Update(fn func(b *BVH)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.bvh)
}

// BuildProcedural generates a deterministic procedural scene:
// a large ground plane plus a seeded arrangement of spheres
// (a jittered grid with a few stacked "towers"). The same seed
// always produces the same scene.
func BuildProcedural(seed uint64, numSpheres int) *Scene {
	rng := rand.New(rand.NewSource(int64(seed)))
	s := NewScene()
	s.Update(func(b *BVH) {
		// Ground: two big triangles at y=0.
		const g = 60.0
		gray := geom.V(0.72, 0.72, 0.72)
		b.Insert(&Triangle{A: geom.V(-g, 0, -g), B: geom.V(g, 0, -g), C: geom.V(g, 0, g), Color: gray})
		b.Insert(&Triangle{A: geom.V(-g, 0, -g), B: geom.V(g, 0, g), C: geom.V(-g, 0, g), Color: gray})

		palette := []geom.Vec3{
			geom.V(0.75, 0.25, 0.22),
			geom.V(0.25, 0.55, 0.75),
			geom.V(0.35, 0.70, 0.30),
			geom.V(0.80, 0.65, 0.25),
			geom.V(0.55, 0.35, 0.70),
		}
		// Jittered grid of spheres.
		side := int(math.Ceil(math.Sqrt(float64(numSpheres))))
		const spacing = 3.0
		placed := 0
		for i := 0; i < side && placed < numSpheres; i++ {
			for j := 0; j < side && placed < numSpheres; j++ {
				cx := (float64(i) - float64(side-1)/2) * spacing
				cz := (float64(j) - float64(side-1)/2) * spacing
				cx += (rng.Float64() - 0.5) * 1.2
				cz += (rng.Float64() - 0.5) * 1.2
				r := 0.55 + rng.Float64()*0.45
				col := palette[rng.Intn(len(palette))]
				b.Insert(&Sphere{
					Center: geom.V(cx, r, cz),
					Radius: r,
					Color:  col,
				})
				placed++
				// Occasionally stack a smaller sphere on top.
				if rng.Float64() < 0.18 && placed < numSpheres {
					r2 := r * (0.4 + rng.Float64()*0.2)
					b.Insert(&Sphere{
						Center: geom.V(cx, 2*r+r2*0.9, cz),
						Radius: r2,
						Color:  palette[rng.Intn(len(palette))],
					})
					placed++
				}
			}
		}
	})
	return s
}
