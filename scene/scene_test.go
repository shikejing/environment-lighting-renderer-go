package scene

import (
	"math"
	"math/rand"
	"testing"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// bruteIntersect is the reference linear scan with identical
// tie-breaking rules (closest t, then smallest id).
func bruteIntersect(prims []Primitive, r geom.Ray, tMin, tMax float64) (Hit, bool) {
	best := Hit{ID: -1}
	found := false
	bestT := tMax
	for _, p := range prims {
		h, ok := p.Intersect(r, tMin, bestT)
		if !ok {
			continue
		}
		if !found || h.T < bestT || (h.T == bestT && h.ID < best.ID) {
			best, bestT, found = h, h.T, true
		}
	}
	return best, found
}

func bruteOccluded(prims []Primitive, r geom.Ray, tMin, tMax float64) bool {
	for _, p := range prims {
		if _, ok := p.Intersect(r, tMin, tMax); ok {
			return true
		}
	}
	return false
}

func randRay(rng *rand.Rand) geom.Ray {
	o := geom.V(rng.Float64()*20-10, rng.Float64()*8+0.1, rng.Float64()*20-10)
	d := geom.V(rng.Float64()*2-1, rng.Float64()*2-1, rng.Float64()*2-1)
	if d.Len2() < 1e-6 {
		d = geom.V(0, -1, 0)
	}
	return geom.Ray{Orig: o, Dir: d.Normalize()}
}

// TestBVHAgainstBruteForce checks closest-hit and occlusion queries
// against a linear scan on random rays: no missed or phantom hits.
func TestBVHAgainstBruteForce(t *testing.T) {
	sc := BuildProcedural(42, 200)
	bvh, release := sc.Acquire()
	defer release()
	prims := bvh.Prims()
	// Ids must be unique for tie-breaking to be meaningful.
	seen := map[int]bool{}
	for _, p := range prims {
		if seen[p.(interface{ getID() int }).getID()] {
			t.Fatal("duplicate primitive id")
		}
		seen[p.(interface{ getID() int }).getID()] = true
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		r := randRay(rng)
		bh, bok := bvh.Intersect(r, 1e-6, 1e30)
		rh, rok := bruteIntersect(prims, r, 1e-6, 1e30)
		if bok != rok {
			t.Fatalf("ray %d: hit mismatch bvh=%v brute=%v", i, bok, rok)
		}
		if bok && (bh.ID != rh.ID || bh.T != rh.T) {
			t.Fatalf("ray %d: bvh=(id %d,t %v) brute=(id %d,t %v)", i, bh.ID, bh.T, rh.ID, rh.T)
		}
		if bvh.Occluded(r, 1e-6, 1e30) != bruteOccluded(prims, r, 1e-6, 1e30) {
			t.Fatalf("ray %d: occlusion mismatch", i)
		}
	}
}

// TestIncrementalUpdateEqualsRebuild applies random inserts, removes
// and replaces, then compares query results against a fresh full
// rebuild over the same live primitive set.
func TestIncrementalUpdateEqualsRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	inc := NewBVH()
	var live []Primitive // primitives currently in `inc`
	var ids []int        // parallel: id of each live prim

	addSphere := func() {
		c := geom.V(rng.Float64()*16-8, rng.Float64()*4+0.5, rng.Float64()*16-8)
		s := &Sphere{Center: c, Radius: 0.3 + rng.Float64()*0.7, Color: geom.V(0.5, 0.5, 0.5)}
		id := inc.Insert(s)
		live = append(live, s)
		ids = append(ids, id)
	}
	for i := 0; i < 120; i++ {
		addSphere()
	}
	for step := 0; step < 300; step++ {
		switch rng.Intn(3) {
		case 0:
			addSphere()
		case 1: // remove
			if len(live) == 0 {
				continue
			}
			k := rng.Intn(len(live))
			if !inc.Remove(ids[k]) {
				t.Fatalf("step %d: Remove(%d) failed", step, ids[k])
			}
			live = append(live[:k], live[k+1:]...)
			ids = append(ids[:k], ids[k+1:]...)
		case 2: // replace (modify)
			if len(live) == 0 {
				continue
			}
			k := rng.Intn(len(live))
			c := geom.V(rng.Float64()*16-8, rng.Float64()*4+0.5, rng.Float64()*16-8)
			ns := &Sphere{Center: c, Radius: 0.3 + rng.Float64()*0.7, Color: geom.V(0.5, 0.5, 0.5)}
			nid, ok := inc.Replace(ids[k], ns)
			if !ok {
				t.Fatalf("step %d: Replace(%d) failed", step, ids[k])
			}
			live[k] = ns
			ids[k] = nid
		}
	}

	// Fresh full rebuild over the same live set (cloned, because
	// Rebuild reassigns ids on the primitives it is given).
	fresh := NewBVH()
	fresh.Rebuild(ClonePrims(live))

	incPrims := inc.Prims()
	if len(incPrims) != len(live) {
		t.Fatalf("live count mismatch: %d vs %d", len(incPrims), len(live))
	}
	for i := 0; i < 30000; i++ {
		r := randRay(rng)
		ih, iok := inc.Intersect(r, 1e-6, 1e30)
		fh, fok := fresh.Intersect(r, 1e-6, 1e30)
		if iok != fok {
			t.Fatalf("ray %d: hit mismatch inc=%v fresh=%v", i, iok, fok)
		}
		if iok && ih.T != fh.T {
			t.Fatalf("ray %d: t mismatch inc=%v fresh=%v", i, ih.T, fh.T)
		}
		if iok && ih.Point != fh.Point {
			t.Fatalf("ray %d: hit point mismatch", i)
		}
		if inc.Occluded(r, 1e-6, 1e30) != fresh.Occluded(r, 1e-6, 1e30) {
			t.Fatalf("ray %d: occlusion mismatch", i)
		}
	}
}

