package writer

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// FormatSVG is the registry name of the SVG writer.
const FormatSVG = "svg"

// svgEyeModules is the side of a QR finder pattern in modules and
// svgEyeBallOffset the inset of its solid centre. Both are fixed by the QR
// specification and are repeated here because render does not export them.
const (
	svgEyeModules    = 7
	svgEyeBallOffset = 2
)

func init() { Register(svgWriter{}) }

// svgWriter emits a hand-built SVG document.
//
// Nothing here goes through a templating or XML-building library. An SVG for a
// code is a fixed skeleton with two variable parts — a very long path and a
// handful of numbers — so a template engine would cost more than it saves, and
// the one place untrusted text appears (the human-readable line) is escaped
// through encoding/xml rather than by hand.
type svgWriter struct{}

func (svgWriter) Name() string      { return FormatSVG }
func (svgWriter) MIME() string      { return "image/svg+xml" }
func (svgWriter) Extension() string { return "svg" }
func (svgWriter) Binary() bool      { return false }

func (svgWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	w, h, scale, err := PixelSize(c, o)
	if err != nil {
		return nil, err
	}

	modules, err := render.ModuleShape(c.Style.Module)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}

	// The viewBox is in module units so the whole document scales by changing
	// two attributes, but width and height are still written in pixels: an
	// <img src="...svg"> with no intrinsic size renders at 300x150 in every
	// browser, and a code stretched to 2:1 does not scan.
	viewRows := c.Rows
	if c.HRI != "" {
		viewRows += hriBandModules
		h += hriBandModules * scale
	}

	var b bytes.Buffer
	// Roughly one path fragment per dark module, plus the fixed skeleton.
	b.Grow(512 + c.Cols*c.Rows*3)

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	// shape-rendering=crispEdges disables anti-aliasing on the module edges.
	// A half-lit boundary pixel is one of the commonest causes of a code that
	// looks correct on screen and fails to scan at small sizes.
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
			`viewBox="0 0 %d %d" shape-rendering="crispEdges">`+"\n",
		w, h, c.Cols, viewRows)

	if c.Style.BG.A != 0 {
		fmt.Fprintf(&b, `<rect width="%d" height="%d" %s/>`+"\n",
			c.Cols, viewRows, svgFill(c.Style.BG))
	}

	eyes := svgEyeOrigins(c)
	svgModulePath(&b, c, modules, eyes != nil)
	if err := svgEyePaths(&b, c, eyes); err != nil {
		return nil, err
	}
	if err := svgHRI(&b, c); err != nil {
		return nil, err
	}

	b.WriteString("</svg>\n")
	return b.Bytes(), nil
}

// svgModulePath writes every dark data module as a single <path>.
//
// One path for a few thousand modules rather than one <rect> each is what
// keeps an SVG of a dense code in the low tens of kilobytes and its DOM cheap
// enough to animate; the browser also fills a single path in one pass.
func svgModulePath(b *bytes.Buffer, c render.Canvas, p render.ModulePainter, skipEyes bool) {
	var d strings.Builder
	d.Grow(c.Cols * c.Rows * 3)

	for y := range c.Rows {
		for x := range c.Cols {
			if !c.At(x, y) {
				continue
			}
			// Finder patterns are painted as whole shapes below, so their
			// modules must not also appear here. When the canvas carries no
			// finder roles at all, every dark module belongs in this path.
			if skipEyes && c.Role(x, y) != render.RoleData {
				continue
			}
			d.WriteString(p.SVGPath(x, y, svgNeighbours(c, x, y)))
		}
	}

	if d.Len() == 0 {
		return
	}
	fmt.Fprintf(b, `<path %s d="%s"/>`+"\n", svgFill(c.Style.FG), d.String())
}

