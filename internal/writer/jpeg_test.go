package writer

import (
	"bytes"
	"errors"
	"image/jpeg"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestJPEGWriterIsRegisteredAsBinaryImage(t *testing.T) {
	t.Parallel()

	w, err := Get(JPEG)
	if err != nil {
		t.Fatalf("Get(jpeg): %v", err)
	}
	if w.MIME() != "image/jpeg" || w.Extension() != "jpg" || !w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestJPEGWriteProducesADecodableJPEG(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := jpegWriter{}.Write(c, OutputOpts{Scale: 4})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if magic := []byte{0xFF, 0xD8, 0xFF}; !bytes.HasPrefix(out, magic) {
		t.Fatalf("magic = % x, want % x", out[:min(len(out), 3)], magic)
	}

	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	if got := img.Bounds(); got.Dx() != c.Cols*4 || got.Dy() != c.Rows*4 {
		t.Errorf("bounds = %v, want %dx%d", got, c.Cols*4, c.Rows*4)
	}
}

func TestJPEGWriteValidatesQuality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		quality int
		wantErr bool
	}{
		{"unset uses the default", 0, false},
		{"lowest", 1, false},
		{"highest", 100, false},
		{"negative", -1, true},
		{"above the range", 101, true},
	}

	c := rasterQR(t, render.DefaultStyle())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := jpegWriter{}.Write(c, OutputOpts{Scale: 2, Quality: tt.quality})
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidOutput) {
					t.Fatalf("error = %v, want %v", err, ErrInvalidOutput)
				}
				return
			}
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
		})
	}
}

func TestJPEGWriteCompositesAwayTransparency(t *testing.T) {
	t.Parallel()

	// Without compositing, image/jpeg reads a fully transparent pixel as
	// premultiplied black and the quiet zone comes out as a black border.
	s := render.DefaultStyle()
	s.BG = render.Transparent
	c := rasterQR(t, s)

	out, err := jpegWriter{}.Write(c, OutputOpts{Scale: 4, Quality: 100})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}

	r, g, b, _ := img.At(2, 2).RGBA()
	const nearlyWhite = 0xF000
	if r < nearlyWhite || g < nearlyWhite || b < nearlyWhite {
		t.Errorf("quiet zone = %d,%d,%d, want near white", r>>8, g>>8, b>>8)
	}
}

func TestJPEGWriteKeepsAnOpaqueBackgroundColour(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.BG = render.Transparent
	s.BG.R, s.BG.A = 0xFF, 0xFF // opaque red
	c := rasterQR(t, s)

	out, err := jpegWriter{}.Write(c, OutputOpts{Scale: 6, Quality: 100})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}

	// JPEG is lossy, so this asserts the hue rather than an exact value.
	r, g, b, _ := img.At(4, 4).RGBA()
	if r < 0xE000 || g > 0x2000 || b > 0x2000 {
		t.Errorf("quiet zone = %d,%d,%d, want red", r>>8, g>>8, b>>8)
	}
}

func TestJPEGWriteFailsOnAnOversizeCanvas(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	_, err := jpegWriter{}.Write(c, OutputOpts{Scale: 40, MaxPixels: 100})
	if !errors.Is(err, ErrCanvasTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrCanvasTooLarge)
	}
}
