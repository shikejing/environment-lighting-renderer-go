package scene

import (
	"math"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// Hit records a ray-primitive intersection.
type Hit struct {
	T      float64
	Point  geom.Vec3
	Normal geom.Vec3 // geometric, oriented against the ray
	ID     int       // primitive id, -1 if none
}

// Epsilon used to avoid self-intersection when spawning new rays.
const ShadowEpsilon = 1e-6

// Primitive is a renderable, intersectable object.
type Primitive interface {
	// ID returns the id assigned by the BVH on insert (-1 before).
	// Intersect returns the closest hit in (tMin, tMax); ok=false if none.
	Intersect(r geom.Ray, tMin, tMax float64) (Hit, bool)
	Bounds() geom.AABB
	Albedo() geom.Vec3
	setID(int)
	getID() int
}

// Sphere is a diffuse sphere primitive.
type Sphere struct {
	Center geom.Vec3
	Radius float64
	Color  geom.Vec3
	id     int
}

func (s *Sphere) setID(id int) { s.id = id }

// ID returns the BVH-assigned primitive id.
func (s *Sphere) ID() int           { return s.id }
func (s *Sphere) getID() int        { return s.id }
func (s *Sphere) Albedo() geom.Vec3 { return s.Color }

func (s *Sphere) Intersect(r geom.Ray, tMin, tMax float64) (Hit, bool) {
	oc := r.Orig.Sub(s.Center)
	a := r.Dir.Len2()
	halfB := oc.Dot(r.Dir)
	c := oc.Len2() - s.Radius*s.Radius
	disc := halfB*halfB - a*c
	if disc < 0 {
		return Hit{}, false
	}
	sq := math.Sqrt(disc)
	t := (-halfB - sq) / a
	if t < tMin || t > tMax {
		t = (-halfB + sq) / a
		if t < tMin || t > tMax {
			return Hit{}, false
		}
	}
	p := r.At(t)
	n := p.Sub(s.Center).Scale(1 / s.Radius)
	if n.Dot(r.Dir) > 0 {
		n = n.Neg()
	}
	return Hit{T: t, Point: p, Normal: n, ID: s.id}, true
}

func (s *Sphere) Bounds() geom.AABB {
	r := geom.V(s.Radius, s.Radius, s.Radius)
	return geom.AABB{Min: s.Center.Sub(r), Max: s.Center.Add(r)}
}

// Triangle is a diffuse triangle primitive.
type Triangle struct {
	A, B, C geom.Vec3
	Color   geom.Vec3
	id      int
}

func (t *Triangle) setID(id int) { t.id = id }

// ID returns the BVH-assigned primitive id.
func (t *Triangle) ID() int           { return t.id }
func (t *Triangle) getID() int        { return t.id }
func (t *Triangle) Albedo() geom.Vec3 { return t.Color }

// Intersect uses the Möller–Trumbore algorithm.
func (t *Triangle) Intersect(r geom.Ray, tMin, tMax float64) (Hit, bool) {
	e1 := t.B.Sub(t.A)
	e2 := t.C.Sub(t.A)
	p := r.Dir.Cross(e2)
	det := e1.Dot(p)
	if math.Abs(det) < 1e-12 {
		return Hit{}, false
	}
	inv := 1 / det
	s := r.Orig.Sub(t.A)
	u := s.Dot(p) * inv
	if u < 0 || u > 1 {
		return Hit{}, false
	}
	q := s.Cross(e1)
	v := r.Dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return Hit{}, false
	}
	tt := e2.Dot(q) * inv
	if tt < tMin || tt > tMax {
		return Hit{}, false
	}
	n := e1.Cross(e2).Normalize()
	if n.Dot(r.Dir) > 0 {
		n = n.Neg()
	}
	return Hit{T: tt, Point: r.At(tt), Normal: n, ID: t.id}, true
}

func (t *Triangle) Bounds() geom.AABB {
	b := geom.EmptyAABB().Expand(t.A).Expand(t.B).Expand(t.C)
	// Give degenerate (zero-thickness) boxes a tiny padding so the
	// BVH box test never rejects them due to float rounding.
	pad := geom.V(1e-9, 1e-9, 1e-9)
	return geom.AABB{Min: b.Min.Sub(pad), Max: b.Max.Add(pad)}
}
