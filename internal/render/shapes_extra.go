package render

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// Registered module shape names beyond the specified square.
//
// Every one of these trades a little scannability for appearance, and the
// order below is roughly the order of that cost: a dot loses the module
// corners, a diamond loses half the module area, and the bar shapes change the
// apparent module boundary entirely. The square remains the default for a
// reason.
const (
	// ShapeDot is a circle inscribed in the module.
	ShapeDot = "dot"
	// ShapeRounded is a square rounded only on corners with no dark
	// neighbour, so a run of modules reads as one smooth blob.
	ShapeRounded = "rounded"
	// ShapeDiamond is a rhombus on the module's edge midpoints.
	ShapeDiamond = "diamond"
	// ShapeClassy is a square with two opposite corners rounded: the "leaf".
	ShapeClassy = "classy"
	// ShapeVertical merges modules into continuous vertical bars with rounded
	// caps.
	ShapeVertical = "vertical"
	// ShapeHorizontal is ShapeVertical turned through ninety degrees.
	ShapeHorizontal = "horizontal"
)

// Corner radii in module units.
//
// roundedRadius stays well below a half so that a rounded module keeps about
// 97% of the square's area: the decoder samples the module centre, but a
// scanner's binarisation threshold is set from the local average, and shaving
// too much off every module shifts that threshold towards light. classyRadius
// is a full half because the leaf look depends on the corner becoming a
// quarter circle, and it only rounds two corners rather than four.
const (
	roundedRadius = 0.35
	classyRadius  = 0.5
	// barCapRadius rounds the end of a merged bar into a semicircle.
	barCapRadius = 0.5
)

func init() {
	RegisterModuleShape(dotPainter{})
	RegisterModuleShape(roundedPainter{})
	RegisterModuleShape(diamondPainter{})
	RegisterModuleShape(classyPainter{})
	RegisterModuleShape(barPainter{vertical: true})
	RegisterModuleShape(barPainter{vertical: false})
}

// dotPainter draws a circle inscribed in the module.
//
// The circle uses the full half-module radius rather than the smaller radius
// most "dots" styles pick. A dot that only fills 70% of its module leaves so
// much light between modules that a camera at an angle merges the gaps and the
// symbol greys out; keeping the diameter at a full module means only the four
// corners are lost, which every decoder tolerates because it samples centres.
type dotPainter struct{}

func (dotPainter) Name() string { return ShapeDot }

func (dotPainter) SVGPath(x, y int, _ Neighbors) string {
	// Two half arcs, because a single arc command cannot close a full circle:
	// with identical start and end points the renderer draws nothing.
	cx, cy := float64(x)+0.5, float64(y)+0.5
	var b strings.Builder
	b.WriteString("M" + fnum(cx-0.5) + " " + fnum(cy))
	b.WriteString("a.5 .5 0 1 0 1 0")
	b.WriteString("a.5 .5 0 1 0 -1 0")
	b.WriteString("z")
	return b.String()
}

func (dotPainter) Raster(dst *image.NRGBA, r image.Rectangle, _ Neighbors, c color.NRGBA) {
	rad := float64(min(r.Dx(), r.Dy())) / 2
	paintMask(dst, r, c, func(px, py float64) float64 {
		return roundRectCov(px, py, r, rad, rad, rad, rad)
	})
}

// roundedPainter draws a square whose corners are rounded only where no dark
// module abuts them.
//
// That is what Neighbors is for. Rounding every corner unconditionally breaks
// a run of modules into a string of beads and adds a light notch at every
// join; rounding only the free corners keeps the run solid and confines the
// change to the silhouette of the blob, which is exactly where it is safe.
type roundedPainter struct{}

func (roundedPainter) Name() string { return ShapeRounded }

func (roundedPainter) SVGPath(x, y int, n Neighbors) string {
	tl, tr, br, bl := freeCorners(n, roundedRadius)
	return roundRectPath(float64(x), float64(y), 1, 1, tl, tr, br, bl)
}

func (roundedPainter) Raster(dst *image.NRGBA, r image.Rectangle, n Neighbors, c color.NRGBA) {
	unit := float64(min(r.Dx(), r.Dy()))
	tl, tr, br, bl := freeCorners(n, roundedRadius*unit)
	paintMask(dst, r, c, func(px, py float64) float64 {
		return roundRectCov(px, py, r, tl, tr, br, bl)
	})
}

// freeCorners returns the radius for each corner, zero where a dark neighbour
// means the corner must stay square so the two modules merge cleanly.
func freeCorners(n Neighbors, radius float64) (tl, tr, br, bl float64) {
	pick := func(a, b bool) float64 {
		if a || b {
			return 0
		}
		return radius
	}
	return pick(n.Up, n.Left), pick(n.Up, n.Right), pick(n.Down, n.Right), pick(n.Down, n.Left)
}

