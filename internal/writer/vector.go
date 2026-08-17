package writer

// Geometry shared by the vector writers.
//
// PDF and EPS describe the same picture in two syntaxes: the same page size
// in points, the same merged rectangle runs, the same flattened colours. SVG
// shares the human-readable-text band constants. Keeping that arithmetic in
// one place is what stops the three formats drifting into producing subtly
// different output for one canvas.

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// vecPaper is the colour an unpainted page is assumed to be when a translucent
// ink has to be flattened onto it.
var vecPaper = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

// vecPage is a resolved physical page: overall size and one module, all in
// PostScript points. Both PDF and EPS put the origin at the bottom-left, so
// the helpers on it also flip the canvas's downward row order.
type vecPage struct {
	// W and H are the page size in points, including the human-readable band.
	W, H float64
	// Module is the side of one module in points.
	Module float64
}

// rect converts a run of dark modules into a rectangle in page coordinates.
func (p vecPage) rect(r vecRun) (x, y, w, h float64) {
	// Canvas rows run downwards from the top; page coordinates run upwards
	// from the bottom, so row r.Y sits one module below the top edge times
	// r.Y+1.
	return float64(r.X) * p.Module,
		p.H - float64(r.Y+1)*p.Module,
		float64(r.Len) * p.Module,
		p.Module
}

// baseline is the text baseline for the human-readable line, in points.
func (p vecPage) baseline(c render.Canvas) float64 {
	return p.H - (float64(c.Rows)+hriFontModules)*p.Module
}

// vecPageSize resolves the physical page for a vector output.
//
// A millimetre or inch size is honoured exactly. That is the whole point of
// the vector path: a rasteriser has to round to a whole number of pixels per
// module and so cannot hit 30mm on the nose, whereas a PDF placed on a label
// template must. PixelSize is still consulted, because it is the single place
// MaxPixels is enforced and a request too large to raster must fail the same
// way here rather than quietly succeed.
func vecPageSize(c render.Canvas, o OutputOpts) (vecPage, error) {
	w, _, _, err := PixelSize(c, o)
	if err != nil {
		return vecPage{}, err
	}

	dpi := o.DPI
	if dpi <= 0 {
		dpi = 300
	}

	var width float64
	switch {
	case o.Size > 0 && o.Unit == UnitMM:
		width = o.Size / 25.4 * pointsPerInch
	case o.Size > 0 && o.Unit == UnitIn:
		width = o.Size * pointsPerInch
	default:
		// A pixel size only becomes physical through DPI; the resolved pixel
		// width is used so that scale and MaxPixels are reflected in the page.
		width = float64(w) / float64(dpi) * pointsPerInch
	}

	module := width / float64(c.Cols)
	rows := float64(c.Rows)
	if c.HRI != "" {
		rows += hriBandModules
	}
	return vecPage{W: width, H: module * rows, Module: module}, nil
}

// vecRun is a horizontal stretch of dark modules of one colour within a row.
type vecRun struct {
	X, Y, Len int
	Color     color.NRGBA
}

// vecRuns merges horizontally adjacent dark modules into single runs.
//
// The vector writers draw one rectangle per run instead of one per module.
// Runs are roughly a third of the module count on a typical QR and far fewer
// on a linear code, where a wide bar is a single rectangle, so the content
// stream and the EPS body shrink in proportion. A run is broken by a colour
// change so that a distinctly coloured finder pattern still comes out right.
func vecRuns(c render.Canvas) []vecRun {
	runs := make([]vecRun, 0, c.Cols*c.Rows/8+1)

	for y := range c.Rows {
		for x := 0; x < c.Cols; {
			if !c.At(x, y) {
				x++
				continue
			}
			ink := c.ColorAt(x, y)
			n := 1
			for x+n < c.Cols && c.At(x+n, y) && c.ColorAt(x+n, y) == ink {
				n++
			}
			runs = append(runs, vecRun{X: x, Y: y, Len: n, Color: ink})
			x += n
		}
	}
	return runs
}

// vecOver flattens a translucent colour onto the surface behind it.
//
// Neither PDF nor EPS expresses per-object alpha without a soft mask or an
// extended graphics state, and both are more machinery than a code is worth.
// Print output lands on opaque paper anyway, so compositing now produces the
// same ink for a fraction of the file — and, unlike a transparency group, it
// renders identically in every reader.
func vecOver(fg, bg color.NRGBA) color.NRGBA {
	if fg.A == 0xFF {
		return fg
	}
	if bg.A != 0xFF {
		bg = vecPaper
	}

	a := float64(fg.A) / 255
	blend := func(f, b uint8) uint8 {
		return uint8(math.Round(float64(f)*a + float64(b)*(1-a)))
	}
	return color.NRGBA{
		R: blend(fg.R, bg.R),
		G: blend(fg.G, bg.G),
		B: blend(fg.B, bg.B),
		A: 0xFF,
	}
}

// vecNum formats a point measurement. Three decimals is a thousandth of a
// point, some forty times finer than any imagesetter resolves, and it keeps
// the content stream free of sixteen-digit float expansions.
func vecNum(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}

// vecRGB formats a colour as the three 0..1 components that both `rg` (PDF)
// and `setrgbcolor` (PostScript) expect.
func vecRGB(c color.NRGBA) string {
	return vecNum(float64(c.R)/255) + " " +
		vecNum(float64(c.G)/255) + " " +
		vecNum(float64(c.B)/255)
}

// vecString renders a literal string for PDF or PostScript, which share the
// same syntax.
//
// The parenthesis and backslash escapes are the security-relevant part: an
// unescaped ')' in the human-readable text would close the string early and
// let the rest of the payload be read as operators. Everything outside
// printable ASCII is written as an octal escape, which keeps the file
// seven-bit clean and sidesteps the WinAnsi encoding entirely.
func vecString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('(')

	for i := range len(s) {
		switch ch := s[i]; {
		case ch == '(' || ch == ')' || ch == '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case ch < 0x20 || ch > 0x7E:
			fmt.Fprintf(&b, "\\%03o", ch)
		default:
			b.WriteByte(ch)
		}
	}

	b.WriteByte(')')
	return b.String()
}
