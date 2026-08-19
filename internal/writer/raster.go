package writer

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/el-amin-dev/barqr/internal/render"
)

// Finder-pattern geometry, fixed by the QR specification. The renderer marks
// the roles; the rasteriser needs the sizes to turn a marked corner back into
// the two rectangles an EyePainter expects.
const (
	eyeFrameModules = 7
	eyeBallOffset   = 2
	eyeBallModules  = 3
)

// Rasterize draws a canvas into an NRGBA image.
//
// It is the single raster path: PNG, JPEG, WebP and anything else pixel-based
// differ only in how they encode the result, so sizing, shape dispatch, eye
// painting and the human-readable text all live here and are exercised by
// every raster format at once.
//
// The drawing happens in two layers. Every module, eye and glyph is painted
// onto a transparent ink layer, which is then composited over the background
// in one pass. That indirection is not decoration: EyePainter.RasterFrame
// punches the centre of a finder frame back to transparent, and a painter may
// use a translucent colour. Painting straight onto a filled background would
// leave a hole through an opaque background and would make a translucent
// module blend with nothing. Compositing once makes both cases correct without
// the rasteriser knowing the shape of any particular hole.
//
// The background is a single flat colour, so the composite happens in place
// against that colour rather than into a second image. It saves a full-image
// allocation and a copy on every render, which on a default QR is most of the
// rasteriser's cost.
func Rasterize(c render.Canvas, o OutputOpts) (*image.NRGBA, error) {
	w, h, scale, err := PixelSize(c, o)
	if err != nil {
		return nil, err
	}

	module, err := render.ModuleShape(c.Style.Module)
	if err != nil {
		return nil, err
	}
	frame, err := render.EyeShape(c.Style.Eye)
	if err != nil {
		return nil, err
	}
	ball, err := render.EyeShape(c.Style.EyeBall)
	if err != nil {
		return nil, err
	}

	// The text band may push past the quiet zone and make the image taller
	// than PixelSize reported, so the cap is re-checked against the real size.
	text, hasText := layoutHRI(c, w, h, scale)
	total := h + text.extra
	if o.MaxPixels > 0 && int64(w)*int64(total) > o.MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d exceeds %d pixels",
			ErrCanvasTooLarge, w, total, o.MaxPixels)
	}

	bounds := image.Rect(0, 0, w, total)
	ink := image.NewNRGBA(bounds)

	// The frame goes down first. It occupies only the modules the renderer
	// reserved outside the quiet zone, so it never collides with a module, but
	// painting it below the code is what keeps that true if a shape ever grows.
	if fr, ok := resolveFrame(c); ok {
		paintFrame(ink, fr, scale)
	}

	for y := range c.Rows {
		for x := range c.Cols {
			// Eye modules are skipped here and painted below as whole
			// patterns: the shape of an eye belongs to the pattern, not to
			// the modules it is made of.
			if !c.At(x, y) || c.Role(x, y) != render.RoleData {
				continue
			}
			n := render.Neighbors{
				Up:    c.At(x, y-1),
				Down:  c.At(x, y+1),
				Left:  c.At(x-1, y),
				Right: c.At(x+1, y),
			}
			module.Raster(ink, moduleRect(x, y, 1, 1, scale), n, c.ColorAt(x, y))
		}
	}

	for _, e := range findEyes(&c) {
		frame.RasterFrame(ink,
			moduleRect(e.X, e.Y, eyeFrameModules, eyeFrameModules, scale),
			c.ColorAt(e.X, e.Y))

		bx, by := e.X+eyeBallOffset, e.Y+eyeBallOffset
		ball.RasterBall(ink,
			moduleRect(bx, by, eyeBallModules, eyeBallModules, scale),
			c.ColorAt(bx, by))
	}

	// The logo lands after the modules so that a caller who did not ask for
	// excavation still gets it on top of them, and before any text so that no
	// glyph disappears underneath it.
	paintLogo(ink, c, scale)

	if spec, ok := resolveCaption(c); ok {
		paintCaption(ink, spec, scale)
	}

	if hasText {
		drawHRI(ink, text, c.Style.FG)
	}

	compositeOverUniform(ink, c.Style.BG)
	return ink, nil
}

