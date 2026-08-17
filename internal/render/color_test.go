package render_test

import (
	"errors"
	"image/color"
	"math"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestParseColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want color.NRGBA
	}{
		{"#000000", color.NRGBA{A: 0xFF}},
		{"#fff", color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}},
		{"fff", color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}},
		{"#abc", color.NRGBA{R: 0xAA, G: 0xBB, B: 0xCC, A: 0xFF}},
		{"#11223344", color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44}},
		{"#1234", color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44}},
		{"  #FF0000  ", color.NRGBA{R: 0xFF, A: 0xFF}},
		{"black", color.NRGBA{A: 0xFF}},
		{"WHITE", color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}},
		{"transparent", color.NRGBA{}},
		{"none", color.NRGBA{}},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := render.ParseColor(tt.in)
			if err != nil {
				t.Fatalf("ParseColor(%q) returned error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseColor(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseColorRejects(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "   ", "#", "#12", "#12345", "#1234567",
		"#gggggg", "rgb(1,2,3)", "darkgoldenrod"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			if _, err := render.ParseColor(in); !errors.Is(err, render.ErrInvalidColor) {
				t.Fatalf("ParseColor(%q) error = %v, want %v", in, err, render.ErrInvalidColor)
			}
		})
	}
}

func TestHexColorRoundTrips(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"#112233", "#11223344", "#000000", "#ffffff"} {
		c, err := render.ParseColor(in)
		if err != nil {
			t.Fatalf("ParseColor(%q): %v", in, err)
		}
		if got := render.HexColor(c); got != in {
			t.Errorf("HexColor(ParseColor(%q)) = %q, want %q", in, got, in)
		}
	}
}

func TestContrastRatio(t *testing.T) {
	t.Parallel()

	black := color.NRGBA{A: 0xFF}
	white := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// Black on white is the maximum the WCAG formula produces.
	if got := render.ContrastRatio(black, white); math.Abs(got-21) > 0.01 {
		t.Errorf("ContrastRatio(black, white) = %.3f, want 21", got)
	}
	// The ratio is symmetric: it is a property of the pair, not the order.
	if a, b := render.ContrastRatio(black, white), render.ContrastRatio(white, black); a != b {
		t.Errorf("ContrastRatio is not symmetric: %.3f vs %.3f", a, b)
	}
	// A colour against itself is 1:1.
	if got := render.ContrastRatio(black, black); math.Abs(got-1) > 0.001 {
		t.Errorf("ContrastRatio(black, black) = %.3f, want 1", got)
	}
}

func TestLuminanceCompositesAlphaOverWhite(t *testing.T) {
	t.Parallel()

	opaque := color.NRGBA{A: 0xFF}
	transparent := color.NRGBA{}

	// A fully transparent colour is judged as the white it will appear over,
	// which is what makes the transparent-background warning meaningful.
	if got := render.Luminance(transparent); math.Abs(got-1) > 0.001 {
		t.Errorf("Luminance(transparent) = %.3f, want 1 (white)", got)
	}
	if got := render.Luminance(opaque); got > 0.001 {
		t.Errorf("Luminance(black) = %.3f, want 0", got)
	}
}
