package writer

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/decoder"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
)

// rasterQR builds the canvas the raster tests draw: a real QR symbol, so that
// finder patterns, a quiet zone and a realistic module mix are all exercised.
func rasterQR(t *testing.T, s render.Style) render.Canvas {
	t.Helper()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatalf("encoder.Get(qr): %v", err)
	}
	m, err := enc.Encode("BARQR RASTER TEST", encoder.AutoEncodeOpts())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return rasterRender(t, m, s)
}

// rasterLinear builds a linear canvas with human-readable text.
//
// The matrix is assembled by hand rather than taken from an encoder: the
// raster path has to work for any 1D symbology, and pinning these tests to
// whichever linear encoders happen to be compiled in would make them fail for
// reasons that have nothing to do with rasterising.
func rasterLinear(t *testing.T, s render.Style, hri string) render.Canvas {
	t.Helper()

	const cols = 40
	m := encoder.NewMatrix(cols, 1)
	m.Symbology = "test1d"
	m.Kind = encoder.Kind1D
	m.QuietZone = 4
	m.HRI = hri
	for x := range cols {
		m.Set(x, 0, x%3 != 0)
	}
	return rasterRender(t, m, s)
}

func rasterRender(t *testing.T, m encoder.Matrix, s render.Style) render.Canvas {
	t.Helper()

	r, err := render.Get(render.StandardRenderer)
	if err != nil {
		t.Fatalf("render.Get: %v", err)
	}
	c, err := r.Render(m, s)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return c
}

func TestRasterizeResolvesDimensions(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())

	tests := []struct {
		name  string
		opts  OutputOpts
		scale int
	}{
		{"default scale when nothing is given", OutputOpts{}, DefaultScale},
		{"explicit scale", OutputOpts{Scale: 7}, 7},
		{"pixel size divides down to whole modules", OutputOpts{Size: float64(c.Cols * 4), Unit: UnitPx}, 4},
		{"millimetres through dpi", OutputOpts{Size: 25.4, Unit: UnitMM, DPI: c.Cols * 3}, 3},
		{"inches through dpi", OutputOpts{Size: 2, Unit: UnitIn, DPI: c.Cols}, 2},
		{"size wins over scale", OutputOpts{Size: float64(c.Cols * 5), Scale: 99}, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			img, err := Rasterize(c, tt.opts)
			if err != nil {
				t.Fatalf("Rasterize: %v", err)
			}
			b := img.Bounds()
			if b.Dx() != c.Cols*tt.scale || b.Dy() != c.Rows*tt.scale {
				t.Errorf("bounds = %dx%d, want %dx%d",
					b.Dx(), b.Dy(), c.Cols*tt.scale, c.Rows*tt.scale)
			}
		})
	}
}

