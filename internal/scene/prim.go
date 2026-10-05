// Package scene holds scene primitives, a BVH with incremental updates,
// and procedural scene generation.
package scene

import (
	"math"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
)

// Sphere is a scene primitive.
type Sphere struct {
	Center geom.Vec3
	Radius float64
	// Albedo is the surface reflectance used to weight environment light.
	Albedo geom.Vec3
}

// ID is a stable handle to a primitive in the Scene.
type ID uint64

// Bounds returns the world-space AABB of the sphere.
func (s Sphere) Bounds() geom.AABB {
	r := geom.Vec3{X: s.Radius, Y: s.Radius, Z: s.Radius}
	return geom.AABB{Min: geom.Sub(s.Center, r), Max: geom.Add(s.Center, r)}
}

// intersect solves the ray-sphere quadratic; returns t and ok.
// t is the nearest root in (tmin, tmax).
func (s Sphere) intersect(r geom.Ray, tmin, tmax float64) (float64, bool) {
	oc := geom.Sub(r.Orig, s.Center)
	a := geom.Dot(r.Dir, r.Dir)
	halfB := geom.Dot(oc, r.Dir)
	c := geom.Dot(oc, oc) - s.Radius*s.Radius
	disc := halfB*halfB - a*c
	if disc < 0 {
		return 0, false
	}
	sq := math.Sqrt(disc)
	t := (-halfB - sq) / a
	if t <= tmin || t >= tmax {
		t = (-halfB + sq) / a
		if t <= tmin || t >= tmax {
			return 0, false
		}
	}
	return t, true
}
