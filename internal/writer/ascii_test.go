package writer

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

// decoration is one style a terminal writer must refuse, paired with the word
// its error has to name. A caller told only "unsupported" cannot work out which
// of three options to drop, so the message is part of the contract.
type decoration struct {
	name  string
	style render.Style
}

// decorations builds the three refusable styles.
//
// The table is shared by the ascii, unicode, ansi and json tests rather than
// copied into each: the refusal comes from one helper, so the cases it is
// judged on should come from one place too — and json is held to the mirror
// image of the same list.
func decorations(t *testing.T) []decoration {
	t.Helper()

	logo := render.DefaultStyle()
	img, err := render.ParseLogo(tinyLogoURI(t))
	if err != nil {
		t.Fatalf("ParseLogo: %v", err)
	}
	logo.Logo = &render.Logo{Image: img, Scale: render.DefaultLogoScale, Padding: 1}

	frame := render.DefaultStyle()
	frame.Frame = &render.Frame{
		Kind:  render.FrameBorder,
		Width: 3,
		Color: color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF},
	}

	caption := render.DefaultStyle()
	caption.Caption = "BARQR"

	return []decoration{
		{name: "logo", style: logo},
		{name: "frame", style: frame},
		{name: "caption", style: caption},
	}
}

// tinyLogoURI encodes a 2x2 PNG as a data URI, the only logo form ParseLogo
// accepts. The pixels are irrelevant — only the reserved geometry is under
// test — so the smallest legal image keeps the fixture honest and cheap.
func tinyLogoURI(t *testing.T) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// assertRefusesDecorations checks that w rejects every decorated canvas with
// ErrUnsupportedOutput and says which decoration it rejected.
func assertRefusesDecorations(t *testing.T, w Writer) {
	t.Helper()

	for _, d := range decorations(t) {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			c := rasterQR(t, d.style)
			_, err := w.Write(c, OutputOpts{})
			if !errors.Is(err, ErrUnsupportedOutput) {
				t.Fatalf("error = %v, want %v", err, ErrUnsupportedOutput)
			}
			if !strings.Contains(err.Error(), d.name) {
				t.Errorf("error %q does not name the %s it refused", err, d.name)
			}
		})
	}
}

// gradientStyle is the style the gradient tests share: a ramp with two stops
// far enough apart that a writer honouring it emits visibly different colours.
func gradientStyle() render.Style {
	s := render.DefaultStyle()
	s.Gradient = &render.Gradient{
		Kind:  render.GradientLinear,
		Angle: 90,
		Stops: []render.Stop{
			{Offset: 0, Color: color.NRGBA{R: 0xFF, A: 0xFF}},
			{Offset: 1, Color: color.NRGBA{B: 0xFF, A: 0xFF}},
		},
	}
	return s
}

func TestASCIIWritersAreRegisteredUnderBothNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ASCII, Text} {
		w, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if w.Name() != name {
			t.Errorf("Name() = %q, want %q", w.Name(), name)
		}
		if w.MIME() != "text/plain; charset=utf-8" || w.Extension() != "txt" || w.Binary() {
			t.Errorf("%s: mime=%q ext=%q binary=%v", name, w.MIME(), w.Extension(), w.Binary())
		}
	}
}

func TestASCIIWriteDrawsTwoColumnsPerModule(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := asciiWriter{format: ASCII}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := string(out)
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("output does not end with a newline")
	}

	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != c.Rows {
		t.Fatalf("%d lines, want %d", len(lines), c.Rows)
	}

	for y, line := range lines {
		if len(line) != 2*c.Cols {
			t.Fatalf("line %d is %d characters, want %d", y, len(line), 2*c.Cols)
		}
		for x := range c.Cols {
			cell := line[2*x : 2*x+2]
			want := "  "
			if c.At(x, y) {
				want = "##"
			}
			if cell != want {
				t.Fatalf("module (%d,%d) = %q, want %q", x, y, cell, want)
			}
		}
	}
}

func TestTextAliasMatchesASCIIByteForByte(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	first, err := asciiWriter{format: ASCII}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	second, err := asciiWriter{format: Text}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the txt alias produced different bytes from ascii")
	}
}

// textWriters is every writer that goes through checkCanvas: the four terminal
// formats and json.
func textWriters() []Writer {
	return []Writer{
		asciiWriter{format: ASCII},
		asciiWriter{format: Text},
		unicodeWriter{},
		ansiWriter{},
		jsonWriter{},
	}
}

func TestTextWritersRejectAnEmptyCanvas(t *testing.T) {
	t.Parallel()

	for _, w := range textWriters() {
		t.Run(w.Name(), func(t *testing.T) {
			t.Parallel()

			if _, err := w.Write(render.Canvas{}, OutputOpts{}); !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidOutput)
			}
		})
	}
}

func TestASCIIWritersRefuseDecoratedCanvases(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ASCII, Text} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertRefusesDecorations(t, asciiWriter{format: name})
		})
	}
}

// TestTextWritersAcceptAGradient pins the distinction that makes the refusal
// rule defensible rather than blanket.
//
// A logo, a frame and a caption are things a terminal cannot draw at all. A
// gradient is not: ansi honours it per module through Canvas.ColorAt, and
// ascii and unicode ignore colour by design, which is a documented property of
// a monochrome format and not an option quietly dropped on the floor.
func TestTextWritersAcceptAGradient(t *testing.T) {
	t.Parallel()

	for _, w := range textWriters() {
		t.Run(w.Name(), func(t *testing.T) {
			t.Parallel()

			c := rasterQR(t, gradientStyle())
			out, err := w.Write(c, OutputOpts{})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if len(out) == 0 {
				t.Error("a gradient canvas produced no output")
			}
		})
	}
}