func TestRasterizeRejectsBadOptions(t *testing.T) {
	t.Parallel()

	valid := rasterQR(t, render.DefaultStyle())

	badShape := render.DefaultStyle()
	badShape.Module = "no-such-shape"
	badEye := render.DefaultStyle()
	badEye.Eye = "no-such-shape"
	badBall := render.DefaultStyle()
	badBall.EyeBall = "no-such-shape"

	tests := []struct {
		name   string
		canvas render.Canvas
		opts   OutputOpts
		want   error
	}{
		{"empty canvas", render.Canvas{}, OutputOpts{Scale: 4}, ErrInvalidOutput},
		{"unknown unit", valid, OutputOpts{Size: 100, Unit: "furlong"}, ErrInvalidOutput},
		{"size below one pixel per module", valid, OutputOpts{Size: 4, Unit: UnitPx}, ErrInvalidOutput},
		{"pixel cap", valid, OutputOpts{Scale: 10, MaxPixels: 1000}, ErrCanvasTooLarge},
		{"unknown module shape", canvasStyled(valid, badShape), OutputOpts{Scale: 2}, render.ErrUnknownShape},
		{"unknown eye shape", canvasStyled(valid, badEye), OutputOpts{Scale: 2}, render.ErrUnknownShape},
		{"unknown eye ball shape", canvasStyled(valid, badBall), OutputOpts{Scale: 2}, render.ErrUnknownShape},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Rasterize(tt.canvas, tt.opts); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// canvasStyled swaps a canvas's style without re-rendering, which is how the
// shape-lookup failures are reached: the renderer validates shapes itself, so
// an invalid one can only arrive at the rasteriser this way.
func canvasStyled(c render.Canvas, s render.Style) render.Canvas {
	c.Style = s
	return c
}

func TestRasterizeDrawsEveryModuleWhereTheCanvasSaysItIs(t *testing.T) {
	t.Parallel()

	const scale = 3
	c := rasterQR(t, render.DefaultStyle())
	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	// The square eye painter reproduces the specified finder pattern exactly,
	// so a whole-grid comparison holds through the eyes as well — which is the
	// point: a mismatch there means the frame's hollow centre was mispainted.
	for y := range c.Rows {
		for x := range c.Cols {
			want := c.Style.BG
			if c.At(x, y) {
				want = c.ColorAt(x, y)
			}
			got := img.NRGBAAt(x*scale+scale/2, y*scale+scale/2)
			if got != want {
				t.Fatalf("module (%d,%d) dark=%v role=%d: colour = %v, want %v",
					x, y, c.At(x, y), c.Role(x, y), got, want)
			}
		}
	}
}

func TestRasterizeKeepsAnOpaqueBackgroundOpaque(t *testing.T) {
	t.Parallel()

	// RasterFrame clears the middle of a finder pattern to transparent. On an
	// opaque background that hole must end up filled with the background, not
	// punched through the image.
	c := rasterQR(t, render.DefaultStyle())
	img, err := Rasterize(c, OutputOpts{Scale: 4})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if a := img.NRGBAAt(x, y).A; a != 0xFF {
				t.Fatalf("pixel (%d,%d) has alpha %d, want 255", x, y, a)
			}
		}
	}
}

func TestRasterizeHonoursATransparentBackground(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.BG = render.Transparent
	c := rasterQR(t, s)

	const scale = 4
	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	if got := img.NRGBAAt(1, 1); got.A != 0 {
		t.Errorf("quiet zone alpha = %d, want 0", got.A)
	}

	light, dark := 0, 0
	for y := range c.Rows {
		for x := range c.Cols {
			got := img.NRGBAAt(x*scale+scale/2, y*scale+scale/2)
			if c.At(x, y) {
				dark++
				if got != s.FG {
					t.Fatalf("dark module (%d,%d) = %v, want %v", x, y, got, s.FG)
				}
				continue
			}
			light++
			if got.A != 0 {
				t.Fatalf("light module (%d,%d) alpha = %d, want 0", x, y, got.A)
			}
		}
	}
	if dark == 0 || light == 0 {
		t.Fatalf("degenerate canvas: %d dark, %d light", dark, light)
	}
}

func TestRasterizeAppliesEyeColour(t *testing.T) {
	t.Parallel()

	eye := color.NRGBA{R: 0xC0, B: 0x20, A: 0xFF}
	s := render.DefaultStyle()
	s.EyeFG = &eye
	c := rasterQR(t, s)

	const scale = 5
	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	// The top-left finder starts at the first module after the quiet zone.
	q := c.QuietZone
	if got := img.NRGBAAt(q*scale+scale/2, q*scale+scale/2); got != eye {
		t.Errorf("finder frame colour = %v, want %v", got, eye)
	}
	if got := img.NRGBAAt((q+3)*scale+scale/2, (q+3)*scale+scale/2); got != eye {
		t.Errorf("finder ball colour = %v, want %v", got, eye)
	}
}

