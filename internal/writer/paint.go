package writer

// Decoration shared by the writers that can actually draw.
//
// render reserves and reports the geometry of a gradient, a logo, a frame and
// a caption but paints none of it: a rectangle in module coordinates is the
// only thing a rasteriser and an SVG emitter agree on. This file turns that
// geometry into the handful of rectangles both of them draw from, and holds
// the raster side of it, so that a PNG and an SVG of one canvas put the same
// ink in the same place.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"

	"github.com/el-amin-dev/barqr/internal/render"
)

// frameSpec is a frame resolved into rectangles, in module coordinates.
//
// outer is the stroke's outer boundary and inner the hole it leaves. Both come
// from the space the renderer reserved, which is why the stroke can never
// reach the quiet zone: the renderer grew the canvas outwards for the frame
// and left the margin around the symbol untouched.
type frameSpec struct {
	outer  image.Rectangle
	inner  image.Rectangle
	radius int
	ink    color.NRGBA
	band   image.Rectangle
	tail   image.Rectangle
}

// resolveFrame works out what a canvas's frame consists of, and reports false
// when there is nothing to draw.
func resolveFrame(c render.Canvas) (frameSpec, bool) {
	outer, ok := c.FrameRect()
	if !ok || outer.Empty() {
		return frameSpec{}, false
	}
	f := c.Style.Frame

	// The renderer reserved Frame.Width modules on every side, defaulting the
	// same way, so the stroke thickness is re-derived rather than guessed: an
	// inner rectangle that disagreed with the reservation would either leave a
	// gap or bleed into the quiet zone.
	width := f.Width
	if width <= 0 {
		width = render.DefaultFrameWidth
	}
	inner := outer.Inset(width)
	if inner.Empty() {
		return frameSpec{}, false
	}

	spec := frameSpec{outer: outer, inner: inner, ink: frameInk(c)}
	if f.Kind == render.FrameRounded || f.Kind == render.FrameBubble {
		spec.radius = frameRadius(width, outer)
	}

	// A banner is a solid bar behind the caption; a bubble is the same bar with
	// a tail, which is what the renderer's extra BubbleTailModules pay for.
	if f.Kind == render.FrameBanner || f.Kind == render.FrameBubble {
		if band, hasBand := c.CaptionRect(); hasBand {
			spec.band = band
			if f.Kind == render.FrameBubble {
				spec.band.Max.Y -= render.BubbleTailModules
				if !spec.band.Empty() {
					spec.tail = bubbleTail(spec.band)
				}
			}
		}
	}
	return spec, true
}

// frameRadius rounds the OUTER edge only, and never by more than the frame has
// room for.
//
// The hole stays square on purpose. A rounded inner edge bulges towards the
// corners of the rectangle it is inset from, which is exactly where the quiet
// zone begins — and ink in the quiet zone is the classic way a pretty code
// stops scanning, because the decoder needs that clear margin to find the
// symbol's edge at all.
func frameRadius(width int, outer image.Rectangle) int {
	return max(1, min(3*width, min(outer.Dx(), outer.Dy())/4))
}

// bubbleTail places the speech-bubble tail directly under the caption bar.
func bubbleTail(band image.Rectangle) image.Rectangle {
	d := render.BubbleTailModules
	cx := band.Min.X + band.Dx()/2
	return image.Rect(cx-d, band.Max.Y, cx+d, band.Max.Y+d)
}

// frameInk is the colour the frame is stroked in.
//
// A Frame built in Go and never given a colour carries the zero NRGBA, which is
// fully transparent. Honouring that literally would draw a frame the caller
// asked for and cannot see, and an option that silently does nothing is a bug,
// so an unset colour falls back to the foreground.
func frameInk(c render.Canvas) color.NRGBA {
	if f := c.Style.Frame; f != nil && f.Color.A != 0 {
		return f.Color
	}
	return c.Style.FG
}

// captionSpec is a caption resolved into the box it must stay inside, in
// module coordinates.
type captionSpec struct {
	text string
	rect image.Rectangle
	ink  color.NRGBA
}