// diamondPainter draws a rhombus on the module's edge midpoints.
//
// A diamond keeps the module's full width and height but only half its area,
// which is the most aggressive shape here. It is offered because it looks
// striking at large print sizes; at small ones the symbol loses so much ink
// that the scannability report's contrast advice stops being enough.
type diamondPainter struct{}

func (diamondPainter) Name() string { return ShapeDiamond }

func (diamondPainter) SVGPath(x, y int, _ Neighbors) string {
	fx, fy := float64(x), float64(y)
	var b strings.Builder
	b.WriteString("M" + fnum(fx+0.5) + " " + fnum(fy))
	b.WriteString("L" + fnum(fx+1) + " " + fnum(fy+0.5))
	b.WriteString("L" + fnum(fx+0.5) + " " + fnum(fy+1))
	b.WriteString("L" + fnum(fx) + " " + fnum(fy+0.5))
	b.WriteString("z")
	return b.String()
}

func (diamondPainter) Raster(dst *image.NRGBA, r image.Rectangle, _ Neighbors, c color.NRGBA) {
	paintMask(dst, r, c, func(px, py float64) float64 { return diamondCov(px, py, r) })
}

// classyPainter draws a square with the top-left and bottom-right corners
// rounded to a quarter circle: the "leaf" look.
//
// The rounding is not neighbour-aware. That is deliberate — the whole point of
// the shape is that every module carries the same diagonal, and suppressing it
// on joined modules would leave an inconsistent pattern rather than a smoother
// one.
type classyPainter struct{}

func (classyPainter) Name() string { return ShapeClassy }

func (classyPainter) SVGPath(x, y int, _ Neighbors) string {
	return roundRectPath(float64(x), float64(y), 1, 1, classyRadius, 0, classyRadius, 0)
}

func (classyPainter) Raster(dst *image.NRGBA, r image.Rectangle, _ Neighbors, c color.NRGBA) {
	rad := classyRadius * float64(min(r.Dx(), r.Dy()))
	paintMask(dst, r, c, func(px, py float64) float64 {
		return roundRectCov(px, py, r, rad, 0, rad, 0)
	})
}

// barPainter merges modules into continuous bars with rounded ends.
//
// Each module still draws only inside its own cell; the merge happens because
// a module with a dark neighbour along the bar's axis leaves that end square,
// so consecutive cells butt together with no seam. Drawing across cell
// boundaries instead would break the SVG writer, which emits one subpath per
// module and relies on them not overlapping.
type barPainter struct{ vertical bool }

func (p barPainter) Name() string {
	if p.vertical {
		return ShapeVertical
	}
	return ShapeHorizontal
}

// caps reports the radius of the two ends of the bar for this module.
func (p barPainter) caps(n Neighbors) (start, end float64) {
	before, after := n.Left, n.Right
	if p.vertical {
		before, after = n.Up, n.Down
	}
	if !before {
		start = barCapRadius
	}
	if !after {
		end = barCapRadius
	}
	return start, end
}

func (p barPainter) SVGPath(x, y int, n Neighbors) string {
	start, end := p.caps(n)
	if p.vertical {
		return roundRectPath(float64(x), float64(y), 1, 1, start, start, end, end)
	}
	return roundRectPath(float64(x), float64(y), 1, 1, start, end, end, start)
}

func (p barPainter) Raster(dst *image.NRGBA, r image.Rectangle, n Neighbors, c color.NRGBA) {
	start, end := p.caps(n)
	unit := float64(min(r.Dx(), r.Dy()))
	start, end = start*unit, end*unit

	tl, tr, br, bl := start, start, end, end
	if !p.vertical {
		tl, tr, br, bl = start, end, end, start
	}
	paintMask(dst, r, c, func(px, py float64) float64 {
		return roundRectCov(px, py, r, tl, tr, br, bl)
	})
}

// roundRectPath builds a closed clockwise subpath for a rectangle at (x, y)
// with per-corner radii, in whatever units the caller is working in.
//
// Zero-radius corners emit no arc at all. The SVG specification says an arc
// with a zero radius degenerates to a line, so emitting them would be correct
// but would roughly double the size of a path covering a few thousand modules.
func roundRectPath(x, y, w, h, tl, tr, br, bl float64) string {
	var b strings.Builder
	b.Grow(96)

	// Track the pen so that a zero-length segment — which every square corner
	// produces — is dropped rather than written out once per module.
	lx, ly := x+tl, y
	b.WriteString("M" + fnum(lx) + " " + fnum(ly))

	lineTo := func(px, py float64) {
		if px == lx && py == ly {
			return
		}
		b.WriteString("L" + fnum(px) + " " + fnum(py))
		lx, ly = px, py
	}
	arcTo := func(rad, px, py float64) {
		if rad <= 0 {
			lineTo(px, py)
			return
		}
		b.WriteString("A" + fnum(rad) + " " + fnum(rad) + " 0 0 1 " + fnum(px) + " " + fnum(py))
		lx, ly = px, py
	}

	lineTo(x+w-tr, y)
	arcTo(tr, x+w, y+tr)
	lineTo(x+w, y+h-br)
	arcTo(br, x+w-br, y+h)
	lineTo(x+bl, y+h)
	arcTo(bl, x, y+h-bl)
	lineTo(x, y+tl)
	arcTo(tl, x+tl, y)
	b.WriteString("z")
	return b.String()
}