func TestRasterizeDrawsHRIBeneathTheBars(t *testing.T) {
	t.Parallel()

	const scale = 10
	c := rasterLinear(t, render.DefaultStyle(), "AB-12.3")
	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	barsBottom := (c.Rows - c.QuietZone) * scale
	if !hasInk(img, barsBottom, c.Style.FG) {
		t.Error("no text drawn below the bars")
	}

	// Suppressing the HRI must leave the band empty rather than blank-filled.
	silent := render.DefaultStyle()
	silent.HRI = false
	quiet := rasterLinear(t, silent, "AB-12.3")
	if quiet.HRI != "" {
		t.Fatalf("canvas HRI = %q, want empty", quiet.HRI)
	}
	plain, err := Rasterize(quiet, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	if hasInk(plain, barsBottom, quiet.Style.FG) {
		t.Error("text drawn for a canvas with no HRI")
	}
}

// hasInk reports whether any pixel at or below fromY is the foreground colour.
func hasInk(img *image.NRGBA, fromY int, fg color.NRGBA) bool {
	b := img.Bounds()
	for y := max(fromY, b.Min.Y); y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y) == fg {
				return true
			}
		}
	}
	return false
}

func TestRasterizeGrowsForHRIWhenTheQuietZoneIsShallow(t *testing.T) {
	t.Parallel()

	const scale = 10
	deep := render.DefaultStyle()
	shallow := render.DefaultStyle()
	shallow.QuietZone = 1

	shallowCanvas := rasterLinear(t, shallow, "1234567890")
	deepCanvas := rasterLinear(t, deep, "1234567890")

	tall, err := Rasterize(shallowCanvas, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	if got := tall.Bounds().Dy(); got <= shallowCanvas.Rows*scale {
		t.Errorf("height = %d, want more than the %d-pixel canvas",
			got, shallowCanvas.Rows*scale)
	}

	// A quiet zone deep enough to hold the text leaves the image alone; growing
	// it anyway would silently widen the margin of every linear code.
	roomy, err := Rasterize(deepCanvas, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	if got := roomy.Bounds().Dy(); got != deepCanvas.Rows*scale {
		t.Errorf("height = %d, want %d", got, deepCanvas.Rows*scale)
	}
}

func TestRasterizeCountsTheTextBandAgainstTheCap(t *testing.T) {
	t.Parallel()

	shallow := render.DefaultStyle()
	shallow.QuietZone = 1
	c := rasterLinear(t, shallow, "1234567890")

	// Exactly the size PixelSize reports: the extra text band must push it over.
	w, h, _, err := PixelSize(c, OutputOpts{Scale: 10})
	if err != nil {
		t.Fatalf("PixelSize: %v", err)
	}
	_, err = Rasterize(c, OutputOpts{Scale: 10, MaxPixels: int64(w) * int64(h)})
	if !errors.Is(err, ErrCanvasTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrCanvasTooLarge)
	}
}

func TestLayoutHRIRefusesTextItCannotFit(t *testing.T) {
	t.Parallel()

	c := rasterLinear(t, render.DefaultStyle(), "0123456789ABCDEF")
	// At one image pixel per font pixel the string is still wider than the
	// image, so the band must be refused rather than clipped.
	if _, ok := layoutHRI(c, 20, 20, 1); ok {
		t.Fatal("layoutHRI accepted text wider than the image")
	}

	blank := rasterLinear(t, render.DefaultStyle(), "")
	if _, ok := layoutHRI(blank, 400, 400, 10); ok {
		t.Fatal("layoutHRI accepted an empty string")
	}
}

func TestGlyphFoldsCaseAndFallsBackToBlank(t *testing.T) {
	t.Parallel()

	if glyph('a') != glyph('A') {
		t.Error("lowercase and uppercase A differ")
	}
	if glyph('é') != glyph(' ') {
		t.Error("unknown rune did not fall back to a blank cell")
	}
	if glyph('0') == glyph(' ') {
		t.Error("'0' is blank")
	}
}

func TestTextWidthOfNothingIsZero(t *testing.T) {
	t.Parallel()

	if got := textWidth(0, 3); got != 0 {
		t.Errorf("textWidth(0, 3) = %d, want 0", got)
	}
}

func TestFindEyesLocatesEveryFinderOnce(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	eyes := findEyes(&c)
	if len(eyes) != 3 {
		t.Fatalf("found %d finder patterns, want 3", len(eyes))
	}

	linear := rasterLinear(t, render.DefaultStyle(), "123")
	if got := findEyes(&linear); got != nil {
		t.Errorf("found %v finder patterns on a linear code, want none", got)
	}
}

// solidImage is a block of one colour. Every decoration test that needs a logo
// uses one, so that a pixel of that colour anywhere in the output can only have
// come from the logo and from nothing else the rasteriser draws.
func solidImage(c color.NRGBA, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// pixelRect converts a module-space rectangle into pixels, the way every
// assertion below has to in order to talk about a reported geometry.
func pixelRect(r image.Rectangle, scale int) image.Rectangle {
	return image.Rect(r.Min.X*scale, r.Min.Y*scale, r.Max.X*scale, r.Max.Y*scale)
}

// nearly reports whether two colours are within tol per channel. Resampling a
// logo is the one place in the rasteriser where a colour is arithmetic rather
// than a copy, so an exact comparison there would be testing the kernel's
// rounding rather than the placement.
func nearly(a, b color.NRGBA, tol int) bool {
	diff := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
	return diff(a.R, b.R) <= tol && diff(a.G, b.G) <= tol &&
		diff(a.B, b.B) <= tol && diff(a.A, b.A) <= tol
}

// widestDataModules returns the leftmost and rightmost dark data modules of the
// symbol, which is where a left-to-right gradient is furthest apart.
func widestDataModules(c render.Canvas) (lo, hi image.Point, ok bool) {
	sym := c.SymbolRect()
	lo, hi = image.Pt(-1, -1), image.Pt(-1, -1)

	for y := sym.Min.Y; y < sym.Max.Y; y++ {
		for x := sym.Min.X; x < sym.Max.X; x++ {
			if !c.At(x, y) || c.Role(x, y) != render.RoleData {
				continue
			}
			if lo.X < 0 || x < lo.X {
				lo = image.Pt(x, y)
			}
			if hi.X < 0 || x > hi.X {
				hi = image.Pt(x, y)
			}
		}
	}
	return lo, hi, lo.X >= 0 && hi.X > lo.X
}

// moduleColor samples the centre of a module.
func moduleColor(img *image.NRGBA, x, y, scale int) color.NRGBA {
	return img.NRGBAAt(x*scale+scale/2, y*scale+scale/2)
}

// testGradient ramps left to right from black to red: two colours that are far
// apart, and both far from the white background, so a flat fill cannot pass
// for a ramp.
func testGradient(kind render.GradientKind) *render.Gradient {
	return &render.Gradient{
		Kind: kind,
		Stops: []render.Stop{
			{Offset: 0, Color: color.NRGBA{A: 0xFF}},
			{Offset: 1, Color: color.NRGBA{R: 0xFF, A: 0xFF}},
		},
	}
}

func TestRasterizeRampsAGradientAcrossTheSymbol(t *testing.T) {
	t.Parallel()

	const scale = 4

	for _, tc := range []struct {
		name   string
		canvas func(*testing.T, render.Style) render.Canvas
	}{
		{"qr", func(t *testing.T, s render.Style) render.Canvas { return rasterQR(t, s) }},
		{"linear code", func(t *testing.T, s render.Style) render.Canvas {
			return rasterLinear(t, s, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Gradient = testGradient(render.GradientLinear)
			s.Gradient.Angle = 0 // left to right, so the extremes are the ends

			c := tc.canvas(t, s)
			lo, hi, ok := widestDataModules(c)
			if !ok {
				t.Fatal("no pair of data modules to compare")
			}

			img, err := Rasterize(c, OutputOpts{Scale: scale})
			if err != nil {
				t.Fatalf("Rasterize: %v", err)
			}

			gotLo := moduleColor(img, lo.X, lo.Y, scale)
			gotHi := moduleColor(img, hi.X, hi.Y, scale)
			if gotLo == gotHi {
				t.Fatalf("modules at (%d,%d) and (%d,%d) are both %v: the ramp is flat",
					lo.X, lo.Y, hi.X, hi.Y, gotLo)
			}
			// The canvas is the authority on the colour; the rasteriser only has
			// to honour it.
			if want := c.ColorAt(lo.X, lo.Y); gotLo != want {
				t.Errorf("module (%d,%d) = %v, want %v", lo.X, lo.Y, gotLo, want)
			}
			if want := c.ColorAt(hi.X, hi.Y); gotHi != want {
				t.Errorf("module (%d,%d) = %v, want %v", hi.X, hi.Y, gotHi, want)
			}

			// Without the gradient the same two modules must be identical, which
			// is what proves the difference above came from the ramp.
			flat := tc.canvas(t, render.DefaultStyle())
			plain, err := Rasterize(flat, OutputOpts{Scale: scale})
			if err != nil {
				t.Fatalf("Rasterize: %v", err)
			}
			if a, b := moduleColor(plain, lo.X, lo.Y, scale), moduleColor(plain, hi.X, hi.Y, scale); a != b {
				t.Errorf("flat style gave %v and %v, want one colour", a, b)
			}
		})
	}
}

func TestRasterizeKeepsAGradientOffTheFinderPatterns(t *testing.T) {
	t.Parallel()

	const scale = 4
	s := render.DefaultStyle()
	s.Gradient = testGradient(render.GradientRadial)
	c := rasterQR(t, s)

	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	// A ramp that runs light exactly where an eye sits costs the scanner the
	// one landmark it cannot recover from losing, so the eyes stay on FG.
	for _, e := range findEyes(&c) {
		if got := moduleColor(img, e.X, e.Y, scale); got != s.FG {
			t.Errorf("finder frame at (%d,%d) = %v, want the flat foreground %v",
				e.X, e.Y, got, s.FG)
		}
	}
}

func TestRasterizeDrawsTheLogoOnlyInsideItsRect(t *testing.T) {
	t.Parallel()

	const scale = 6
	ink := color.NRGBA{R: 0x22, G: 0x88, B: 0xEE, A: 0xFF}

	s := render.DefaultStyle()
	s.Logo = &render.Logo{Image: solidImage(ink, 24, 24), Scale: 0.3, Excavate: true, Padding: 1}
	c := rasterQR(t, s)

	rect, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo for a style that carries one")
	}
	box := pixelRect(rect, scale)

	img, err := Rasterize(c, OutputOpts{Scale: scale})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	if got := img.NRGBAAt(box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2); !nearly(got, ink, 1) {
		t.Errorf("centre of the logo rect = %v, want the logo colour %v", got, ink)
	}

	// The reserved area is the whole contract: a logo that bleeds past it eats
	// modules the renderer never excavated and Scannability never judged.
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if image.Pt(x, y).In(box) {
				continue
			}
			if got := img.NRGBAAt(x, y); nearly(got, ink, 8) {
				t.Fatalf("logo ink at (%d,%d), outside the rect %v", x, y, box)
			}
		}
	}

	if got := img.NRGBAAt(scale/2, scale/2); got != c.Style.BG {
		t.Errorf("quiet-zone corner = %v, want the background %v", got, c.Style.BG)
	}
}

