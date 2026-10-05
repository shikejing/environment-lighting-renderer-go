package geom

import "testing"

func TestAABBUnionSurfaceArea(t *testing.T) {
	b := EmptyAABB().Expand(V(0, 0, 0)).Expand(V(1, 2, 3))
	if got := b.SurfaceArea(); got != 2*(2.0+6.0+3.0) {
		t.Fatalf("surface area = %v", got)
	}
	u := b.Union(EmptyAABB().Expand(V(2, 0, 0)).Expand(V(3, 1, 1)))
	if u.Min != V(0, 0, 0) || u.Max != V(3, 2, 3) {
		t.Fatalf("union = %+v", u)
	}
}

func TestAABBIntersect(t *testing.T) {
	b := EmptyAABB().Expand(V(-1, -1, -1)).Expand(V(1, 1, 1))
	if !b.Intersect(Ray{V(0, 0, -5), V(0, 0, 1)}, 0, 1e30) {
		t.Fatal("expected hit")
	}
	if b.Intersect(Ray{V(5, 0, -5), V(0, 0, 1)}, 0, 1e30) {
		t.Fatal("expected miss")
	}
	// Parallel ray outside slab.
	if b.Intersect(Ray{V(2, 0, 0), V(0, 0, 1)}, 0, 1e30) {
		t.Fatal("expected miss for parallel ray outside slab")
	}
	// Parallel ray inside slab.
	if !b.Intersect(Ray{V(0, 0, 0), V(0, 0, 1)}, 0, 1e30) {
		t.Fatal("expected hit for parallel ray inside slab")
	}
	// t-range clipping.
	if b.Intersect(Ray{V(0, 0, -5), V(0, 0, 1)}, 0, 2) {
		t.Fatal("expected miss due to tMax")
	}
}
