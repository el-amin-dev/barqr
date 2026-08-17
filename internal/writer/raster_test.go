package writer

import (
	"errors"
	"image"
	"image/color"
	"testing"

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
