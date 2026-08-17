package writer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/png"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestPNGWriterIsRegisteredAsBinaryImage(t *testing.T) {
	t.Parallel()

	w, err := Get(PNG)
	if err != nil {
		t.Fatalf("Get(png): %v", err)
	}
	if w.MIME() != "image/png" || w.Extension() != "png" || !w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestPNGWriteProducesADecodablePNG(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := pngWriter{}.Write(c, OutputOpts{Scale: 4, DPI: 300})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !bytes.HasPrefix(out, pngSignature) {
		t.Fatalf("magic = % x, want % x", out[:min(len(out), 8)], pngSignature)
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got := img.Bounds(); got.Dx() != c.Cols*4 || got.Dy() != c.Rows*4 {
		t.Errorf("bounds = %v, want %dx%d", got, c.Cols*4, c.Rows*4)
	}
}

func TestPNGWriteRecordsPhysicalResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dpi  int
		ppm  uint32
	}{
		{"screen", 72, 2835},
		{"print", 300, 11811},
		{"unset falls back to 300", 0, 11811},
	}

	c := rasterQR(t, render.DefaultStyle())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := pngWriter{}.Write(c, OutputOpts{Scale: 2, DPI: tt.dpi})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			data := chunkData(t, out, "pHYs")
			if len(data) != 9 {
				t.Fatalf("pHYs is %d bytes, want 9", len(data))
			}
			x := binary.BigEndian.Uint32(data[0:4])
			y := binary.BigEndian.Uint32(data[4:8])
			if x != tt.ppm || y != tt.ppm {
				t.Errorf("resolution = %d x %d, want %d", x, y, tt.ppm)
			}
			if data[8] != 1 {
				t.Errorf("unit specifier = %d, want 1 (metre)", data[8])
			}
		})
	}
}

func TestWithPhysLeavesAnythingItCannotParseAlone(t *testing.T) {
	t.Parallel()

	good := append(append([]byte{}, pngSignature...), 0, 0, 0, 13)
	good = append(good, "IHDR"...)

	tests := []struct {
		name string
		raw  []byte
		dpi  int
	}{
		{"not a png", []byte("this is not an image at all"), 300},
		{"truncated", pngSignature, 300},
		{"first chunk is not IHDR", append(append(append([]byte{}, pngSignature...),
			0, 0, 0, 13), "IDAT"...), 300},
		{"header longer than the file", good, 300},
		{"absurd dpi", nil, 1 << 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := tt.raw
			if raw == nil {
				var err error
				raw, err = pngWriter{}.Write(rasterQR(t, render.DefaultStyle()), OutputOpts{Scale: 1})
				if err != nil {
					t.Fatalf("Write: %v", err)
				}
				// A DPI this large cannot be recorded, so the only difference
				// from the encoder's own bytes must be the missing chunk.
				raw = bytes.Replace(raw, chunkOf(t, raw, "pHYs"), nil, 1)
			}
			if got := withPhys(raw, tt.dpi); !bytes.Equal(got, raw) {
				t.Errorf("input of %d bytes became %d bytes", len(raw), len(got))
			}
		})
	}
}

func TestPNGWriteFailsOnAnOversizeCanvas(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	_, err := pngWriter{}.Write(c, OutputOpts{Scale: 40, MaxPixels: 100})
	if !errors.Is(err, ErrCanvasTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrCanvasTooLarge)
	}
}

// chunkOf returns a whole PNG chunk, length and CRC included.
func chunkOf(t *testing.T, raw []byte, typ string) []byte {
	t.Helper()

	for at := len(pngSignature); at+8 <= len(raw); {
		n := int(binary.BigEndian.Uint32(raw[at : at+4]))
		end := at + 12 + n
		if end > len(raw) {
			break
		}
		if string(raw[at+4:at+8]) == typ {
			return raw[at:end]
		}
		at = end
	}
	t.Fatalf("no %s chunk in %d bytes", typ, len(raw))
	return nil
}

// chunkData returns the payload of a PNG chunk.
func chunkData(t *testing.T, raw []byte, typ string) []byte {
	t.Helper()
	c := chunkOf(t, raw, typ)
	return c[8 : len(c)-4]
}
