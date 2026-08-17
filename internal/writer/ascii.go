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

// checkTextCanvas rejects a canvas the text writers cannot draw. They do not
// go through PixelSize, so this is where the empty-canvas guard lives.
func checkTextCanvas(c render.Canvas) error {
	if c.Cols <= 0 || c.Rows <= 0 {
		return fmt.Errorf("%w: empty canvas", ErrInvalidOutput)
	}
	return nil
}
