//go:build windows

package tray

import "encoding/binary"

func makeTrayIcon() []byte {
	const width = 16
	const height = 16
	const xorSize = width * height * 4
	const maskStride = 4
	const maskSize = maskStride * height
	const dibSize = 40 + xorSize + maskSize
	const fileSize = 6 + 16 + dibSize

	data := make([]byte, fileSize)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 1)
	data[6], data[7] = width, height
	binary.LittleEndian.PutUint16(data[10:12], 1)
	binary.LittleEndian.PutUint16(data[12:14], 32)
	binary.LittleEndian.PutUint32(data[14:18], dibSize)
	binary.LittleEndian.PutUint32(data[18:22], 22)

	dib := data[22:]
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], width)
	binary.LittleEndian.PutUint32(dib[8:12], height*2)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[20:24], xorSize)

	pixels := dib[40 : 40+xorSize]
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := ((height-1-y)*width + x) * 4
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = 217, 40, 109, 255
			if x > 3 && x < 12 && (y == 4 || y == 8 || y == 11) {
				pixels[i], pixels[i+1], pixels[i+2] = 255, 255, 255
			}
		}
	}
	return data
}
