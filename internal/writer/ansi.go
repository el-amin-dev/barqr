package writer

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// ANSI is the registry name of the true-colour terminal writer.
const ANSI = "ansi"

// ansiReset returns the terminal to its own colours. Every line ends with it,
// so an interrupted stream cannot leave the terminal painted white.
const ansiReset = "\x1b[0m"

// ansiDefaultBG selects the terminal's own background, which is how a
// transparent canvas background is honoured.
const ansiDefaultBG = "\x1b[49m"

func init() { Register(ansiWriter{}) }

// ansiWriter draws the canvas as coloured terminal cells.
//
// This is the only text form that scans reliably off a screen. It paints the
// background of each cell rather than a glyph, so the code carries its own
// light and dark values instead of inheriting the reader's colour scheme —
// which is what makes the ascii and unicode forms a coin flip on a dark
// terminal. Two cells per module keep it square, as in the ascii writer.
type ansiWriter struct{}

func (ansiWriter) Name() string      { return ANSI }
func (ansiWriter) MIME() string      { return "text/plain; charset=utf-8" }
func (ansiWriter) Extension() string { return "txt" }
func (ansiWriter) Binary() bool      { return false }

func (ansiWriter) Write(c render.Canvas, _ OutputOpts) ([]byte, error) {
	if err := checkTextCanvas(c); err != nil {
		return nil, err
	}

	var b strings.Builder
	// Two spaces per module plus, in the worst case, one escape each.
	b.Grow(c.Rows * (c.Cols*(2+len(ansiDefaultBG)) + len(ansiReset) + 1))

	for y := range c.Rows {
		// A run of same-coloured modules shares one escape. A quiet zone is
		// one run, which is most of the bytes in a small code.
		current := ""
		for x := range c.Cols {
			col := c.Style.BG
			if c.At(x, y) {
				col = c.ColorAt(x, y)
			}
			if esc := ansiBackground(col); esc != current {
				b.WriteString(esc)
				current = esc
			}
			b.WriteString("  ")
		}
		b.WriteString(ansiReset)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// ansiBackground is the escape that sets the cell background to a colour.
//
// A terminal has no alpha, so a translucent colour is composited over white
// before being emitted: white is what a scanner assumes behind a code, and
// guessing the terminal's real background would be worse than assuming the
// one the code was designed for.
func ansiBackground(c color.NRGBA) string {
	if c.A == 0 {
		return ansiDefaultBG
	}
	r, g, b := overWhite(c)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// overWhite composites a straight-alpha colour onto white.
func overWhite(c color.NRGBA) (r, g, b uint8) {
	if c.A == 0xFF {
		return c.R, c.G, c.B
	}
	blend := func(v uint8) uint8 {
		// Straight alpha over white, rounded: v*a + 255*(1-a). The quotient
		// cannot exceed 255 — both terms are scaled by the same 255 — and the
		// mask says so to anything reading this without doing the algebra.
		mix := (uint32(v)*uint32(c.A) + 0xFF*(0xFF-uint32(c.A)) + 0x7F) / 0xFF
		return uint8(mix & 0xFF)
	}
	return blend(c.R), blend(c.G), blend(c.B)
}
