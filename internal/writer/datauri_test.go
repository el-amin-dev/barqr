package writer

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestDataURIWriterIsRegisteredAsText(t *testing.T) {
	t.Parallel()

	w, err := Get(DataURI)
	if err != nil {
		t.Fatalf("Get(datauri): %v", err)
	}
	// The payload is binary but the output is a URI, which is text: this is
	// what stops the HTTP layer base64-ing it a second time.
	if w.MIME() != "text/plain; charset=utf-8" || w.Extension() != "txt" || w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestDataURIWriteWrapsAPNG(t *testing.T) {
	t.Parallel()

	const scale = 3
	c := rasterQR(t, render.DefaultStyle())
	out, err := dataURIWriter{}.Write(c, OutputOpts{Scale: scale, Format: DataURI})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	const prefix = "data:image/png;base64,"
	uri := string(out)
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("prefix = %q, want %q", uri[:min(len(uri), len(prefix))], prefix)
	}
	if strings.ContainsAny(uri, "\n\r") {
		t.Error("a data URI must be a single line")
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if !bytes.HasPrefix(raw, pngSignature) {
		t.Fatal("payload is not a PNG")
	}

	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got := img.Bounds(); got.Dx() != c.Cols*scale {
		t.Errorf("width = %d, want %d: sizing options did not reach the inner writer",
			got.Dx(), c.Cols*scale)
	}
}

func TestDataURIWriteReportsTheInnerFailure(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	_, err := dataURIWriter{}.Write(c, OutputOpts{Scale: 40, MaxPixels: 100})
	if !errors.Is(err, ErrCanvasTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrCanvasTooLarge)
	}
}