func TestRasterizeLeavesTheCanvasAloneWithoutALogoImage(t *testing.T) {
	t.Parallel()

	// A logo with no bitmap is the vector case: the geometry is reserved and
	// excavated, but there is nothing to paint into it.
	s := render.DefaultStyle()
	s.Logo = &render.Logo{Scale: 0.2, Excavate: true}
	c := rasterQR(t, s)

	img, err := Rasterize(c, OutputOpts{Scale: 3})
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}

	rect, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo")
	}
	box := pixelRect(rect, 3)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			got := img.NRGBAAt(x, y)
			if got != c.Style.BG && got != c.Style.FG {
				t.Fatalf("pixel (%d,%d) = %v, want only background or foreground", x, y, got)
			}
		}
	}
}

// assertQuietZoneClear fails unless every pixel of the margin around the symbol
// is the background colour.
//
// This is the assertion that matters most for decoration: the decoder needs the
// quiet zone to find the symbol's edge at all, and a two-module border drawn
// over it eats half of a QR code's four. Everything the writers paint outside
// the modules — frame stroke, caption bar, caption text, bubble tail — has to
// stay out of this band.
func assertQuietZoneClear(t *testing.T, c render.Canvas, img *image.NRGBA, scale int) {
	t.Helper()

	sym := c.SymbolRect()
	outer := sym.Inset(-c.QuietZone)
	for y := outer.Min.Y; y < outer.Max.Y; y++ {
		for x := outer.Min.X; x < outer.Max.X; x++ {
			if image.Pt(x, y).In(sym) {
				continue
			}
			for py := y * scale; py < (y+1)*scale; py++ {
				for px := x * scale; px < (x+1)*scale; px++ {
					if got := img.NRGBAAt(px, py); got != c.Style.BG {
						t.Fatalf("quiet-zone module (%d,%d), pixel (%d,%d) = %v, want %v",
							x, y, px, py, got, c.Style.BG)
					}
				}
			}
		}
	}
}

