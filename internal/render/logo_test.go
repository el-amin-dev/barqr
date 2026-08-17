package render

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

// pngDataURI encodes a w by h grey PNG as a data URI, which is the only shape
// of logo input ParseLogo accepts.
func pngDataURI(t *testing.T, w, h int) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// abs is the integer absolute value the centring assertions need.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestParseLogoAcceptsADataURI(t *testing.T) {
	t.Parallel()

	img, err := ParseLogo(pngDataURI(t, 32, 16))
	if err != nil {
		t.Fatalf("ParseLogo: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 32 || got.Dy() != 16 {
		t.Errorf("decoded bounds = %v, want 32x16", got)
	}
}

func TestParseLogoRejectsNonDataURIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"https", "https://example.test/logo.png"},
		{"http", "http://example.test/logo.png"},
		{"file", "file:///etc/passwd"},
		{"relative path", "/var/tmp/logo.png"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseLogo(tt.in); err == nil {
				t.Fatal("ParseLogo accepted a non-data URI")
			} else if !errors.Is(err, ErrInvalidStyle) {
				t.Errorf("error %v does not wrap ErrInvalidStyle", err)
			}
		})
	}
}

func TestParseLogoRejectsMalformedDataURIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"no comma", "data:image/png;base64"},
		{"not base64 encoded", "data:image/png,%89PNG"},
		{"wrong media type", "data:text/html;base64,PGh0bWw+"},
		{"invalid base64", "data:image/png;base64,!!!!"},
		{"not an image", "data:image/png;base64," +
			base64.StdEncoding.EncodeToString([]byte("this is not a png"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseLogo(tt.in); err == nil {
				t.Fatal("ParseLogo accepted a malformed data URI")
			} else if !errors.Is(err, ErrInvalidStyle) {
				t.Errorf("error %v does not wrap ErrInvalidStyle", err)
			}
		})
	}
}

func TestParseLogoRejectsAnOversizedImage(t *testing.T) {
	t.Parallel()

	// 2100x2100 is 4.41 megapixels, past the cap, but compresses to a few
	// kilobytes: exactly the decompression bomb the pixel cap exists for.
	_, err := ParseLogo(pngDataURI(t, 2100, 2100))
	if err == nil {
		t.Fatal("ParseLogo accepted an image past the pixel cap")
	}
	if !strings.Contains(err.Error(), "pixels") {
		t.Errorf("error %v should name the pixel limit", err)
	}
}

func TestParseLogoRejectsAnOversizedPayload(t *testing.T) {
	t.Parallel()

	// The byte cap must trip on the encoded length alone, before any decode.
	huge := "data:image/png;base64," + strings.Repeat("A", 4*(maxLogoBytes/3)+8)
	if _, err := ParseLogo(huge); err == nil {
		t.Fatal("ParseLogo accepted a payload past the byte cap")
	} else if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("error %v should name the byte limit", err)
	}
}

func TestParseLogoAcceptsUnpaddedBase64(t *testing.T) {
	t.Parallel()

	uri := pngDataURI(t, 4, 4)
	body := strings.TrimRight(strings.SplitN(uri, ",", 2)[1], "=")
	if _, err := ParseLogo("data:image/png;base64," + body); err != nil {
		t.Fatalf("ParseLogo rejected unpadded base64: %v", err)
	}
}

func TestLogoValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		logo    *Logo
		wantErr bool
	}{
		{"nil", nil, false},
		{"zero scale means default", &Logo{}, false},
		{"in range", &Logo{Scale: 0.2, Padding: 2}, false},
		{"too small", &Logo{Scale: 0.01}, true},
		{"too large", &Logo{Scale: 0.9}, true},
		{"negative padding", &Logo{Scale: 0.2, Padding: -1}, true},
		{"absurd padding", &Logo{Scale: 0.2, Padding: MaxLogoPadding + 1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.logo.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidStyle) {
				t.Errorf("error %v does not wrap ErrInvalidStyle", err)
			}
		})
	}
}

func TestCanvasLogoRectIsCentredOnTheSymbol(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{Scale: 0.2}

	c := renderQR(t, 21, s)

	r, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo")
	}
	sym := c.SymbolRect()
	if sym.Dx() != 21 || sym.Dy() != 21 {
		t.Fatalf("SymbolRect = %v, want a 21x21 symbol", sym)
	}
	// 20% of 21 modules rounds to 4.
	if r.Dx() != 4 || r.Dy() != 4 {
		t.Errorf("LogoRect = %v, want 4x4 modules", r)
	}
	// A 4-module logo cannot sit exactly in the middle of a 21-module symbol,
	// so the margins may differ by one module but no more.
	if left, right := r.Min.X-sym.Min.X, sym.Max.X-r.Max.X; abs(left-right) > 1 {
		t.Errorf("LogoRect %v is off-centre: %d modules left, %d right", r, left, right)
	}
	if top, bottom := r.Min.Y-sym.Min.Y, sym.Max.Y-r.Max.Y; abs(top-bottom) > 1 {
		t.Errorf("LogoRect %v is off-centre: %d modules above, %d below", r, top, bottom)
	}
	if !r.In(sym) {
		t.Errorf("LogoRect %v escapes the symbol %v", r, sym)
	}
}

