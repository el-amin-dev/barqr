package writer

import (
	"bytes"
	"errors"
	"testing"

	"github.com/HugoSmits86/nativewebp"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestWebPWriterIsRegisteredAsBinaryImage(t *testing.T) {
	t.Parallel()

	w, err := Get(WebP)
	if err != nil {
		t.Fatalf("Get(webp): %v", err)
	}
	if w.MIME() != "image/webp" || w.Extension() != "webp" || !w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestWebPWriteProducesADecodableWebP(t *testing.T) {
	t.Parallel()

	const scale = 4
	c := rasterQR(t, render.DefaultStyle())
	out, err := webpWriter{}.Write(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// RIFF container: "RIFF", a four-byte size, then the "WEBP" form type.
	if !bytes.HasPrefix(out, []byte("RIFF")) || !bytes.Equal(out[8:12], []byte("WEBP")) {
		t.Fatalf("container = %q, want a RIFF/WEBP header", out[:min(len(out), 12)])
	}

	img, err := nativewebp.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("nativewebp.Decode: %v", err)
	}
	if got := img.Bounds(); got.Dx() != c.Cols*scale || got.Dy() != c.Rows*scale {
		t.Fatalf("bounds = %v, want %dx%d", got, c.Cols*scale, c.Rows*scale)
	}

	// VP8L is lossless, so a module comes back exactly as it went in.
	q := c.QuietZone
	r, g, b, a := img.At(q*scale+scale/2, q*scale+scale/2).RGBA()
	if r != 0 || g != 0 || b != 0 || a != 0xFFFF {
		t.Errorf("finder pixel = %d,%d,%d,%d, want opaque black", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestWebPWriteRejectsAnImpossibleQuality(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	w := webpWriter{}

	if _, err := w.Write(c, OutputOpts{Scale: 2, Quality: 250}); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidOutput)
	}
	// The encoder is lossless, so an in-range quality is simply ignored.
	if _, err := w.Write(c, OutputOpts{Scale: 2, Quality: 40}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestWebPWriteFailsOnAnOversizeCanvas(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	_, err := webpWriter{}.Write(c, OutputOpts{Scale: 40, MaxPixels: 100})
	if !errors.Is(err, ErrCanvasTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrCanvasTooLarge)
	}
}