// compositeOverUniform composites the ink layer over a flat background colour,
// in place.
//
// image/draw has no fast path for NRGBA over NRGBA, so its generic loop is
// several times slower than this one — and compositing against a uniform needs
// no second image at all. The two common pixels, fully inked and fully clear,
// are branches rather than arithmetic because a code is overwhelmingly made of
// them: only anti-aliased shape edges reach the blend.
//
// Every narrowing conversion below is a weighted average of two bytes divided
// by the sum of its own weights, so each is bounded by 255 by construction:
// (255*a + 255*(255-a) + 127)/255 = 255 is the maximum. The alpha term is
// (outA+127)/255 where outA <= 255*255. Static analysis cannot see that, hence
// the blanket annotation.
//
// #nosec G115 -- every conversion is a weighted average bounded by 255; see above
func compositeOverUniform(dst *image.NRGBA, bg color.NRGBA) {
	if bg.A == 0 {
		return // nothing to composite against; the ink layer is the result
	}
	opaqueBG := bg.A == 0xFF

	for i := 0; i < len(dst.Pix); i += 4 {
		p := dst.Pix[i : i+4 : i+4]

		switch p[3] {
		case 0xFF:
			continue // the ink already covers this pixel completely
		case 0x00:
			p[0], p[1], p[2], p[3] = bg.R, bg.G, bg.B, bg.A
			continue
		}

		a := uint32(p[3])
		inv := 255 - a

		if opaqueBG {
			// Source-over onto an opaque backdrop: the result is opaque, so
			// the alpha divisions cancel out. Every term is a weighted average
			// of two bytes, so the quotient cannot exceed 255:
			// (255*a + 255*(255-a) + 127)/255 = 255.
			p[0] = uint8((uint32(p[0])*a + uint32(bg.R)*inv + 127) / 255)
			p[1] = uint8((uint32(p[1])*a + uint32(bg.G)*inv + 127) / 255)
			p[2] = uint8((uint32(p[2])*a + uint32(bg.B)*inv + 127) / 255)
			p[3] = 0xFF
			continue
		}

		// General non-premultiplied source-over. outA is never zero here: the
		// fully clear case was handled above. Each channel is again a weighted
		// average whose weights sum to outA, so the quotient stays within a
		// byte, and outA itself is at most 255*255.
		ba := uint32(bg.A)
		outA := a*255 + ba*inv
		p[0] = uint8((uint32(p[0])*a*255 + uint32(bg.R)*ba*inv) / outA)
		p[1] = uint8((uint32(p[1])*a*255 + uint32(bg.G)*ba*inv) / outA)
		p[2] = uint8((uint32(p[2])*a*255 + uint32(bg.B)*ba*inv) / outA)
		p[3] = uint8((outA + 127) / 255)
	}
}

// moduleRect converts a module-space rectangle into pixels.
func moduleRect(x, y, cols, rows, scale int) image.Rectangle {
	return image.Rect(x*scale, y*scale, (x+cols)*scale, (y+rows)*scale)
}

// fillRect paints a solid rectangle, clipped to the image. A zero-alpha colour
// clears rather than composites, which is what the ink layer wants.
//
// This runs once per module, so it writes bytes directly: at ten pixels square
// image/draw's per-call setup costs more than the fill itself. The first row is
// built once and copied down, which lets the runtime use its vectorised copy.
func fillRect(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(dst.Bounds())
	if r.Empty() {
		return
	}

	start := dst.PixOffset(r.Min.X, r.Min.Y)
	width := r.Dx() * 4
	row := dst.Pix[start : start+width : start+width]
	for i := 0; i < width; i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, c.A
	}
	for y := r.Min.Y + 1; y < r.Max.Y; y++ {
		off := dst.PixOffset(r.Min.X, y)
		copy(dst.Pix[off:off+width], row)
	}
}

// findEyes returns the top-left module of every finder pattern.
//
// The renderer marks a full 7x7 block per finder, so the origin is the only
// frame module with no frame or ball module above it or to its left. Scanning
// for that rather than recomputing the QR corner layout keeps the rasteriser
// working for any symbology whose renderer marks eyes, and yields nothing at
// all when none are marked.
func findEyes(c *render.Canvas) []image.Point {
	var out []image.Point
	for y := range c.Rows {
		for x := range c.Cols {
			if c.Role(x, y) != render.RoleEyeFrame {
				continue
			}
			if c.Role(x-1, y) != render.RoleData || c.Role(x, y-1) != render.RoleData {
				continue
			}
			out = append(out, image.Pt(x, y))
		}
	}
	return out
}

// hriBand is the placement of the human-readable text under a linear code.
type hriBand struct {
	// face is the type family the glyphs came from, needed again to draw them.
	face *bitmapFace
	// glyphs are the resolved bitmaps, one per character.
	glyphs []glyphBits
	// pixel is the size of one font pixel in image pixels.
	pixel int
	// x and y are the top-left corner of the text block.
	x, y int
	// extra is how many pixels the image must grow by to hold the band.
	extra int
}

