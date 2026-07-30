//go:build darwin

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

func makeTrayIcon() []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x >= 3 && x < size-3 && y >= 3 && y < size-3 {
				img.Set(x, y, color.NRGBA{R: 109, G: 40, B: 217, A: 255})
			}
			if x >= 8 && x < 24 && (y == 10 || y == 16 || y == 22) {
				img.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
