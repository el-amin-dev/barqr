package writer

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
)

func TestJSONWriterIsRegisteredAsText(t *testing.T) {
	t.Parallel()

	w, err := Get(JSON)
	if err != nil {
		t.Fatalf("Get(json): %v", err)
	}
	if w.MIME() != "application/json" || w.Extension() != "json" || w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestJSONWriteDescribesTheCanvas(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("output does not end with a newline")
	}

	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Symbology != encoder.QR || got.Kind != string(encoder.Kind2D) {
		t.Errorf("symbology/kind = %q/%q, want %q/%q",
			got.Symbology, got.Kind, encoder.QR, encoder.Kind2D)
	}
	if got.Cols != c.Cols || got.Rows != c.Rows || got.QuietZone != c.QuietZone {
		t.Errorf("geometry = %dx%d quiet %d, want %dx%d quiet %d",
			got.Cols, got.Rows, got.QuietZone, c.Cols, c.Rows, c.QuietZone)
	}
	if got.FG != "#000000" || got.BG != "#ffffff" {
		t.Errorf("colours = %q on %q, want #000000 on #ffffff", got.FG, got.BG)
	}
	if got.HRI != "" {
		t.Errorf("hri = %q, want empty for a QR symbol", got.HRI)
	}

	if len(got.Modules) != c.Rows {
		t.Fatalf("%d module rows, want %d", len(got.Modules), c.Rows)
	}
	for y, row := range got.Modules {
		if len(row) != c.Cols {
			t.Fatalf("row %d is %d characters, want %d", y, len(row), c.Cols)
		}
		for x := range c.Cols {
			want := byte('0')
			if c.At(x, y) {
				want = '1'
			}
			if row[x] != want {
				t.Fatalf("module (%d,%d) = %q, want %q", x, y, row[x], want)
			}
		}
	}
}

func TestJSONWriteCarriesTheHumanReadableText(t *testing.T) {
	t.Parallel()

	c := rasterLinear(t, render.DefaultStyle(), "1234567890")
	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HRI != "1234567890" {
		t.Errorf("hri = %q, want 1234567890", got.HRI)
	}
	if got.Kind != string(encoder.Kind1D) {
		t.Errorf("kind = %q, want %q", got.Kind, encoder.Kind1D)
	}
}

func TestJSONWriteRecordsATranslucentColourWithItsAlpha(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.BG = render.Transparent
	c := rasterQR(t, s)

	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BG != "#00000000" {
		t.Errorf("bg = %q, want #00000000", got.BG)
	}
}
