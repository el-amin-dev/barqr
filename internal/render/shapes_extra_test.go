package render

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// extraShapes is every module shape this file adds, so that a new painter is
// covered by the whole battery below the moment it is registered.
var extraShapes = []string{
	ShapeDot, ShapeRounded, ShapeDiamond, ShapeClassy, ShapeVertical, ShapeHorizontal,
}

// allNeighbourCases enumerates the neighbour combinations that change a
// shape's silhouette: isolated, one side, and fully surrounded.
var allNeighbourCases = map[string]Neighbors{
	"isolated":   {},
	"up":         {Up: true},
	"down":       {Down: true},
	"left":       {Left: true},
	"right":      {Right: true},
	"vertical":   {Up: true, Down: true},
	"horizontal": {Left: true, Right: true},
	"surrounded": {Up: true, Down: true, Left: true, Right: true},
}

func TestExtraModuleShapesAreRegistered(t *testing.T) {
	t.Parallel()

	have := make(map[string]bool)
	for _, n := range ModuleShapes() {
		have[n] = true
	}
	for _, want := range extraShapes {
		if !have[want] {
			t.Errorf("module shape %q is not registered", want)
		}
	}
}

func TestExtraModuleShapesEmitClosedSubpaths(t *testing.T) {
	t.Parallel()

	for _, name := range extraShapes {
		for caseName, n := range allNeighbourCases {
			t.Run(name+"/"+caseName, func(t *testing.T) {
				t.Parallel()

				p, err := ModuleShape(name)
				if err != nil {
					t.Fatalf("ModuleShape(%q): %v", name, err)
				}
				got := p.SVGPath(3, 5, n)
				if got == "" {
					t.Fatal("SVGPath returned an empty fragment")
				}
				if !strings.HasPrefix(got, "M") {
					t.Errorf("SVGPath = %q, want a leading moveto", got)
				}
				if !strings.HasSuffix(got, "z") {
					t.Errorf("SVGPath = %q, want a closed subpath", got)
				}
				if strings.ContainsAny(got, "<>\"") {
					t.Errorf("SVGPath = %q, must be safe inside a d attribute", got)
				}
			})
		}
	}
}

func TestExtraModuleShapesRasterStayInsideTheirRect(t *testing.T) {
	t.Parallel()

	for _, name := range extraShapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := ModuleShape(name)
			if err != nil {
				t.Fatalf("ModuleShape(%q): %v", name, err)
			}

			dst := image.NewNRGBA(image.Rect(0, 0, 48, 48))
			r := image.Rect(12, 16, 24, 28)
			p.Raster(dst, r, Neighbors{}, color.NRGBA{A: 0xFF})

			inked, leaked := 0, 0
			for y := range 48 {
				for x := range 48 {
					if dst.NRGBAAt(x, y).A == 0 {
						continue
					}
					if image.Pt(x, y).In(r) {
						inked++
					} else {
						leaked++
					}
				}
			}
			if inked == 0 {
				t.Error("Raster marked no pixels inside the module rectangle")
			}
			if leaked != 0 {
				t.Errorf("Raster marked %d pixels outside the module rectangle", leaked)
			}
			// A shape that fills less than half its cell has lost so much ink
			// that a scanner's local threshold shifts; the diamond is the
			// deliberate floor.
			if area := r.Dx() * r.Dy(); inked*2 < area {
				t.Errorf("Raster covered %d of %d pixels, too little to scan", inked, area)
			}
		})
	}
}

func TestExtraModuleShapesRasterCentreIsOpaque(t *testing.T) {
	t.Parallel()

	for _, name := range extraShapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := ModuleShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, 16, 16))
			r := image.Rect(2, 2, 14, 14)
			p.Raster(dst, r, Neighbors{}, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF})

			mid := dst.NRGBAAt(8, 8)
			if mid.A != 0xFF {
				t.Errorf("centre alpha = %d, want 255", mid.A)
			}
			if mid.R != 0x11 || mid.G != 0x22 || mid.B != 0x33 {
				t.Errorf("centre colour = %v, want the painter's colour unchanged", mid)
			}
		})
	}
}

func TestRoundedSquaresOffCornersWithoutDarkNeighbours(t *testing.T) {
	t.Parallel()

	p, _ := ModuleShape(ShapeRounded)

	surrounded := p.SVGPath(0, 0, Neighbors{Up: true, Down: true, Left: true, Right: true})
	if strings.Contains(surrounded, "A") {
		t.Errorf("a fully surrounded module rounded a corner: %q", surrounded)
	}

	isolated := p.SVGPath(0, 0, Neighbors{})
	if strings.Count(isolated, "A") != 4 {
		t.Errorf("an isolated module should round all four corners, got %q", isolated)
	}

	// The top-left corner is shared with the module above and the one to the
	// left, so either of them keeps it square.
	if got := p.SVGPath(0, 0, Neighbors{Up: true}); strings.Count(got, "A") != 2 {
		t.Errorf("a module with a neighbour above should round two corners, got %q", got)
	}
}

