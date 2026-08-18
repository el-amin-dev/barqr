package writer

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
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

// svgGradientID is the id the gradient definition is emitted under.
//
// It is a fixed string rather than anything derived from the request: a
// document holds exactly one gradient, and an id built from caller data is an
// XML injection waiting for the one writer that forgets to sanitise it.
const svgGradientID = "barqr-gradient"

// Caption metrics, in module units.
const (
	// svgMonoAdvance is the advance width of a monospace glyph as a fraction
	// of the font size. It is close enough to 0.6 in every monospace face that
	// ships by default to size text without measuring a font barqr does not
	// carry.
	svgMonoAdvance = 0.6
	// svgCaptionFill is how much of the caption band the text is allowed to
	// take vertically, leaving the rest as the breathing space that stops a
	// caption from touching the frame above and below it.
	svgCaptionFill = 0.55
	// svgCaptionBaseline lifts the baseline from the band's centre by roughly
	// half a cap height, which is what centres text optically.
	svgCaptionBaseline = 0.35
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
	viewRows := float64(c.Rows)
	if c.HRI != "" {
		band := hriBandModules(c)
		viewRows += band
		h += int(math.Round(band * float64(scale)))
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
			`viewBox="0 0 %d %s" shape-rendering="crispEdges">`+"\n",
		w, h, c.Cols, svgNum(viewRows))

	// The gradient is declared before anything can reference it. SVG resolves
	// a url() forward as well, but a <defs> at the top is what every editor and
	// optimiser expects to find and is cheaper for a streaming parser.
	if g := c.Style.Gradient; g != nil {
		fmt.Fprintf(&b, `<defs>%s</defs>`+"\n", g.SVGDef(svgGradientID))
	}

	if c.Style.BG.A != 0 {
		fmt.Fprintf(&b, `<rect width="%d" height="%s" %s/>`+"\n",
			c.Cols, svgNum(viewRows), svgFill(c.Style.BG))
	}

	// Frame, then code, then logo, then caption: painter's order, so a logo
	// covers the modules it sits on and no text ends up underneath it.
	svgFrame(&b, c)

	eyes := svgEyeOrigins(c)
	svgModulePath(&b, c, modules, eyes != nil)
	if err := svgEyePaths(&b, c, eyes); err != nil {
		return nil, err
	}
	if err := svgLogo(&b, c); err != nil {
		return nil, err
	}
	if err := svgCaption(&b, c); err != nil {
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

	// A gradient replaces the flat foreground for data modules only, which is
	// exactly the set this path holds: the finder patterns are painted by
	// svgEyePaths in their own flat colour, because a ramp that runs light
	// across an eye costs the scanner the landmark it locates the symbol with.
	fill := svgFill(c.Style.FG)
	if c.Style.Gradient != nil {
		fill = `fill="url(#` + svgGradientID + `)"`
	}
	fmt.Fprintf(b, `<path %s d="%s"/>`+"\n", fill, d.String())
}

// svgFrame writes the frame: its stroke, the caption bar a banner or bubble
// carries, and a bubble's tail. Nothing is written when the style has no frame.
func svgFrame(b *bytes.Buffer, c render.Canvas) {
	f, ok := resolveFrame(c)
	if !ok {
		return
	}

	// The outer boundary and the square hole go in one path under the even-odd
	// rule. A stroked outline would be centred on its own path and spill half
	// its width outside the modules the renderer reserved, which on the inside
	// edge means ink in the quiet zone.
	fmt.Fprintf(b, `<path %s fill-rule="evenodd" d="%s%s"/>`+"\n",
		svgFill(f.ink), svgRoundRectPath(f.outer, f.radius), svgRectPath(f.inner))

	// The bar and the tail are paths rather than <rect>/<polygon> so that the
	// whole frame is one kind of element to style, clip, or strip.
	if !f.band.Empty() {
		fmt.Fprintf(b, `<path %s d="%s"/>`+"\n", svgFill(f.ink), svgRectPath(f.band))
	}
	if !f.tail.Empty() {
		fmt.Fprintf(b, `<path %s d="%s"/>`+"\n", svgFill(f.ink), svgTailPath(f.tail))
	}
}

// svgRectPath is a closed rectangular subpath in module units.
func svgRectPath(r image.Rectangle) string {
	return fmt.Sprintf("M%d %dH%dV%dH%dZ", r.Min.X, r.Min.Y, r.Max.X, r.Max.Y, r.Min.X)
}

// svgRoundRectPath is a rectangle with quarter-circle corners, wound clockwise
// so the arc sweep flag is 1 throughout.
func svgRoundRectPath(r image.Rectangle, radius int) string {
	if radius <= 0 {
		return svgRectPath(r)
	}
	return fmt.Sprintf(
		"M%d %dH%dA%d %d 0 0 1 %d %dV%dA%d %d 0 0 1 %d %dH%dA%d %d 0 0 1 %d %dV%dA%d %d 0 0 1 %d %dZ",
		r.Min.X+radius, r.Min.Y, r.Max.X-radius,
		radius, radius, r.Max.X, r.Min.Y+radius,
		r.Max.Y-radius,
		radius, radius, r.Max.X-radius, r.Max.Y,
		r.Min.X+radius,
		radius, radius, r.Min.X, r.Max.Y-radius,
		r.Min.Y+radius,
		radius, radius, r.Min.X+radius, r.Min.Y)
}

// svgTailPath is the speech-bubble tail: a triangle narrowing to a point on the
// bottom edge of its box.
func svgTailPath(r image.Rectangle) string {
	return fmt.Sprintf("M%d %dH%dL%d %dZ",
		r.Min.X, r.Min.Y, r.Max.X, r.Min.X+r.Dx()/2, r.Max.Y)
}

// svgLogo writes the logo as an <image> over the area the renderer reserved.
func svgLogo(b *bytes.Buffer, c render.Canvas) error {
	l := c.Style.Logo
	if l == nil || l.Image == nil {
		return nil
	}
	r, ok := c.LogoRect()
	if !ok || r.Empty() {
		return nil
	}

	uri, err := logoDataURI(l.Image)
	if err != nil {
		return err
	}

	// xMidYMid meet fits the artwork inside the box without distorting it. The
	// renderer already sized the box from the image's own aspect ratio, so the
	// fit is exact in practice and letterboxes only when a caller supplied the
	// geometry and the bitmap separately.
	fmt.Fprintf(b, `<image x="%d" y="%d" width="%d" height="%d" `+
		`preserveAspectRatio="xMidYMid meet" href="%s"/>`+"\n",
		r.Min.X, r.Min.Y, r.Dx(), r.Dy(), uri)
	return nil
}

// svgCaption writes the caption centred in its band.
//
// Like the human-readable line it is caller-supplied text, so it is escaped
// through encoding/xml rather than concatenated: an unescaped '<' turns a label
// into markup inside a document that is often served inline.
func svgCaption(b *bytes.Buffer, c render.Canvas) error {
	spec, ok := resolveCaption(c)
	if !ok {
		return nil
	}
	size := svgCaptionSize(spec)

	fmt.Fprintf(b, `<text x="%s" y="%s" font-family="monospace" font-size="%s" `+
		`text-anchor="middle" %s>`,
		svgNum(float64(spec.rect.Min.X)+float64(spec.rect.Dx())/2),
		svgNum(float64(spec.rect.Min.Y)+float64(spec.rect.Dy())/2+size*svgCaptionBaseline),
		svgNum(size),
		svgFill(spec.ink))

	if err := xml.EscapeText(b, []byte(spec.text)); err != nil {
		return fmt.Errorf("%w: escaping the caption: %w", ErrInvalidOutput, err)
	}

	b.WriteString("</text>\n")
	return nil
}

// svgCaptionSize picks a font size in modules that keeps the caption inside its
// band both ways: the band's height caps it, and a long caption shrinks to the
// band's width rather than running out through the frame.
func svgCaptionSize(spec captionSpec) float64 {
	size := float64(spec.rect.Dy()) * svgCaptionFill
	if n := len([]rune(spec.text)); n > 0 {
		size = min(size, float64(spec.rect.Dx())/(svgMonoAdvance*float64(n)))
	}
	// Three decimals is finer than any renderer resolves at these sizes and
	// keeps the document free of float noise. It rounds down rather than to
	// nearest so that trimming the number can never push the text back over the
	// width it was just fitted to.
	return math.Floor(size*1000) / 1000
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

	fmt.Fprintf(b, `<text x="%s" y="%s" font-family="%s" font-size="%s" `+
		`text-anchor="middle" %s>`,
		svgNum(float64(c.Cols)/2),
		svgNum(float64(c.Rows)+hriFontModules(c)),
		svgFontFamily(c.Style.HRIFont),
		svgNum(hriFontModules(c)),
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
// The positions are computed from the symbol's own rectangle rather than found
// by scanning for connected dark regions: the three corners are fixed by the QR
// specification, and a scan would also match a 7x7 ring that data modules
// happened to form. SymbolRect rather than the quiet zone alone, because a
// frame and a caption band move the symbol as well, and origins derived from
// the margin would land on the frame the moment one is set. The role marked by
// the renderer is then the authority on whether this symbology has finder
// patterns at all — painting an eye shape over the live modules of a Data
// Matrix or a linear code would destroy it.
func svgEyeOrigins(c render.Canvas) [][2]int {
	sym := c.SymbolRect()
	if sym.Dx() < svgEyeModules || sym.Dy() < svgEyeModules {
		return nil
	}

	origins := [][2]int{
		{sym.Min.X, sym.Min.Y},                 // top-left
		{sym.Max.X - svgEyeModules, sym.Min.Y}, // top-right
		{sym.Min.X, sym.Max.Y - svgEyeModules}, // bottom-left
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
