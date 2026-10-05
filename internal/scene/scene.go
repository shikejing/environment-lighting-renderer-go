package scene

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// snapshot is an immutable, published view of the scene.
type snapshot struct {
	root    *bvhNode
	spheres map[ID]Sphere
	version uint64
}

// Scene is a mutable scene that publishes immutable snapshots.
//
// Renders operate on a Snapshot obtained via Snapshot(); updates mutate the
// live scene and publish a new snapshot atomically, so a render that started
// before or during an update never observes a half-updated structure.
type Scene struct {
	mu      sync.Mutex // serializes updates
	spheres map[ID]Sphere
	root    *bvhNode
	nextID  ID
	version uint64
	snap    atomic.Pointer[snapshot]
}

func New() *Scene {
	s := &Scene{spheres: make(map[ID]Sphere), nextID: 1}
	s.publish()
	return s
}

func (s *Scene) publish() {
	s.version++
	// Copy the primitive map so published snapshots are fully immutable;
	// the BVH itself is already persistent (copy-on-write paths).
	spheres := make(map[ID]Sphere, len(s.spheres))
	for id, sp := range s.spheres {
		spheres[id] = sp
	}
	s.snap.Store(&snapshot{root: s.root, spheres: spheres, version: s.version})
}

// Snapshot returns the current immutable view. Safe for concurrent use.
func (s *Scene) Snapshot() Snapshot {
	sn := s.snap.Load()
	return Snapshot{root: sn.root, spheres: sn.spheres, Version: sn.version}
}

// Add inserts a sphere and returns its stable ID.
func (s *Scene) Add(sp Sphere) ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.spheres[id] = sp
	s.root = s.root.insert(id, sp.Bounds())
	s.publish()
	return id
}

// Remove deletes a primitive. Returns false if it does not exist.
func (s *Scene) Remove(id ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.spheres[id]; !ok {
		return false
	}
	delete(s.spheres, id)
	root, _ := s.root.remove(id)
	s.root = root
	s.publish()
	return true
}

// Update replaces the sphere for id (incremental: remove + reinsert).
func (s *Scene) Update(id ID, sp Sphere) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.spheres[id]; !ok {
		return false
	}
	delete(s.spheres, id)
	root, _ := s.root.remove(id)
	s.spheres[id] = sp
	s.root = root.insert(id, sp.Bounds())
	s.publish()
	return true
}

// Rebuild reconstructs the BVH from scratch (full build). Used to verify
// that incremental updates converge to the same query results.
func (s *Scene) Rebuild() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]ID, 0, len(s.spheres))
	boxes := make(map[ID]geom.AABB, len(s.spheres))
	for id, sp := range s.spheres {
		ids = append(ids, id)
		boxes[id] = sp.Bounds()
	}
	s.root = buildBVH(ids, boxes)
	s.publish()
}

// Len returns the number of primitives in the live scene.
func (s *Scene) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.spheres)
}

// Snapshot is an immutable, render-ready view of the scene.
type Snapshot struct {
	root    *bvhNode
	spheres map[ID]Sphere
	Version uint64
}

// Intersect finds the nearest hit along r in (tmin, tmax).
func (sn Snapshot) Intersect(r geom.Ray, tmin, tmax float64) (HitRecord, bool) {
	if sn.root == nil {
		return HitRecord{}, false
	}
	return sn.root.intersect(r, tmin, tmax, sn.spheres)
}

// Occluded reports whether any primitive blocks r in (tmin, tmax).
func (sn Snapshot) Occluded(r geom.Ray, tmin, tmax float64) bool {
	if sn.root == nil {
		return false
	}
	return sn.root.occluded(r, tmin, tmax, sn.spheres)
}

// Sphere returns the primitive data for a hit ID.
func (sn Snapshot) Sphere(id ID) Sphere { return sn.spheres[id] }

// Count returns the number of primitives in this snapshot.
func (sn Snapshot) Count() int { return len(sn.spheres) }

