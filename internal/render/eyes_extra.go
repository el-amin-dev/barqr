package render

import (
	"image"
	"image/color"
	"strings"
)

// Registered eye shape names beyond the specified square.
//
// An eye shape is riskier than a module shape. The three finder patterns are
// what a decoder locates first, by looking for the 1:1:3:1:1 dark-light run
// ratio along a scan line through the centre. Every shape here preserves that
// ratio through the horizontal and vertical centre lines — which is why they
// are all concentric rings of the same proportions and differ only at the
// corners, where no centre line passes.
const (
	// EyeCircle is a circular ring with a circular centre.
	EyeCircle = "circle"
	// EyeRounded is a square ring with softened corners.
	EyeRounded = "rounded"
	// EyeLeaf rounds two opposite corners to a quarter circle.
	EyeLeaf = "leaf"
	// EyeShield rounds three corners and leaves the bottom-left square.
	EyeShield = "shield"
)

// Finder-pattern geometry in modules, fixed by the QR specification and
// repeated here because the constants in standard.go are not exported.
const (
	eyeFrameSide = 7
	eyeHoleSide  = 5
	eyeBallSide  = 3
)

func init() {
	// Radii are fractions of the shape's own side, so the ring, its hole and
	// the ball stay visually concentric at every size.
	RegisterEyeShape(cornerEye{name: EyeCircle, tl: 0.5, tr: 0.5, br: 0.5, bl: 0.5})
	RegisterEyeShape(cornerEye{name: EyeRounded, tl: 0.25, tr: 0.25, br: 0.25, bl: 0.25})
	RegisterEyeShape(cornerEye{name: EyeLeaf, tl: 0.5, br: 0.5})
	RegisterEyeShape(cornerEye{name: EyeShield, tl: 2.0 / 7, tr: 2.0 / 7, br: 2.0 / 7})
}

// cornerEye draws a finder pattern as a rounded rectangle with independent
// corner radii. One implementation covers every eye style offered here because
// a circle is just a square rounded to half its side, and a leaf is the same
// thing applied to two corners.
type cornerEye struct {
	name string
	// tl, tr, br, bl are corner radii as a fraction of the shape's side.
	tl, tr, br, bl float64
}

func (e cornerEye) Name() string { return e.name }

// SVGFrame draws the ring as an outer rounded rectangle plus a hole wound the
// same way, so the even-odd fill rule leaves the middle empty. That matches
// squareEye: the SVG writer emits fill-rule="evenodd" for the frame path, and
// a shape that relied on winding instead would fill solid and kill the code.
func (e cornerEye) SVGFrame(x, y int) string {
	var b strings.Builder
	b.WriteString(e.path(float64(x), float64(y), eyeFrameSide))
	b.WriteString(e.path(float64(x+1), float64(y+1), eyeHoleSide))
	return b.String()
}

func (e cornerEye) SVGBall(x, y int) string {
	return e.path(float64(x), float64(y), eyeBallSide)
}

// path builds one closed subpath of the given side, with the radii scaled to
// it.
func (e cornerEye) path(x, y, side float64) string {
	return roundRectPath(x, y, side, side,
		e.tl*side, e.tr*side, e.br*side, e.bl*side)
}

// RasterFrame paints the ring and clears its centre in a single pass.
//
// Coverage is the outer shape minus the hole, so the inner edge is
// anti-aliased rather than stamped out as a hard rectangle. The whole
// rectangle is written, including the cleared middle, because that is what
// squareEye does and what the rasteriser's ink layer expects: a writer
// composites the layer over the background once, and a frame that left stale
// pixels inside its own hole would show them through.
func (e cornerEye) RasterFrame(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	unit := max(r.Dx()/eyeFrameSide, 1)
	hole := image.Rect(r.Min.X+unit, r.Min.Y+unit, r.Max.X-unit, r.Max.Y-unit)

	outerR := e.radii(float64(min(r.Dx(), r.Dy())))
	holeR := e.radii(float64(min(hole.Dx(), hole.Dy())))

	paintMaskSrc(dst, r, c, func(px, py float64) float64 {
		out := clamp01(roundRectCov(px, py, r, outerR[0], outerR[1], outerR[2], outerR[3]))
		if hole.Empty() {
			return out
		}
		in := clamp01(roundRectCov(px, py, hole, holeR[0], holeR[1], holeR[2], holeR[3]))
		return out * (1 - in)
	})
}

func (e cornerEye) RasterBall(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	rad := e.radii(float64(min(r.Dx(), r.Dy())))
	paintMaskSrc(dst, r, c, func(px, py float64) float64 {
		return roundRectCov(px, py, r, rad[0], rad[1], rad[2], rad[3])
	})
}

// radii scales the corner fractions onto a side length in pixels.
func (e cornerEye) radii(side float64) [4]float64 {
	return [4]float64{e.tl * side, e.tr * side, e.br * side, e.bl * side}
}
