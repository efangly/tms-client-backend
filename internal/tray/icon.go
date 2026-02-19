package tray

import (
	"bytes"
	"encoding/binary"
)

// generateIcon creates a minimal 16x16 ICO file bytes for the system tray icon.
// The icon is a green square representing "server running".
func generateIcon(r, g, b byte) []byte {
	const (
		width  = 16
		height = 16
	)

	buf := new(bytes.Buffer)

	// ICO Header
	binary.Write(buf, binary.LittleEndian, uint16(0)) // Reserved
	binary.Write(buf, binary.LittleEndian, uint16(1)) // Type: ICO
	binary.Write(buf, binary.LittleEndian, uint16(1)) // Count: 1 image

	// Calculate sizes
	bmpInfoSize := uint32(40)
	pixelDataSize := uint32(width * height * 4)                // BGRA
	andMaskRowSize := uint32(((width + 31) / 32) * 4)          // Padded to 4 bytes
	andMaskSize := andMaskRowSize * height                     //
	imageDataSize := bmpInfoSize + pixelDataSize + andMaskSize //
	dataOffset := uint32(6 + 16)                               // header(6) + entry(16)

	// ICO Directory Entry
	buf.WriteByte(byte(width))                            // Width
	buf.WriteByte(byte(height))                           // Height
	buf.WriteByte(0)                                      // Color count (0 = no palette)
	buf.WriteByte(0)                                      // Reserved
	binary.Write(buf, binary.LittleEndian, uint16(1))     // Color planes
	binary.Write(buf, binary.LittleEndian, uint16(32))    // Bits per pixel
	binary.Write(buf, binary.LittleEndian, imageDataSize) // Image data size
	binary.Write(buf, binary.LittleEndian, dataOffset)    // Offset to image data

	// BMP Info Header (BITMAPINFOHEADER)
	binary.Write(buf, binary.LittleEndian, bmpInfoSize)     // Header size
	binary.Write(buf, binary.LittleEndian, int32(width))    // Width
	binary.Write(buf, binary.LittleEndian, int32(height*2)) // Height (doubled for ICO)
	binary.Write(buf, binary.LittleEndian, uint16(1))       // Planes
	binary.Write(buf, binary.LittleEndian, uint16(32))      // Bit count
	binary.Write(buf, binary.LittleEndian, uint32(0))       // Compression
	binary.Write(buf, binary.LittleEndian, uint32(0))       // Image size
	binary.Write(buf, binary.LittleEndian, int32(0))        // X pixels per meter
	binary.Write(buf, binary.LittleEndian, int32(0))        // Y pixels per meter
	binary.Write(buf, binary.LittleEndian, uint32(0))       // Colors used
	binary.Write(buf, binary.LittleEndian, uint32(0))       // Colors important

	// Pixel data (BGRA format, bottom-up row order)
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			if x <= 1 || x >= width-2 || y <= 1 || y >= height-2 {
				// Border - darker shade
				buf.Write([]byte{b / 2, g / 2, r / 2, 0xFF})
			} else {
				// Fill color
				buf.Write([]byte{b, g, r, 0xFF})
			}
		}
	}

	// AND mask (all 0x00 = fully opaque)
	for y := 0; y < height; y++ {
		for x := uint32(0); x < andMaskRowSize; x++ {
			buf.WriteByte(0x00)
		}
	}

	return buf.Bytes()
}

// GreenIcon returns a green tray icon (server running)
func GreenIcon() []byte {
	return generateIcon(0x33, 0xCC, 0x33)
}

// RedIcon returns a red tray icon (server error)
func RedIcon() []byte {
	return generateIcon(0xCC, 0x33, 0x33)
}
