package scene

import (
	"math"
	"math/rand"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// bruteForce is the reference nearest-hit over all spheres.
func bruteForce(spheres map[ID]Sphere, r geom.Ray, tmin, tmax float64) (HitRecord, bool) {
	var best HitRecord
	hit := false
	for id, s := range spheres {
		if t, ok := s.intersect(r, tmin, tmax); ok {
			hit = true
			best.T = t
			best.ID = id
			best.Normal = geom.Norm(geom.Sub(r.At(t), s.Center))
			tmax = t
		}
	}
	return best, hit
}

func randomRays(rng *rand.Rand, n int) []geom.Ray {
	rays := make([]geom.Ray, n)
	for i := range rays {
		o := geom.Vec3{X: rng.Float64()*20 - 10, Y: rng.Float64() * 8, Z: rng.Float64()*20 - 10}
		d := geom.Vec3{X: rng.Float64()*2 - 1, Y: rng.Float64()*2 - 1, Z: rng.Float64()*2 - 1}
		if geom.Len(d) < 1e-6 {
			d = geom.Vec3{X: 0, Y: -1, Z: 0}
		}
		rays[i] = geom.Ray{Orig: o, Dir: geom.Norm(d)}
	}
	return rays
}

// TestIncrementalMatchesFullRebuild applies random add/remove/update ops
// and checks that every query matches both a brute-force reference and a
// from-scratch rebuilt BVH.
func TestIncrementalMatchesFullRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sc := New()
	live := make(map[ID]Sphere)

	addSphere := func() {
		sp := Sphere{
			Center: geom.Vec3{X: rng.Float64()*16 - 8, Y: rng.Float64() * 6, Z: rng.Float64()*16 - 8},
			Radius: 0.2 + rng.Float64()*0.8,
			Albedo: geom.Vec3{X: 0.5, Y: 0.5, Z: 0.5},
		}
		id := sc.Add(sp)
		live[id] = sp
	}
	for i := 0; i < 60; i++ {
		addSphere()
	}

	rays := randomRays(rng, 300)

	check := func(phase string) {
		t.Helper()
		snap := sc.Snapshot()
		if err := snap.Validate(); err != nil {
			t.Fatalf("%s: invalid bvh: %v", phase, err)
		}
		// Full rebuild for comparison.
		sc.Rebuild()
		rebuilt := sc.Snapshot()
		if err := rebuilt.Validate(); err != nil {
			t.Fatalf("%s: invalid rebuilt bvh: %v", phase, err)
		}
		for i, r := range rays {
			wantHit, wantOK := bruteForce(live, r, 1e-6, math.Inf(1))
			gotHit, gotOK := snap.Intersect(r, 1e-6, math.Inf(1))
			if gotOK != wantOK {
				t.Fatalf("%s ray %d: intersect ok=%v want %v", phase, i, gotOK, wantOK)
			}
			if wantOK {
				if gotHit.ID != wantHit.ID || gotHit.T != wantHit.T {
					t.Fatalf("%s ray %d: hit=(id %d,t %v) want (id %d,t %v)",
						phase, i, gotHit.ID, gotHit.T, wantHit.ID, wantHit.T)
				}
			}
			// Rebuilt tree must give identical results.
			rbHit, rbOK := rebuilt.Intersect(r, 1e-6, math.Inf(1))
			if rbOK != wantOK || (wantOK && (rbHit.ID != wantHit.ID || rbHit.T != wantHit.T)) {
				t.Fatalf("%s ray %d: rebuilt mismatch", phase, i)
			}
			// Occlusion must agree with nearest-hit existence.
			wantOcc := wantOK && wantHit.T < 50
			if got := snap.Occluded(r, 1e-6, 50); got != wantOcc {
				t.Fatalf("%s ray %d: occluded=%v want %v", phase, i, got, wantOcc)
			}
		}
	}

	check("initial")

	// Random mutations: add, remove, update.
	for round := 0; round < 6; round++ {
		for k := 0; k < 20; k++ {
			switch rng.Intn(3) {
			case 0:
				addSphere()
			case 1:
				if len(live) > 0 {
					var ids []ID
					for id := range live {
						ids = append(ids, id)
					}
					id := ids[rng.Intn(len(ids))]
					if !sc.Remove(id) {
						t.Fatalf("remove %d failed", id)
					}
					delete(live, id)
				}
			case 2:
				if len(live) > 0 {
					var ids []ID
					for id := range live {
						ids = append(ids, id)
					}
					id := ids[rng.Intn(len(ids))]
					sp := Sphere{
						Center: geom.Vec3{X: rng.Float64()*16 - 8, Y: rng.Float64() * 6, Z: rng.Float64()*16 - 8},
						Radius: 0.2 + rng.Float64()*0.8,
						Albedo: geom.Vec3{X: 0.4, Y: 0.4, Z: 0.4},
					}
					if !sc.Update(id, sp) {
						t.Fatalf("update %d failed", id)
					}
					live[id] = sp
				}
			}
		}
		check("after-mutations")
	}
}

// TestSnapshotIsolation ensures a snapshot taken before an update is
// unaffected by the update (no half-updated reads).
func TestSnapshotIsolation(t *testing.T) {
	sc := New()
	id := sc.Add(Sphere{Center: geom.Vec3{X: 0, Y: 0, Z: 0}, Radius: 1})
	before := sc.Snapshot()
	sc.Update(id, Sphere{Center: geom.Vec3{X: 100, Y: 0, Z: 0}, Radius: 1})
	sc.Add(Sphere{Center: geom.Vec3{X: 5, Y: 0, Z: 0}, Radius: 1})

	ray := geom.Ray{Orig: geom.Vec3{X: 0, Y: 10, Z: 0}, Dir: geom.Vec3{X: 0, Y: -1, Z: 0}}
	if _, ok := before.Intersect(ray, 1e-6, math.Inf(1)); !ok {
		t.Fatal("old snapshot lost its sphere")
	}
	after := sc.Snapshot()
	if _, ok := after.Intersect(ray, 1e-6, math.Inf(1)); ok {
		t.Fatal("new snapshot should not hit moved sphere at old location")
	}
	if before.Count() != 1 || after.Count() != 2 {
		t.Fatalf("counts: before=%d after=%d", before.Count(), after.Count())
	}
}
