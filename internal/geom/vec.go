// Package geom provides basic 3D vector and ray math.
package geom

import "math"

// Vec3 is a 3D vector / RGB color.
type Vec3 struct{ X, Y, Z float64 }

func Add(a, b Vec3) Vec3           { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func Sub(a, b Vec3) Vec3           { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func Mul(a, b Vec3) Vec3           { return Vec3{a.X * b.X, a.Y * b.Y, a.Z * b.Z} }
func Scale(a Vec3, s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func Dot(a, b Vec3) float64        { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func Cross(a, b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}
func Len(a Vec3) float64 { return math.Sqrt(Dot(a, a)) }
func Norm(a Vec3) Vec3   { return Scale(a, 1/Len(a)) }
func Neg(a Vec3) Vec3    { return Vec3{-a.X, -a.Y, -a.Z} }

// Ray is an origin + direction (direction need not be normalized).
type Ray struct{ Orig, Dir Vec3 }

func (r Ray) At(t float64) Vec3 { return Add(r.Orig, Scale(r.Dir, t)) }

// AABB is an axis-aligned bounding box.
type AABB struct{ Min, Max Vec3 }

func EmptyAABB() AABB {
	inf := math.Inf(1)
	return AABB{Min: Vec3{inf, inf, inf}, Max: Vec3{-inf, -inf, -inf}}
}

func (b AABB) Union(o AABB) AABB {
	return AABB{
		Min: Vec3{math.Min(b.Min.X, o.Min.X), math.Min(b.Min.Y, o.Min.Y), math.Min(b.Min.Z, o.Min.Z)},
		Max: Vec3{math.Max(b.Max.X, o.Max.X), math.Max(b.Max.Y, o.Max.Y), math.Max(b.Max.Z, o.Max.Z)},
	}
}

func (b AABB) UnionPoint(p Vec3) AABB {
	return b.Union(AABB{Min: p, Max: p})
}

// Hit tests ray against box in [tmin, tmax]; returns false on miss.
func (b AABB) Hit(r Ray, tmin, tmax float64) bool {
	for ax := 0; ax < 3; ax++ {
		var o, d, mn, mx float64
		switch ax {
		case 0:
			o, d, mn, mx = r.Orig.X, r.Dir.X, b.Min.X, b.Max.X
		case 1:
			o, d, mn, mx = r.Orig.Y, r.Dir.Y, b.Min.Y, b.Max.Y
		default:
			o, d, mn, mx = r.Orig.Z, r.Dir.Z, b.Min.Z, b.Max.Z
		}
		inv := 1 / d
		t0 := (mn - o) * inv
		t1 := (mx - o) * inv
		if inv < 0 {
			t0, t1 = t1, t0
		}
		if t0 > tmin {
			tmin = t0
		}
		if t1 < tmax {
			tmax = t1
		}
		if tmax <= tmin {
			return false
		}
	}
	return true
}