// Validate checks BVH structural consistency (used by tests).
func (sn Snapshot) Validate() error {
	if sn.root == nil {
		if len(sn.spheres) != 0 {
			return fmt.Errorf("nil root with %d spheres", len(sn.spheres))
		}
		return nil
	}
	seen := make(map[ID]int)
	if err := validateNode(sn.root, seen); err != nil {
		return err
	}
	if len(seen) != len(sn.spheres) {
		return fmt.Errorf("bvh holds %d leaves, scene has %d spheres", len(seen), len(sn.spheres))
	}
	for id := range sn.spheres {
		if seen[id] != 1 {
			return fmt.Errorf("sphere %d appears %d times in bvh", id, seen[id])
		}
	}
	return nil
}

func validateNode(n *bvhNode, seen map[ID]int) error {
	if n.leaf {
		seen[n.id]++
		return nil
	}
	if n.left == nil || n.right == nil {
		return fmt.Errorf("internal node missing child")
	}
	u := n.left.box.Union(n.right.box)
	if u != n.box {
		return fmt.Errorf("stale bounds on internal node")
	}
	if err := validateNode(n.left, seen); err != nil {
		return err
	}
	return validateNode(n.right, seen)
}

// Generate builds a deterministic procedural scene: a ground plane of
// spheres on a jittered grid plus floating spheres, driven by seed.
func Generate(seed uint64, gridN int) *Scene {
	rng := splitmix64{state: seed}
	s := New()
	// Ground: one very large sphere acts as the floor.
	s.Add(Sphere{
		Center: geom.Vec3{X: 0, Y: -1000, Z: 0},
		Radius: 1000,
		Albedo: geom.Vec3{X: 0.75, Y: 0.72, Z: 0.68},
	})
	for i := 0; i < gridN; i++ {
		for j := 0; j < gridN; j++ {
			x := (float64(i)-float64(gridN)/2)*2.2 + (rng.next()-0.5)*0.6
			z := (float64(j)-float64(gridN)/2)*2.2 + (rng.next()-0.5)*0.6
			r := 0.35 + rng.next()*0.55
			albedo := geom.Vec3{
				X: 0.3 + 0.7*rng.next(),
				Y: 0.3 + 0.7*rng.next(),
				Z: 0.3 + 0.7*rng.next(),
			}
			s.Add(Sphere{Center: geom.Vec3{X: x, Y: r, Z: z}, Radius: r, Albedo: albedo})
		}
	}
	// Floating occluders.
	nFloat := gridN * gridN / 4
	for k := 0; k < nFloat; k++ {
		x := (rng.next() - 0.5) * float64(gridN) * 2.0
		y := 1.5 + rng.next()*4.0
		z := (rng.next() - 0.5) * float64(gridN) * 2.0
		r := 0.2 + rng.next()*0.4
		s.Add(Sphere{
			Center: geom.Vec3{X: x, Y: y, Z: z},
			Radius: r,
			Albedo: geom.Vec3{X: 0.8, Y: 0.8, Z: 0.8},
		})
	}
	return s
}

// splitmix64 is a small deterministic PRNG (also used for per-sample seeds).
type splitmix64 struct{ state uint64 }

func (s *splitmix64) next() float64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	// 53-bit mantissa in [0,1)
	return float64(z>>11) * (1.0 / 9007199254740992.0)
}

// HashSeed derives a deterministic per-(pixel, sample) seed.
func HashSeed(seed uint64, x, y, sample int) uint64 {
	h := seed*0x9E3779B97F4A7C15 + 0xBF58476D1CE4E5B9
	h ^= uint64(x)*0x94D049BB133111EB + 0x2545F4914F6CDD1D
	h ^= uint64(y) * 0xFF51AFD7ED558CCD
	h ^= uint64(sample) * 0xC4CEB9FE1A85EC53
	h ^= h >> 33
	h *= 0xFF51AFD7ED558CCD
	h ^= h >> 29
	return h
}
