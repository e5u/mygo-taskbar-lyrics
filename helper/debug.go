package main

import (
	"image"
	"image/png"
	"os"
)

// dumpPath is set by MYGO_TASKBAR_DUMP so one run can keep what it painted.
var dumpPath = os.Getenv("MYGO_TASKBAR_DUMP")

// dumpCanvas writes a premultiplied canvas as a PNG for offline checking.
func dumpCanvas(c *canvas, path string) {
	if c == nil || path == "" {
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(c.w), int(c.h)))
	for i := 0; i < int(c.w)*int(c.h); i++ {
		b, g, r, a := c.pix[i*4], c.pix[i*4+1], c.pix[i*4+2], c.pix[i*4+3]
		if a != 0 && a != 255 {
			b = byte(int(b) * 255 / int(a))
			g = byte(int(g) * 255 / int(a))
			r = byte(int(r) * 255 / int(a))
		}
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = a
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}