// TestNoSelfIntersection verifies that a shadow ray spawned from a
// hit point (offset by ShadowEpsilon along the normal) does not hit
// the same primitive again.
func TestNoSelfIntersection(t *testing.T) {
	sc := BuildProcedural(5, 100)
	bvh, release := sc.Acquire()
	defer release()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 5000; i++ {
		r := geom.Ray{
			Orig: geom.V(rng.Float64()*10-5, 10, rng.Float64()*10-5),
			Dir:  geom.V(0, -1, 0),
		}
		h, ok := bvh.Intersect(r, 1e-9, 1e30)
		if !ok {
			continue
		}
		origin := h.Point.Add(h.Normal.Scale(ShadowEpsilon))
		shadow := geom.Ray{Orig: origin, Dir: h.Normal}
		if sh, ok := bvh.Intersect(shadow, 1e-9, 1e-3); ok && sh.ID == h.ID {
			t.Fatalf("ray %d: self-intersection with prim %d at t=%v", i, h.ID, sh.T)
		}
	}
}

// TestSceneSnapshotIsolation: a reader holding Acquire sees a stable
// structure while an updater waits; updates are applied atomically.
func TestSceneSnapshotIsolation(t *testing.T) {
	sc := BuildProcedural(1, 50)
	bvh1, release1 := sc.Acquire()
	n1 := len(bvh1.Prims())
	done := make(chan struct{})
	go func() {
		sc.Update(func(b *BVH) {
			for i := 0; i < 10; i++ {
				b.Insert(&Sphere{Center: geom.V(0, float64(i)+1, 0), Radius: 0.5, Color: geom.V(1, 0, 0)})
			}
		})
		close(done)
	}()
	// While we hold the read lock the update cannot complete.
	select {
	case <-done:
		t.Fatal("update completed while read lock held")
	default:
	}
	if got := len(bvh1.Prims()); got != n1 {
		t.Fatalf("structure changed under reader: %d -> %d", n1, got)
	}
	release1()
	<-done
	bvh2, release2 := sc.Acquire()
	defer release2()
	if got := len(bvh2.Prims()); got != n1+10 {
		t.Fatalf("after update: %d prims, want %d", got, n1+10)
	}
}

func TestSphereTriangleBasics(t *testing.T) {
	s := &Sphere{Center: geom.V(0, 0, 0), Radius: 1}
	if _, ok := s.Intersect(geom.Ray{Orig: geom.V(0, 0, 5), Dir: geom.V(0, 0, -1)}, 1e-9, 1e30); !ok {
		t.Fatal("sphere miss")
	}
	if _, ok := s.Intersect(geom.Ray{Orig: geom.V(0, 2, 5), Dir: geom.V(0, 0, -1)}, 1e-9, 1e30); ok {
		t.Fatal("sphere phantom hit")
	}
	tr := &Triangle{A: geom.V(-1, 0, 0), B: geom.V(1, 0, 0), C: geom.V(0, 0, 2)}
	h, ok := tr.Intersect(geom.Ray{Orig: geom.V(0, 3, 0.5), Dir: geom.V(0, -1, 0)}, 1e-9, 1e30)
	if !ok || math.Abs(h.Point.Y) > 1e-12 {
		t.Fatalf("triangle hit = %+v ok=%v", h, ok)
	}
}