// resolveCaption works out what and where the caption is, and reports false
// when the canvas carries none.
func resolveCaption(c render.Canvas) (captionSpec, bool) {
	text := c.Caption()
	rect, ok := c.CaptionRect()
	if text == "" || !ok || rect.Empty() {
		return captionSpec{}, false
	}

	// The bubble's tail occupies the bottom of the reserved band, so text
	// centred in the whole band would sit half inside the tail.
	if f := c.Style.Frame; f != nil && f.Kind == render.FrameBubble {
		rect.Max.Y -= render.BubbleTailModules
	}
	if rect.Empty() {
		return captionSpec{}, false
	}
	return captionSpec{text: text, rect: rect, ink: captionInk(c)}, true
}

// captionInk is the caption's colour, falling back to the foreground for the
// same reason frameInk does.
func captionInk(c render.Canvas) color.NRGBA {
	if f := c.Style.Frame; f != nil && f.CaptionColor.A != 0 {
		return f.CaptionColor
	}
	return c.Style.FG
}

// paintFrame strokes a frame onto the ink layer.
//
// The stroke is filled row by row rather than as four rectangles because a
// rounded outer edge makes each row a different width. Only the frame's own
// band of modules is visited, so this stays proportional to the border and not
// to the image.
func paintFrame(dst *image.NRGBA, f frameSpec, scale int) {
	outer := moduleRect(f.outer.Min.X, f.outer.Min.Y, f.outer.Dx(), f.outer.Dy(), scale)
	inner := moduleRect(f.inner.Min.X, f.inner.Min.Y, f.inner.Dx(), f.inner.Dy(), scale)
	radius := f.radius * scale

	for y := outer.Min.Y; y < outer.Max.Y; y++ {
		lo, hi := roundedSpan(outer, radius, y)
		if hi <= lo {
			continue
		}
		if y < inner.Min.Y || y >= inner.Max.Y {
			fillRect(dst, image.Rect(lo, y, hi, y+1), f.ink)
			continue
		}
		fillRect(dst, image.Rect(lo, y, min(hi, inner.Min.X), y+1), f.ink)
		fillRect(dst, image.Rect(max(lo, inner.Max.X), y, hi, y+1), f.ink)
	}

	if !f.band.Empty() {
		fillRect(dst, moduleRect(f.band.Min.X, f.band.Min.Y, f.band.Dx(), f.band.Dy(), scale), f.ink)
	}
	if !f.tail.Empty() {
		paintTail(dst, moduleRect(f.tail.Min.X, f.tail.Min.Y, f.tail.Dx(), f.tail.Dy(), scale), f.ink)
	}
}

// roundedSpan is the horizontal extent of a rounded rectangle on pixel row y,
// or an empty span when the row falls outside the shape.
func roundedSpan(outer image.Rectangle, radius, y int) (lo, hi int) {
	if radius <= 0 {
		return outer.Min.X, outer.Max.X
	}

	r := float64(radius)
	// Sample at the pixel's centre, the same convention Gradient.ColorAt uses,
	// so a corner is cut at the same place in both writers.
	py := float64(y) + 0.5
	top, bottom := float64(outer.Min.Y)+r, float64(outer.Max.Y)-r

	d := 0.0
	switch {
	case py < top:
		d = top - py
	case py > bottom:
		d = py - bottom
	}
	if d >= r {
		return 0, 0
	}

	inset := int(math.Round(r - math.Sqrt(r*r-d*d)))
	return outer.Min.X + inset, outer.Max.X - inset
}

// paintTail fills the speech-bubble tail: a triangle narrowing to a point on
// its bottom row.
func paintTail(dst *image.NRGBA, r image.Rectangle, ink color.NRGBA) {
	if r.Dy() <= 0 {
		return
	}
	cx := r.Min.X + r.Dx()/2
	for y := r.Min.Y; y < r.Max.Y; y++ {
		half := r.Dx() * (r.Max.Y - y) / (2 * r.Dy())
		fillRect(dst, image.Rect(cx-half, y, cx+half, y+1), ink)
	}
}

// paintCaption draws the caption centred in its band.
func paintCaption(dst *image.NRGBA, spec captionSpec, scale int) {
	box := moduleRect(spec.rect.Min.X, spec.rect.Min.Y, spec.rect.Dx(), spec.rect.Dy(), scale)

	glyphs, pixel, ok := fitCaption([]rune(spec.text), box.Dx(), box.Dy(), scale)
	if !ok {
		return
	}

	x := box.Min.X + (box.Dx()-textWidth(len(glyphs), pixel))/2
	y := box.Min.Y + (box.Dy()-fontRows*pixel)/2
	drawGlyphs(dst, glyphs, x, y, pixel, spec.ink)
}