func TestCanvasLogoRectKeepsTheImageAspectRatio(t *testing.T) {
	t.Parallel()

	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{Image: img, Scale: 0.3}

	c := renderQR(t, 41, s)
	r, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo")
	}
	if r.Dy()*2 != r.Dx() {
		t.Errorf("LogoRect = %v, want a 2:1 rectangle from a 2:1 image", r)
	}
}

func TestCanvasLogoRectIsAbsentWithoutALogo(t *testing.T) {
	t.Parallel()

	c := renderQR(t, 21, DefaultStyle())
	if _, ok := c.LogoRect(); ok {
		t.Error("LogoRect reported a logo on a plain style")
	}
	if got := c.logoCoverage(); got != 0 {
		t.Errorf("logoCoverage = %v, want 0", got)
	}
}

func TestCanvasLogoRectHandlesDegenerateInput(t *testing.T) {
	t.Parallel()

	// A canvas with no symbol — the zero value a writer might carry around —
	// has nowhere to put a logo.
	empty := Canvas{Style: Style{Logo: &Logo{Scale: 0.2}}}
	if _, ok := empty.LogoRect(); ok {
		t.Error("LogoRect placed a logo on a zero-sized canvas")
	}
	empty.excavate() // must not panic

	// A zero scale falls back to the documented default rather than vanishing.
	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{}
	c := renderQR(t, 41, s)
	r, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo")
	}
	if want := int(math.Round(41 * DefaultLogoScale)); r.Dx() != want {
		t.Errorf("LogoRect width = %d, want the default scale's %d", r.Dx(), want)
	}

	// An image with no pixels must not divide by zero when the aspect ratio is
	// worked out.
	s.Logo = &Logo{Image: image.NewNRGBA(image.Rectangle{}), Scale: 0.2}
	degenerate := renderQR(t, 41, s)
	if _, ok := degenerate.LogoRect(); !ok {
		t.Error("LogoRect refused a logo with an empty image")
	}
}

func TestExcavationClearsTheModulesUnderTheLogo(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{Scale: 0.3, Excavate: true, Padding: 1}

	plain := s
	plain.Logo = &Logo{Scale: 0.3, Excavate: false, Padding: 1}

	dug := renderQR(t, 33, s)
	kept := renderQR(t, 33, plain)

	r, ok := dug.excavateRect()
	if !ok {
		t.Fatal("excavateRect reported no logo")
	}
	if r.Dx() != 12 || r.Dy() != 12 {
		t.Fatalf("excavateRect = %v, want a 10-module logo plus one module of padding", r)
	}

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if dug.At(x, y) {
				t.Fatalf("module (%d,%d) survived excavation", x, y)
			}
		}
	}
	if dug.Dark() >= kept.Dark() {
		t.Errorf("excavation removed no modules: %d dark, %d without it",
			dug.Dark(), kept.Dark())
	}

	// Everything outside the excavated area must be untouched.
	for y := range dug.Rows {
		for x := range dug.Cols {
			if image.Pt(x, y).In(r) {
				continue
			}
			if dug.At(x, y) != kept.At(x, y) {
				t.Fatalf("module (%d,%d) changed outside the excavated area", x, y)
			}
		}
	}
}

func TestExcavationNeverClearsAFinderPattern(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 0
	// The largest logo allowed, with the most padding, on the smallest QR.
	s.Logo = &Logo{Scale: MaxLogoScale, Excavate: true, Padding: MaxLogoPadding}

	c := renderQR(t, 21, s)

	// The excavated area covers the whole symbol at these settings, so the
	// finder patterns are the only thing that can still be dark.
	if r, _ := c.excavateRect(); r != c.SymbolRect() {
		t.Fatalf("excavateRect = %v, want the whole symbol %v", r, c.SymbolRect())
	}
	// The synthetic matrix fills every finder block, so a cleared module with
	// an eye role means excavation destroyed a landmark the decoder cannot
	// recover by error correction at any level.
	for y := range c.Rows {
		for x := range c.Cols {
			if c.Role(x, y) != RoleData && !c.At(x, y) {
				t.Fatalf("excavation cleared finder module (%d,%d)", x, y)
			}
		}
	}
}

func TestRenderRejectsAnInvalidLogo(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.Logo = &Logo{Scale: 0.8}

	m := qrMatrix(t, 21)
	if _, err := (standard{}).Render(m, s); !errors.Is(err, ErrInvalidStyle) {
		t.Fatalf("Render error = %v, want ErrInvalidStyle", err)
	}
}