// countColor counts pixels of exactly this colour.
func countColor(img *image.NRGBA, c color.NRGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}

func TestRasterizeDrawsEveryFrameKindOutsideTheQuietZone(t *testing.T) {
	t.Parallel()

	const scale = 5
	frameInk := color.NRGBA{R: 0xE0, B: 0x90, A: 0xFF}
	capInk := color.NRGBA{G: 0x90, B: 0x40, A: 0xFF}

	for _, tc := range []struct {
		name   string
		kind   string
		stroke bool
	}{
		{"none reserves nothing and draws nothing", render.FrameNone, false},
		{"border", render.FrameBorder, true},
		{"rounded", render.FrameRounded, true},
		{"banner", render.FrameBanner, true},
		{"bubble", render.FrameBubble, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Frame = &render.Frame{
				Kind:         tc.kind,
				Color:        frameInk,
				Width:        3,
				Caption:      "BARQR",
				CaptionColor: capInk,
			}
			c := rasterQR(t, s)

			img, err := Rasterize(c, OutputOpts{Scale: scale})
			if err != nil {
				t.Fatalf("Rasterize: %v", err)
			}

			switch got := countColor(img, frameInk); {
			case tc.stroke && got == 0:
				t.Errorf("frame kind %q drew nothing", tc.kind)
			case !tc.stroke && got != 0:
				t.Errorf("frame kind %q drew %d pixels, want none", tc.kind, got)
			}
			if got := countColor(img, capInk); got == 0 {
				t.Error("the caption drew nothing")
			}
			assertQuietZoneClear(t, c, img, scale)
		})
	}
}