// svgEyePaths writes the three finder patterns as two paths: one for the
// rings, one for the centres. Nothing is written when the symbology has no
// finder patterns.
func svgEyePaths(b *bytes.Buffer, c render.Canvas, eyes [][2]int) error {
	if len(eyes) == 0 {
		return nil
	}

	frame, err := render.EyeShape(c.Style.Eye)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	ball, err := render.EyeShape(c.Style.EyeBall)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}

	var frames, balls strings.Builder
	for _, e := range eyes {
		frames.WriteString(frame.SVGFrame(e[0], e[1]))
		balls.WriteString(ball.SVGBall(e[0]+svgEyeBallOffset, e[1]+svgEyeBallOffset))
	}

	// The square eye frame is an outer square with an inner square wound the
	// same way, so only the even-odd rule leaves the ring hollow. Under the
	// default nonzero rule the eye fills solid and the code stops scanning.
	fmt.Fprintf(b, `<path %s fill-rule="evenodd" d="%s"/>`+"\n",
		svgFill(c.ColorAt(eyes[0][0], eyes[0][1])), frames.String())
	fmt.Fprintf(b, `<path %s d="%s"/>`+"\n",
		svgFill(c.ColorAt(eyes[0][0]+svgEyeBallOffset, eyes[0][1]+svgEyeBallOffset)), balls.String())
	return nil
}

// svgHRI writes the human-readable line beneath the code.
//
// The text is the only part of the document derived from request data, so it
// is escaped by encoding/xml rather than concatenated: an unescaped '<' would
// turn a payload into markup inside a document that is often served inline.
func svgHRI(b *bytes.Buffer, c render.Canvas) error {
	if c.HRI == "" {
		return nil
	}

	fmt.Fprintf(b, `<text x="%s" y="%s" font-family="monospace" font-size="%s" `+
		`text-anchor="middle" %s>`,
		svgNum(float64(c.Cols)/2),
		svgNum(float64(c.Rows)+hriFontModules),
		svgNum(hriFontModules),
		svgFill(c.Style.FG))

	if err := xml.EscapeText(b, []byte(c.HRI)); err != nil {
		return fmt.Errorf("%w: escaping the human-readable text: %w", ErrInvalidOutput, err)
	}

	b.WriteString("</text>\n")
	return nil
}

// svgEyeOrigins returns the top-left module of each finder pattern in canvas
// coordinates, or nil when this canvas has none.
//
// The positions are computed from the quiet zone rather than found by scanning
// for connected dark regions: the three corners are fixed by the QR
// specification, and a scan would also match a 7x7 ring that data modules
// happened to form. The role marked by the renderer is then the authority on
// whether this symbology has finder patterns at all — painting an eye shape
// over the live modules of a Data Matrix or a linear code would destroy it.
func svgEyeOrigins(c render.Canvas) [][2]int {
	q := c.QuietZone
	cols, rows := c.Cols-2*q, c.Rows-2*q
	if q < 0 || cols < svgEyeModules || rows < svgEyeModules {
		return nil
	}

	origins := [][2]int{
		{q, q},                        // top-left
		{q + cols - svgEyeModules, q}, // top-right
		{q, q + rows - svgEyeModules}, // bottom-left
	}
	for _, e := range origins {
		if c.Role(e[0], e[1]) != render.RoleEyeFrame {
			return nil
		}
	}
	return origins
}

// svgNeighbours reports which orthogonal neighbours of a module are dark, so a
// connected module shape can decide which corners to round.
func svgNeighbours(c render.Canvas, x, y int) render.Neighbors {
	return render.Neighbors{
		Up:    c.At(x, y-1),
		Down:  c.At(x, y+1),
		Left:  c.At(x-1, y),
		Right: c.At(x+1, y),
	}
}

// svgFill renders a colour as fill attributes.
//
// fill="#rrggbbaa" is CSS Color 4 and is still not honoured everywhere an SVG
// ends up — several print RIPs and older editors drop the alpha pair silently,
// which turns a watermark into a solid block. The portable spelling is a
// six-digit fill plus a separate fill-opacity, so translucent colours are
// written that way.
func svgFill(c color.NRGBA) string {
	hex := render.HexColor(c)
	if c.A == 0xFF {
		return `fill="` + hex + `"`
	}
	opacity := math.Round(float64(c.A)/255*1000) / 1000
	return `fill="` + hex[:7] + `" fill-opacity="` + svgNum(opacity) + `"`
}

// svgNum formats a module-unit coordinate in the shortest form that still
// round-trips. A trailing ".000000" on every number in a document with
// thousands of them is pure transfer size.
func svgNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
