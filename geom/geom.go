// Package geom provides basic 3D math: vectors, rays and bounding boxes.
package geom

import "math"

// Vec3 is a 3D vector / point / RGB triple.
type Vec3 struct{ X, Y, Z float64 }

func V(x, y, z float64) Vec3 { return Vec3{x, y, z} }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Mul(b Vec3) Vec3      { return Vec3{a.X * b.X, a.Y * b.Y, a.Z * b.Z} }
func (a Vec3) Neg() Vec3            { return Vec3{-a.X, -a.Y, -a.Z} }

func (a Vec3) Dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}
func (a Vec3) Len() float64    { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Len2() float64   { return a.Dot(a) }
func (a Vec3) Normalize() Vec3 { return a.Scale(1 / a.Len()) }

// Ray is an origin + (normalized) direction half-line.
type Ray struct{ Orig, Dir Vec3 }

func (r Ray) At(t float64) Vec3 { return r.Orig.Add(r.Dir.Scale(t)) }

// AABB is an axis-aligned bounding box.
type AABB struct{ Min, Max Vec3 }

// EmptyAABB returns the identity box for Union/Expand.
func EmptyAABB() AABB {
	return AABB{Min: V(math.Inf(1), math.Inf(1), math.Inf(1)), Max: V(math.Inf(-1), math.Inf(-1), math.Inf(-1))}
}

func (b AABB) Expand(p Vec3) AABB {
	return AABB{
		Min: V(math.Min(b.Min.X, p.X), math.Min(b.Min.Y, p.Y), math.Min(b.Min.Z, p.Z)),
		Max: V(math.Max(b.Max.X, p.X), math.Max(b.Max.Y, p.Y), math.Max(b.Max.Z, p.Z)),
	}
}

func (b AABB) Union(o AABB) AABB { return b.Expand(o.Min).Expand(o.Max) }

func (b AABB) SurfaceArea() float64 {
	d := b.Max.Sub(b.Min)
	if d.X < 0 || d.Y < 0 || d.Z < 0 {
		return 0
	}
	return 2 * (d.X*d.Y + d.Y*d.Z + d.Z*d.X)
}

func (b AABB) Centroid() Vec3 { return b.Min.Add(b.Max).Scale(0.5) }

// Intersect reports whether the ray hits the box within [tMin, tMax],
// using the slab method. It is robust to zero direction components.
func (b AABB) Intersect(r Ray, tMin, tMax float64) bool {
	for axis := 0; axis < 3; axis++ {
		var o, d, mn, mx float64
		switch axis {
		case 0:
			o, d, mn, mx = r.Orig.X, r.Dir.X, b.Min.X, b.Max.X
		case 1:
			o, d, mn, mx = r.Orig.Y, r.Dir.Y, b.Min.Y, b.Max.Y
		default:
			o, d, mn, mx = r.Orig.Z, r.Dir.Z, b.Min.Z, b.Max.Z
		}
		t0, t1 := (mn-o)/d, (mx-o)/d // Inf/NaN handled below
		if d == 0 {
			// Ray parallel to slab: must start inside it.
			if o < mn || o > mx {
				return false
			}
			continue
		}
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		if t0 > tMin {
			tMin = t0
		}
		if t1 < tMax {
			tMax = t1
		}
		if tMin > tMax {
			return false
		}
	}
	return true
}
