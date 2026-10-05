package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/shikejing/environment-lighting-renderer-go/internal/geom"
	"github.com/shikejing/environment-lighting-renderer-go/internal/imageio"
)

// camera is a deterministic pinhole camera looking at the origin.
type camera struct {
	origin, lowerLeft, horiz, vert geom.Vec3
	w, h                           int
}

func defaultCamera(w, h int) camera {
	origin := geom.Vec3{X: 0, Y: 7, Z: 16}
	lookAt := geom.Vec3{X: 0, Y: 1, Z: 0}
	up := geom.Vec3{X: 0, Y: 1, Z: 0}
	fwd := geom.Norm(geom.Sub(lookAt, origin))
	right := geom.Norm(geom.Cross(fwd, up))
	trueUp := geom.Cross(right, fwd)
	const fov = 0.9 // ~51.6 degrees half-height tangent scale
	aspect := float64(w) / float64(h)
	halfH := fov
	halfW := aspect * halfH
	center := geom.Add(origin, fwd)
	return camera{
		origin:    origin,
		lowerLeft: geom.Sub(geom.Sub(center, geom.Scale(right, halfW)), geom.Scale(trueUp, halfH)),
		horiz:     geom.Scale(right, 2*halfW),
		vert:      geom.Scale(trueUp, 2*halfH),
		w:         w,
		h:         h,
	}
}

// ray returns the camera ray through pixel (x, y) with sub-pixel jitter
// (jx, jy) in [0,1).
func (c camera) ray(x, y int, jx, jy float64) geom.Ray {
	u := (float64(x) + jx) / float64(c.w)
	v := 1 - (float64(y)+jy)/float64(c.h)
	p := geom.Add(c.lowerLeft, geom.Add(geom.Scale(c.horiz, u), geom.Scale(c.vert, v)))
	return geom.Ray{Orig: c.origin, Dir: geom.Sub(p, c.origin)}
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func writePNG(path string, w, h int, pix [][4]uint8) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := pix[y*w+x]
			img.SetRGBA(x, y, color.RGBA{R: p[0], G: p[1], B: p[2], A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return imageio.WriteFileAtomic(path, buf.Bytes())
}
