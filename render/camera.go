package render

import (
	"math"

	"github.com/shikejing/environment-lighting-renderer-go/geom"
)

// camera is a pinhole camera.
type camera struct {
	origin, lowerLeft geom.Vec3
	horiz, vert       geom.Vec3
}

// defaultCamera frames the procedural scene at the origin.
// FOV is vertical, in degrees.
func newCamera(lookFrom, lookAt geom.Vec3, vfovDeg, aspect float64) camera {
	if vfovDeg <= 0 {
		vfovDeg = 50
	}
	theta := vfovDeg * math.Pi / 180
	h := math.Tan(theta / 2)
	vh := 2 * h
	vw := aspect * vh
	up := geom.V(0, 1, 0)
	w := lookFrom.Sub(lookAt).Normalize()
	u := up.Cross(w).Normalize()
	v := w.Cross(u)
	return camera{
		origin:    lookFrom,
		horiz:     u.Scale(vw),
		vert:      v.Scale(vh),
		lowerLeft: lookFrom.Sub(u.Scale(vw / 2)).Sub(v.Scale(vh / 2)).Sub(w),
	}
}

// ray returns the camera ray through normalized coords (u, v) in [0,1].
func (c camera) ray(u, v float64) geom.Ray {
	dir := c.lowerLeft.Add(c.horiz.Scale(u)).Add(c.vert.Scale(v)).Sub(c.origin)
	return geom.Ray{Orig: c.origin, Dir: dir.Normalize()}
}