func TestBarShapesRoundOnlyTheirFreeEnds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		shape string
		n     Neighbors
		arcs  int
	}{
		{"vertical isolated", ShapeVertical, Neighbors{}, 4},
		{"vertical mid-bar", ShapeVertical, Neighbors{Up: true, Down: true}, 0},
		{"vertical top of bar", ShapeVertical, Neighbors{Down: true}, 2},
		{"vertical ignores sideways neighbours", ShapeVertical, Neighbors{Left: true, Right: true}, 4},
		{"horizontal isolated", ShapeHorizontal, Neighbors{}, 4},
		{"horizontal mid-bar", ShapeHorizontal, Neighbors{Left: true, Right: true}, 0},
		{"horizontal end of bar", ShapeHorizontal, Neighbors{Left: true}, 2},
		{"horizontal ignores stacked neighbours", ShapeHorizontal, Neighbors{Up: true, Down: true}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _ := ModuleShape(tt.shape)
			got := p.SVGPath(2, 2, tt.n)
			if n := strings.Count(got, "A"); n != tt.arcs {
				t.Errorf("SVGPath(%v) = %q: %d arcs, want %d", tt.n, got, n, tt.arcs)
			}
		})
	}
}

// A bar module in the middle of a run must be a plain square in pixels too, or
// the join between two modules would show a seam.
func TestBarShapeMidRunRastersSolid(t *testing.T) {
	t.Parallel()

	p, _ := ModuleShape(ShapeVertical)
	dst := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	r := image.Rect(0, 0, 10, 10)
	p.Raster(dst, r, Neighbors{Up: true, Down: true}, color.NRGBA{A: 0xFF})

	for y := range 10 {
		for x := range 10 {
			if got := dst.NRGBAAt(x, y).A; got != 0xFF {
				t.Fatalf("pixel (%d,%d) alpha = %d, want a solid square", x, y, got)
			}
		}
	}
}

func TestDotRasterIsAntiAliasedAtTheEdge(t *testing.T) {
	t.Parallel()

	p, _ := ModuleShape(ShapeDot)
	dst := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	p.Raster(dst, image.Rect(0, 0, 20, 20), Neighbors{}, color.NRGBA{A: 0xFF})

	// The corner is outside the inscribed circle and must be untouched; the
	// pixel straddling the circle's edge must be partly transparent.
	if got := dst.NRGBAAt(0, 0).A; got != 0 {
		t.Errorf("corner alpha = %d, want 0 outside the circle", got)
	}
	partial := false
	for x := range 20 {
		if a := dst.NRGBAAt(x, 0).A; a > 0 && a < 0xFF {
			partial = true
			break
		}
	}
	if !partial {
		t.Error("no anti-aliased pixel along the circle's edge")
	}
}

func TestRasterClipsToTheDestinationImage(t *testing.T) {
	t.Parallel()

	for _, name := range extraShapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := ModuleShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, 4, 4))
			// Straddling the edge must not panic and must not write outside.
			p.Raster(dst, image.Rect(-6, -6, 6, 6), Neighbors{}, color.NRGBA{A: 0xFF})
		})
	}
}

func TestFnumTrimsTrailingZeros(t *testing.T) {
	t.Parallel()

	tests := map[float64]string{
		0:     "0",
		1:     "1",
		0.5:   "0.5",
		-0.25: "-0.25",
		2.125: "2.125",
	}
	for in, want := range tests {
		if got := fnum(in); got != want {
			t.Errorf("fnum(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestClampAndAlphaHelpers(t *testing.T) {
	t.Parallel()

	if got := clamp01(-3); got != 0 {
		t.Errorf("clamp01(-3) = %v, want 0", got)
	}
	if got := clamp01(9); got != 1 {
		t.Errorf("clamp01(9) = %v, want 1", got)
	}
	if got := scaleAlpha(0xFF, 0.5); got != 128 {
		t.Errorf("scaleAlpha(255, 0.5) = %d, want 128", got)
	}
	if got := scaleAlpha(0xFF, 0); got != 0 {
		t.Errorf("scaleAlpha(255, 0) = %d, want 0", got)
	}
}

func TestDiamondCoverageIsZeroForADegenerateRect(t *testing.T) {
	t.Parallel()

	if got := diamondCov(0, 0, image.Rect(4, 4, 4, 9)); got != 0 {
		t.Errorf("diamondCov on a zero-width rect = %v, want 0", got)
	}
}
