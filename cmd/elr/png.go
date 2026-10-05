package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"github.com/shikejing/environment-lighting-renderer-go/internal/imageio"
)

func writePNG(path string, w, h int, pix [][4]uint8) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := pix[y*w+x]
			img.SetRGBA(x, y, color.RGBA{R: p[0], G: p[1], B: p[2], A: p[3]})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return imageio.WriteFileAtomic(path, buf.Bytes())
}