func TestRasterizeKeepsTheCaptionInsideItsBand(t *testing.T) {
	t.Parallel()

	const scale = 6
	capInk := color.NRGBA{G: 0x90, B: 0x40, A: 0xFF}

	for _, tc := range []struct {
		name string
		text string
	}{
		{"markup punctuation", `A<b>&"'</b>`},
		{"far longer than the band can hold", strings.Repeat("BARQR ", 40)},
		{"a single character", "X"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Frame = &render.Frame{
				Kind:         render.FrameBubble,
				Color:        color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xFF},
				Width:        2,
				Caption:      tc.text,
				CaptionColor: capInk,
			}
			c := rasterQR(t, s)

			rect, ok := c.CaptionRect()
			if !ok {
				t.Fatal("CaptionRect reported no band for a style that carries a caption")
			}
			box := pixelRect(rect, scale)

			img, err := Rasterize(c, OutputOpts{Scale: scale})
			if err != nil {
				t.Fatalf("Rasterize: %v", err)
			}

			inside := 0
			b := img.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if img.NRGBAAt(x, y) != capInk {
						continue
					}
					if !image.Pt(x, y).In(box) {
						t.Fatalf("caption ink at (%d,%d), outside the band %v", x, y, box)
					}
					inside++
				}
			}
			if inside == 0 {
				t.Error("the caption drew nothing")
			}
			assertQuietZoneClear(t, c, img, scale)
		})
	}
}

