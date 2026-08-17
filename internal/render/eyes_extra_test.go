package render

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// extraEyes is every eye shape this file adds.
var extraEyes = []string{EyeCircle, EyeRounded, EyeLeaf, EyeShield}

func TestExtraEyeShapesAreRegistered(t *testing.T) {
	t.Parallel()

	have := make(map[string]bool)
	for _, n := range EyeShapes() {
		have[n] = true
	}
	for _, want := range extraEyes {
		if !have[want] {
			t.Errorf("eye shape %q is not registered", want)
		}
	}
}

func TestExtraEyeFramesEmitTwoClosedSubpaths(t *testing.T) {
	t.Parallel()

	for _, name := range extraEyes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := EyeShape(name)
			if err != nil {
				t.Fatalf("EyeShape(%q): %v", name, err)
			}

			frame := p.SVGFrame(4, 4)
			// The ring is an outer shape plus a hole, and the SVG writer fills
			// it with the even-odd rule; one subpath would fill solid.
			if n := strings.Count(frame, "M"); n != 2 {
				t.Errorf("SVGFrame has %d subpaths, want 2 (outline plus hole): %q", n, frame)
			}
			if n := strings.Count(frame, "z"); n != 2 {
				t.Errorf("SVGFrame closes %d subpaths, want 2: %q", n, frame)
			}
			if !strings.HasPrefix(frame, "M") || !strings.HasSuffix(frame, "z") {
				t.Errorf("SVGFrame = %q, want a closed path", frame)
			}

			ball := p.SVGBall(6, 6)
			if !strings.HasPrefix(ball, "M") || !strings.HasSuffix(ball, "z") {
				t.Errorf("SVGBall = %q, want a closed path", ball)
			}
			if n := strings.Count(ball, "M"); n != 1 {
				t.Errorf("SVGBall has %d subpaths, want 1: %q", n, ball)
			}
		})
	}
}

func TestExtraEyeFramesClearTheirCentre(t *testing.T) {
	t.Parallel()

	const side = 7 * 8 // 8 pixels per module

	for _, name := range extraEyes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := EyeShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, side+20, side+20))
			r := image.Rect(10, 10, 10+side, 10+side)

			// Pre-fill so that a painter which forgot to clear its centre is
			// caught: squareEye leaves the hole transparent, and every eye
			// painter must behave the same for the ink layer to composite.
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					dst.SetNRGBA(x, y, color.NRGBA{R: 0xFF, A: 0xFF})
				}
			}

			p.RasterFrame(dst, r, color.NRGBA{A: 0xFF})

			centre := dst.NRGBAAt(r.Min.X+side/2, r.Min.Y+side/2)
			if centre.A != 0 {
				t.Errorf("frame centre alpha = %d, want 0: the hole must be cleared", centre.A)
			}

			// The middle of the left arm of the ring is on both the horizontal
			// centre line and inside the ring, so every shape here must ink it.
			arm := dst.NRGBAAt(r.Min.X+4, r.Min.Y+side/2)
			if arm.A != 0xFF {
				t.Errorf("ring alpha at the left arm = %d, want 255", arm.A)
			}
		})
	}
}

func TestExtraEyeRasterStaysInsideItsRect(t *testing.T) {
	t.Parallel()

	for _, name := range extraEyes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := EyeShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, 80, 80))
			frame := image.Rect(10, 10, 45, 45)
			ball := image.Rect(50, 50, 65, 65)

			p.RasterFrame(dst, frame, color.NRGBA{A: 0xFF})
			p.RasterBall(dst, ball, color.NRGBA{A: 0xFF})

			for y := range 80 {
				for x := range 80 {
					if dst.NRGBAAt(x, y).A == 0 {
						continue
					}
					pt := image.Pt(x, y)
					if !pt.In(frame) && !pt.In(ball) {
						t.Fatalf("pixel (%d,%d) inked outside both eye rectangles", x, y)
					}
				}
			}
		})
	}
}

func TestExtraEyeBallsAreSolidAtTheCentre(t *testing.T) {
	t.Parallel()

	for _, name := range extraEyes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := EyeShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, 30, 30))
			r := image.Rect(3, 3, 27, 27)
			p.RasterBall(dst, r, color.NRGBA{B: 0x99, A: 0xFF})

			mid := dst.NRGBAAt(15, 15)
			if mid.A != 0xFF || mid.B != 0x99 {
				t.Errorf("ball centre = %v, want the painter's opaque colour", mid)
			}
		})
	}
}

// A frame rectangle smaller than seven pixels leaves no room for a one-module
// ring; the painter must degrade rather than divide by zero.
func TestExtraEyeFrameToleratesATinyRect(t *testing.T) {
	t.Parallel()

	for _, name := range extraEyes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := EyeShape(name)
			dst := image.NewNRGBA(image.Rect(0, 0, 4, 4))
			p.RasterFrame(dst, image.Rect(0, 0, 2, 2), color.NRGBA{A: 0xFF})
			p.RasterBall(dst, image.Rect(0, 0, 1, 1), color.NRGBA{A: 0xFF})
		})
	}
}