// layoutHRI places the human-readable text beneath the bars.
//
// The text sits in the bottom quiet zone where a printed linear code puts it,
// and the image is only made taller when the quiet zone is too shallow to hold
// it — growing the image unconditionally would silently widen the margin of
// every code that already had room.
//
// It reports false when there is no text, or when the text cannot be drawn at
// even one image pixel per font pixel. Refusing is better than clipping: half
// a digit under a barcode reads as a different number.
func layoutHRI(c render.Canvas, w, h, scale int) (hriBand, bool) {
	if c.HRI == "" {
		return hriBand{}, false
	}

	f := hriFace(c.Style.HRIFont)
	runes := []rune(c.HRI)
	band := hriBand{face: f, glyphs: make([]glyphBits, 0, len(runes))}
	for _, r := range runes {
		band.glyphs = append(band.glyphs, f.glyph(r))
	}

	// Aim for the requested text height — two modules by default, the usual
	// proportion on a printed linear code — then shrink until the whole string
	// fits the image width.
	size := c.Style.HRISize
	if size <= 0 {
		size = render.DefaultHRISize
	}
	target := int(math.Round(size * float64(scale)))
	band.pixel = max(1, (target+f.rows/2)/f.rows)
	for band.pixel > 1 && f.textWidth(len(band.glyphs), band.pixel) > w {
		band.pixel--
	}
	tw := f.textWidth(len(band.glyphs), band.pixel)
	if tw > w {
		return hriBand{}, false
	}

	// One font pixel of clearance is too tight at large scales and half a
	// module is too much at small ones, so take whichever is larger.
	pad := max(band.pixel, scale/2)
	// SymbolRect is where the bars actually are. Deriving the baseline from
	// Rows minus the quiet zone would be wrong the moment a frame or a caption
	// band grows the canvas, and would drop the text on top of the frame.
	barsBottom := c.SymbolRect().Max.Y * scale
	// Reserve the height that was asked for even when the width shrank the
	// font below it. Without this, a width-constrained code renders
	// identically at hri_size 2 and 6 — an option accepted and then discarded,
	// which is the failure this project keeps having to relearn.
	needed := pad + max(f.rows*band.pixel, target) + pad

	band.x = (w - tw) / 2
	band.y = barsBottom + pad
	band.extra = max(0, needed-(h-barsBottom))
	return band, true
}

// drawHRI paints the laid-out text onto the ink layer. The glyph drawing
// itself lives in paint.go, shared with the caption band.
func drawHRI(dst *image.NRGBA, b hriBand, c color.NRGBA) {
	drawGlyphs(dst, b.face, b.glyphs, b.x, b.y, b.pixel, c)
}

// Font cell size. Five by seven is the smallest cell in which every digit
// stays distinguishable when a code is printed small.
const (
	fontCols = 5
	fontRows = 7

	// fontGap is the blank space between adjacent glyph cells, in font pixels.
	//
	// One is too few. At pixel = 1 a single blank column is the first thing
	// lost to print dot-gain or a soft scan, and glyphs whose outer columns
	// are both lit — W, M, H — then read as one smear. That matters more here
	// than it would elsewhere, because the HRI line is the fallback path: it
	// is read precisely when the barcode would not scan.
	fontGap = 2
)

// font5x7 is the embedded bitmap font: seven rows of five pixels per
// character, most significant bit leftmost. It is read-only after init.
//
// The alphabet is exactly what the linear symbologies can put in their
// human-readable text — digits, capitals, and the Code 39 punctuation set —
// because a barcode's HRI is by definition a subset of what it encodes.
//
// Rows are binary literals so that the glyph is legible in the source; edit
// one by looking at it, not by counting hex.
var font5x7 = map[rune][fontRows]uint8{
	' ': {0, 0, 0, 0, 0, 0, 0},
	'-': {0b00000, 0b00000, 0b00000, 0b11111, 0b00000, 0b00000, 0b00000},
	'.': {0b00000, 0b00000, 0b00000, 0b00000, 0b00000, 0b01100, 0b01100},
	'*': {0b00000, 0b10101, 0b01110, 0b11111, 0b01110, 0b10101, 0b00000},
	'$': {0b00100, 0b01111, 0b10100, 0b01110, 0b00101, 0b11110, 0b00100},
	'/': {0b00001, 0b00010, 0b00010, 0b00100, 0b01000, 0b01000, 0b10000},
	'+': {0b00000, 0b00100, 0b00100, 0b11111, 0b00100, 0b00100, 0b00000},
	'%': {0b11000, 0b11001, 0b00010, 0b00100, 0b01000, 0b10011, 0b00011},
	'0': {0b01110, 0b10011, 0b10111, 0b10101, 0b11101, 0b11001, 0b01110},
	'1': {0b00100, 0b01100, 0b10100, 0b00100, 0b00100, 0b00100, 0b11111},
	'2': {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111},
	'3': {0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110},
	'4': {0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010},
	'5': {0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110},
	'6': {0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110},
	'7': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000},
	'8': {0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110},
	'9': {0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100},
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'B': {0b11100, 0b10010, 0b10010, 0b11100, 0b10010, 0b10010, 0b11100},
	'C': {0b01111, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b01111},
	'D': {0b11100, 0b10010, 0b10001, 0b10001, 0b10001, 0b10010, 0b11100},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'F': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000},
	'G': {0b00110, 0b01001, 0b10000, 0b10111, 0b10001, 0b01001, 0b00110},
	'H': {0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'I': {0b01110, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'J': {0b00111, 0b00010, 0b00010, 0b00010, 0b00010, 0b10010, 0b01100},
	'K': {0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001},
	'L': {0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111},
	'M': {0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001},
	'N': {0b10001, 0b11001, 0b11001, 0b10101, 0b10011, 0b10011, 0b10001},
	'O': {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'P': {0b11110, 0b10001, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000},
	'Q': {0b01110, 0b10001, 0b10001, 0b10101, 0b10011, 0b10010, 0b01101},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10110, 0b10011, 0b10001},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'U': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'V': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100},
	'W': {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b11011, 0b10001},
	'X': {0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001},
	'Y': {0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100},
	'Z': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b11111},
}