// roundRectCov returns the anti-aliased coverage of the pixel centred at
// (px, py) by a rounded rectangle r with the given corner radii, in pixels.
//
// Coverage is approximated from the signed distance to the boundary with a one
// pixel feather. That is cheap and, unlike supersampling, keeps a straight edge
// perfectly crisp: a pixel whose centre sits half a pixel inside a vertical
// edge comes out at exactly 1.0, so a square module rasterises identically to
// the specified square painter and small codes stay sharp.
func roundRectCov(px, py float64, r image.Rectangle, tl, tr, br, bl float64) float64 {
	x0, y0 := float64(r.Min.X), float64(r.Min.Y)
	x1, y1 := float64(r.Max.X), float64(r.Max.Y)

	edge := func() float64 {
		return min(px-x0, x1-px, py-y0, y1-py) + 0.5
	}

	var cx, cy, rad float64
	switch {
	case tl > 0 && px < x0+tl && py < y0+tl:
		cx, cy, rad = x0+tl, y0+tl, tl
	case tr > 0 && px > x1-tr && py < y0+tr:
		cx, cy, rad = x1-tr, y0+tr, tr
	case br > 0 && px > x1-br && py > y1-br:
		cx, cy, rad = x1-br, y1-br, br
	case bl > 0 && px < x0+bl && py > y1-bl:
		cx, cy, rad = x0+bl, y1-bl, bl
	default:
		return edge()
	}
	return rad - math.Hypot(px-cx, py-cy) + 0.5
}

// diamondCov is roundRectCov's equivalent for the rhombus: the signed distance
// to the line |dx|/hw + |dy|/hh = 1, converted from that normalised field into
// pixels so the feather is one pixel wide however large the module is.
func diamondCov(px, py float64, r image.Rectangle) float64 {
	hw, hh := float64(r.Dx())/2, float64(r.Dy())/2
	if hw <= 0 || hh <= 0 {
		return 0
	}
	cx, cy := float64(r.Min.X)+hw, float64(r.Min.Y)+hh
	f := 1 - (math.Abs(px-cx)/hw + math.Abs(py-cy)/hh)
	return f*(hw*hh/math.Hypot(hw, hh)) + 0.5
}

// paintMask writes an anti-aliased shape into dst, touching only pixels the
// shape actually covers. Uncovered pixels are left alone so that a module never
// erases a neighbour's feathered edge.
func paintMask(dst *image.NRGBA, r image.Rectangle, c color.NRGBA, cov func(px, py float64) float64) {
	clip := r.Intersect(dst.Bounds())
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		for x := clip.Min.X; x < clip.Max.X; x++ {
			setAA(dst, x, y, c, cov(float64(x)+0.5, float64(y)+0.5))
		}
	}
}

// paintMaskSrc writes an anti-aliased shape into dst over the whole rectangle,
// clearing what the shape does not cover.
//
// Eye painters need this: squareEye fills its rectangle and then punches the
// centre back to transparent, and a writer compositing the ink layer relies on
// the whole eye rectangle being under the painter's control.
func paintMaskSrc(dst *image.NRGBA, r image.Rectangle, c color.NRGBA, cov func(px, py float64) float64) {
	clip := r.Intersect(dst.Bounds())
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		for x := clip.Min.X; x < clip.Max.X; x++ {
			v := clamp01(cov(float64(x)+0.5, float64(y)+0.5))
			dst.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: scaleAlpha(c.A, v)})
		}
	}
}

// setAA writes c at (x, y) with its alpha scaled by coverage, skipping pixels
// the shape misses entirely.
func setAA(dst *image.NRGBA, x, y int, c color.NRGBA, cov float64) {
	if cov <= 0 {
		return
	}
	dst.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: scaleAlpha(c.A, clamp01(cov))})
}

// scaleAlpha multiplies an alpha byte by a 0..1 coverage, rounding to nearest.
func scaleAlpha(a uint8, cov float64) uint8 {
	return uint8(float64(a)*cov + 0.5)
}

// clamp01 confines v to the unit interval.
func clamp01(v float64) float64 { return min(max(v, 0), 1) }

// fnum formats a coordinate for a path, dropping the trailing zeros that would
// otherwise be repeated once per module across a whole symbol.
func fnum(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