func TestFitCaptionShrinksThenTruncates(t *testing.T) {
	t.Parallel()

	const scale = 10

	for _, tc := range []struct {
		name     string
		text     string
		w, h     int
		wantOK   bool
		wantLen  int
		ellipsis bool
	}{
		{name: "empty text is refused", text: "", w: 400, h: 40},
		{name: "a band shorter than the font is refused", text: "AB", w: 400, h: fontRows - 1},
		{name: "a short caption fits whole", text: "AB", w: 400, h: 40, wantOK: true, wantLen: 2},
		{
			name: "a long caption is cut with an ellipsis", text: "ABCDEFGHIJKLMNOP",
			w: 60, h: 40, wantOK: true, wantLen: maxGlyphs(60, 1), ellipsis: true,
		},
		{name: "no room for even one glyph", text: "ABCDEF", w: 3, h: 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			glyphs, pixel, ok := fitCaption([]rune(tc.text), tc.w, tc.h, scale)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if len(glyphs) != tc.wantLen {
				t.Errorf("glyph count = %d, want %d", len(glyphs), tc.wantLen)
			}
			if got := textWidth(len(glyphs), pixel); got > tc.w {
				t.Errorf("text is %d pixels wide, want at most %d", got, tc.w)
			}
			if got := fontRows * pixel; got > tc.h {
				t.Errorf("text is %d pixels tall, want at most %d", got, tc.h)
			}
			if tc.ellipsis {
				// Truncation has to be visible: a caption silently cut mid-word
				// reads as a different label.
				for i := max(0, len(glyphs)-3); i < len(glyphs); i++ {
					if glyphs[i] != glyph('.') {
						t.Errorf("glyph %d is not part of the ellipsis", i)
					}
				}
			}
		})
	}
}

func TestPNGWithEveryDecorationStillScans(t *testing.T) {
	t.Parallel()

	const payload = "https://barqr.example/round-trip"

	// The error-correction budget is what a logo spends, and these pairs are
	// where the round trip was measured to still hold. The gradient, the frame
	// and the caption cost nothing: none of them touches a module, and the
	// eyes keep a flat colour by design. Excavation is the whole cost, and it
	// is why a logo is a request for level H in practice — see the package
	// notes for the levels that were measured to fail.
	for _, tc := range []struct {
		name      string
		ecc       string
		logoScale float64
	}{
		{name: "level H carries the largest logo allowed", ecc: "H", logoScale: render.MaxLogoScale},
		{name: "level H at the default logo size", ecc: "H", logoScale: render.DefaultLogoScale},
		{name: "level M at the default logo size", ecc: "M", logoScale: render.DefaultLogoScale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			enc, err := encoder.Get(encoder.QR)
			if err != nil {
				t.Fatalf("encoder.Get(qr): %v", err)
			}
			opts := encoder.AutoEncodeOpts()
			opts.ECC = tc.ecc
			m, err := enc.Encode(payload, opts)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			s := render.DefaultStyle()
			s.ECC = tc.ecc
			s.Gradient = &render.Gradient{
				Kind:  render.GradientLinear,
				Angle: 45,
				Stops: []render.Stop{
					{Offset: 0, Color: color.NRGBA{A: 0xFF}},
					{Offset: 1, Color: color.NRGBA{R: 0x10, G: 0x2A, B: 0x6E, A: 0xFF}},
				},
			}
			s.Logo = &render.Logo{
				Image:    solidImage(color.NRGBA{R: 0xE8, G: 0x4A, B: 0x1C, A: 0xFF}, 64, 64),
				Scale:    tc.logoScale,
				Excavate: true,
				Padding:  1,
			}
			s.Frame = &render.Frame{
				Kind:         render.FrameBanner,
				Color:        color.NRGBA{R: 0x10, G: 0x2A, B: 0x6E, A: 0xFF},
				Width:        2,
				Caption:      "SCAN ME",
				CaptionColor: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			}

			c := rasterRender(t, m, s)
			w, err := Get(PNG)
			if err != nil {
				t.Fatalf("Get(png): %v", err)
			}
			out, err := w.Write(c, OutputOpts{Scale: 8})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			res, err := decoder.Decode(context.Background(), out, decoder.DefaultOptions())
			if err != nil {
				t.Fatalf("decoding a fully decorated code: %v", err)
			}
			if len(res) != 1 {
				t.Fatalf("decoded %d codes, want 1", len(res))
			}
			if res[0].Data != payload {
				t.Errorf("decoded %q, want %q", res[0].Data, payload)
			}
		})
	}
}
