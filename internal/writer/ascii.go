package writer

import (
	"fmt"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// Registry names of the plain-text writers. Both produce identical bytes;
// "txt" exists because that is what people type when they want a file and
// "ascii" because that is what they type when they want to look at it.
const (
	// ASCII is the registry name of the ASCII-art writer.
	ASCII = "ascii"
	// Text is the registry name of its alias.
	Text = "txt"
)

func init() {
	Register(asciiWriter{format: ASCII})
	Register(asciiWriter{format: Text})
}

// asciiWriter draws the canvas with two characters per module.
//
// Two characters, not one: a terminal cell is about twice as tall as it is
// wide, so a single character per module produces a code squashed to half its
// width, which no scanner will read off a screen. Doubling the columns makes
// the modules square again.
//
// The polarity is literal — a dark module is drawn as ink — which is right on
// a light terminal and inverted on a dark one. Use the ansi writer when the
// output has to scan regardless of the reader's colour scheme.
type asciiWriter struct {
	// format is the registry name this instance was registered under, which is
	// the only difference between the two.
	format string
}

func (w asciiWriter) Name() string    { return w.format }
func (asciiWriter) MIME() string      { return "text/plain; charset=utf-8" }
func (asciiWriter) Extension() string { return "txt" }
func (asciiWriter) Binary() bool      { return false }

func (asciiWriter) Write(c render.Canvas, _ OutputOpts) ([]byte, error) {
	if err := checkTextCanvas(c); err != nil {
		return nil, err
	}

	const dark, light = "##", "  "

	var b strings.Builder
	b.Grow(c.Rows * (2*c.Cols + 1))
	for y := range c.Rows {
		for x := range c.Cols {
			if c.At(x, y) {
				b.WriteString(dark)
			} else {
				b.WriteString(light)
			}
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// checkCanvas rejects a canvas no writer can serialise.
//
// There is exactly one such case: a canvas with no modules, which is a zero
// Canvas or a render that never happened. It lives here, and every text writer
// reaches it — the terminal formats through checkTextCanvas, `json` directly —
// so the rule has one home rather than a copy per format.
func checkCanvas(c render.Canvas) error {
	if c.Cols <= 0 || c.Rows <= 0 {
		return fmt.Errorf("%w: empty canvas", ErrInvalidOutput)
	}
	return nil
}

// checkTextCanvas rejects a canvas the TERMINAL writers cannot draw: `ascii`,
// `txt`, `unicode` and `ansi`.
//
// A logo, a frame, or a caption is decoration the renderer has already
// reserved space for in Cols and Rows. A terminal writer has no way to draw any
// of them, so it would emit that reservation as blank rows and columns around
// the code — a wider, uglier output with nothing in the space, and no hint that
// anything was dropped. Refusing is the honest answer, and ErrUnsupportedOutput
// exists for exactly this.
//
// `json` is deliberately not covered. It does not draw the decorations, it
// describes their geometry, so it can represent all three faithfully and goes
// through checkCanvas alone.
//
// A gradient is deliberately NOT refused. `ansi` honours it per module through
// Canvas.ColorAt, and `ascii`/`unicode` ignore colour entirely — which is a
// documented property of a monochrome format, not a silently dropped option.
func checkTextCanvas(c render.Canvas) error {
	if err := checkCanvas(c); err != nil {
		return err
	}

	if _, ok := c.LogoRect(); ok {
		return fmt.Errorf("%w: a terminal format cannot draw a logo", ErrUnsupportedOutput)
	}
	if _, ok := c.FrameRect(); ok {
		return fmt.Errorf("%w: a terminal format cannot draw a frame", ErrUnsupportedOutput)
	}
	if c.Caption() != "" {
		return fmt.Errorf("%w: a terminal format cannot draw a caption", ErrUnsupportedOutput)
	}
	return nil
}