// fitCaption resolves the caption into glyphs and a font-pixel size that fit
// inside w by h pixels.
//
// Shrinking comes first and truncation only when one image pixel per font
// pixel is still too wide. Overflowing instead is not an option: the band is
// flush against the quiet zone, so a caption that runs past its box puts ink
// exactly where a decoder needs none. The ellipsis is three full stops because
// the bitmap font is the linear-code HRI alphabet and has no U+2026.
//
// It reports false when the band cannot hold a single row of the font, which
// only happens at about one pixel per module — the same threshold, and the same
// answer, as layoutHRI.
func fitCaption(runes []rune, w, h, scale int) ([][fontRows]uint8, int, bool) {
	if len(runes) == 0 || w <= 0 || h < fontRows {
		return nil, 0, false
	}

	// Aim for the two-module text height a printed code uses for its
	// human-readable line, then clamp to what the band actually has.
	pixel := min(max(1, (2*scale+fontRows/2)/fontRows), h/fontRows)
	for pixel > 1 && textWidth(len(runes), pixel) > w {
		pixel--
	}

	fit := len(runes)
	if textWidth(fit, pixel) > w {
		if fit = maxGlyphs(w, pixel); fit <= 0 {
			return nil, 0, false
		}
	}

	out := make([][fontRows]uint8, 0, fit)
	for _, r := range runes[:fit] {
		out = append(out, glyph(r))
	}
	if fit < len(runes) {
		for i := max(0, fit-3); i < fit; i++ {
			out[i] = glyph('.')
		}
	}
	return out, pixel, true
}

// maxGlyphs is how many glyphs fit in w pixels at this font-pixel size.
// textWidth(n, pixel) is pixel*((fontCols+fontGap)*n - fontGap), so it inverts
// directly.
func maxGlyphs(w, pixel int) int {
	return (w/pixel + fontGap) / (fontCols + fontGap)
}

// drawGlyphs paints a run of bitmap glyphs with its top-left corner at (x, y),
// fontGap font pixels of gap between cells.
//
// The human-readable line and the caption share it so that the two pieces of
// text a code can carry come out of one font at one set of metrics. The
// advance comes from fontAdvance for the same reason: measuring and drawing
// must not be able to disagree.
func drawGlyphs(dst *image.NRGBA, glyphs [][fontRows]uint8, x, y, pixel int, ink color.NRGBA) {
	for i, g := range glyphs {
		originX := x + i*fontAdvance(pixel)
		for row := range fontRows {
			bits := g[row]
			for col := range fontCols {
				// Bit fontCols-1 is the leftmost column, which is what makes
				// the binary literals in the font table look like the glyph.
				if bits&(1<<(fontCols-1-col)) == 0 {
					continue
				}
				px, py := originX+col*pixel, y+row*pixel
				fillRect(dst, image.Rect(px, py, px+pixel, py+pixel), ink)
			}
		}
	}
}

// paintLogo scales the logo into the area the renderer reserved for it.
//
// CatmullRom is worth its cost here: a logo is arbitrary artwork being reduced
// by a large factor, and the cheap kernels alias its edges into exactly the
// kind of speckle a binariser mistakes for modules. Nothing is cleared first —
// the renderer already excavated the modules underneath when asked to, and
// clearing again would widen the hole past what Scannability judged.
func paintLogo(dst *image.NRGBA, c render.Canvas, scale int) {
	l := c.Style.Logo
	if l == nil || l.Image == nil {
		return
	}
	r, ok := c.LogoRect()
	if !ok || r.Empty() {
		return
	}

	src := l.Image.Bounds()
	if src.Empty() {
		return
	}
	xdraw.CatmullRom.Scale(dst,
		moduleRect(r.Min.X, r.Min.Y, r.Dx(), r.Dy(), scale),
		l.Image, src, xdraw.Over, nil)
}

// logoDataURI encodes a logo as an inline PNG data URI.
//
// An SVG that references an external file is not a picture, it is a fetch: the
// caller who embeds it gets a broken image, and any viewer that does follow the
// reference makes a request on the caller's behalf that nobody asked for.
// Inlining keeps the document self-contained, which is also what the
// content-security policy barqr serves its SVGs under requires.
func logoDataURI(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("%w: the logo could not be re-encoded for embedding: %w",
			ErrInvalidOutput, err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
