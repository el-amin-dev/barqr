package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
)

// Registered module shape names.
const (
	// ShapeSquare is the specified appearance: a filled unit square. It is
	// the default and the most reliably scannable.
	ShapeSquare = "square"
)

// Neighbors records which orthogonally adjacent modules are dark. Connected
// shapes — rounded, classy, and the bar-joining linear shapes — use it to
// decide which corners to round and which edges to merge.
type Neighbors struct {
	Up, Down, Left, Right bool
}

// ModulePainter draws a single module in two targets: as an SVG path fragment
// and as pixels.
//
// Coordinates for SVGPath are in module units, so a writer scales the whole
// path once rather than per module. Raster receives the exact pixel rectangle
// the module occupies, already scaled.
//
// Implementations must be stateless and safe for concurrent use.
type ModulePainter interface {
	// Name is the registry key.
	Name() string
	// SVGPath returns a path fragment for the module whose top-left corner is
	// at (x, y) in module units. It must be a closed subpath and must not
	// include the "d" attribute or any wrapper element.
	SVGPath(x, y int, n Neighbors) string
	// Raster fills the module into dst within r.
	Raster(dst *image.NRGBA, r image.Rectangle, n Neighbors, c color.NRGBA)
}

// EyePainter draws a finder pattern: the 7x7 frame and its 3x3 centre. Eyes
// are painted as whole units rather than module by module, because their
// shape is a property of the pattern, not of the individual modules.
//
// Implementations must be stateless and safe for concurrent use.
type EyePainter interface {
	// Name is the registry key.
	Name() string
	// SVGFrame returns the path for the ring of a finder pattern whose
	// top-left module is at (x, y), in module units. The ring is 7x7 modules
	// with a 5x5 hole.
	SVGFrame(x, y int) string
	// SVGBall returns the path for the solid 3x3 centre whose top-left module
	// is at (x, y), in module units.
	SVGBall(x, y int) string
	// RasterFrame and RasterBall draw the same two parts as pixels. r is the
	// full 7x7 (frame) or 3x3 (ball) pixel rectangle.
	RasterFrame(dst *image.NRGBA, r image.Rectangle, c color.NRGBA)
	RasterBall(dst *image.NRGBA, r image.Rectangle, c color.NRGBA)
}

func init() {
	RegisterModuleShape(squarePainter{})
	RegisterEyeShape(squareEye{})
}

// squarePainter draws the specified square module.
type squarePainter struct{}

func (squarePainter) Name() string { return ShapeSquare }

func (squarePainter) SVGPath(x, y int, _ Neighbors) string {
	return fmt.Sprintf("M%d %dh1v1h-1z", x, y)
}

func (squarePainter) Raster(dst *image.NRGBA, r image.Rectangle, _ Neighbors, c color.NRGBA) {
	fill(dst, r, c)
}

// squareEye draws the specified square finder pattern.
type squareEye struct{}

func (squareEye) Name() string { return ShapeSquare }

// SVGFrame draws the 7x7 ring as an outer square with an inner square wound
// the other way, so the even-odd fill rule leaves the middle hollow.
func (squareEye) SVGFrame(x, y int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "M%d %dh7v7h-7z", x, y)
	fmt.Fprintf(&b, "M%d %dv5h5v-5z", x+1, y+1)
	return b.String()
}

func (squareEye) SVGBall(x, y int) string {
	return fmt.Sprintf("M%d %dh3v3h-3z", x, y)
}

func (squareEye) RasterFrame(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	// One module is a seventh of the frame; the ring is one module thick.
	unit := r.Dx() / 7
	if unit < 1 {
		unit = 1
	}
	fill(dst, r, c)
	fill(dst, image.Rect(r.Min.X+unit, r.Min.Y+unit, r.Max.X-unit, r.Max.Y-unit), color.NRGBA{})
}

func (squareEye) RasterBall(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	fill(dst, r, c)
}

// fill paints a solid rectangle. A zero-alpha colour clears to transparent
// rather than compositing, which is what the hollow centre of an eye frame
// needs.
func fill(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	draw.Draw(dst, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}
