package render

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// Transparent is the fully transparent colour, and what the literal
// "transparent" parses to.
var Transparent = color.NRGBA{}

// namedColors are the handful of names worth accepting without a hex value.
// The full CSS name list is deliberately not supported: a code is two colours,
// and a typo in "darkgoldenrod" should be an error rather than a surprise.
var namedColors = map[string]color.NRGBA{
	"transparent": Transparent,
	"none":        Transparent,
	"black":       {A: 0xFF},
	"white":       {R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
	"red":         {R: 0xFF, A: 0xFF},
	"green":       {G: 0x80, A: 0xFF},
	"blue":        {B: 0xFF, A: 0xFF},
	"yellow":      {R: 0xFF, G: 0xFF, A: 0xFF},
	"cyan":        {G: 0xFF, B: 0xFF, A: 0xFF},
	"magenta":     {R: 0xFF, B: 0xFF, A: 0xFF},
	"gray":        {R: 0x80, G: 0x80, B: 0x80, A: 0xFF},
	"grey":        {R: 0x80, G: 0x80, B: 0x80, A: 0xFF},
}

// ParseColor accepts "#rgb", "#rgba", "#rrggbb", "#rrggbbaa", the same forms
// without the leading '#', and a short list of colour names.
//
// The alpha channel is optional everywhere and defaults to fully opaque.
func ParseColor(s string) (color.NRGBA, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if raw == "" {
		return color.NRGBA{}, fmt.Errorf("%w: empty", ErrInvalidColor)
	}
	if c, ok := namedColors[raw]; ok {
		return c, nil
	}

	hex := strings.TrimPrefix(raw, "#")
	switch len(hex) {
	case 3, 4: // shorthand: each digit is doubled, #abc == #aabbcc
		var out [4]uint8
		out[3] = 0xFF
		for i := range len(hex) {
			v, err := strconv.ParseUint(hex[i:i+1], 16, 8)
			if err != nil {
				return color.NRGBA{}, fmt.Errorf("%w: %q", ErrInvalidColor, s)
			}
			out[i] = uint8(v)*16 + uint8(v)
		}
		return color.NRGBA{R: out[0], G: out[1], B: out[2], A: out[3]}, nil

	case 6, 8:
		var out [4]uint8
		out[3] = 0xFF
		for i := 0; i < len(hex); i += 2 {
			v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
			if err != nil {
				return color.NRGBA{}, fmt.Errorf("%w: %q", ErrInvalidColor, s)
			}
			out[i/2] = uint8(v)
		}
		return color.NRGBA{R: out[0], G: out[1], B: out[2], A: out[3]}, nil

	default:
		return color.NRGBA{}, fmt.Errorf(
			"%w: %q: expected #rgb, #rgba, #rrggbb, #rrggbbaa, or a colour name", ErrInvalidColor, s)
	}
}

// HexColor renders a colour as "#rrggbb", or "#rrggbbaa" when it is not fully
// opaque. It is the inverse of ParseColor for the hex forms and is what the
// SVG and JSON writers emit.
func HexColor(c color.NRGBA) string {
	if c.A == 0xFF {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// Luminance returns the relative luminance of a colour on the 0..1 scale
// defined by WCAG 2, composited over white so that a translucent colour is
// judged as it will actually appear on a light background.
//
// Contrast between foreground and background is the single strongest
// predictor of whether a scanner will read a code, so this backs the
// scannability report.
func Luminance(c color.NRGBA) float64 {
	a := float64(c.A) / 255
	r := (float64(c.R)/255)*a + (1 - a)
	g := (float64(c.G)/255)*a + (1 - a)
	b := (float64(c.B)/255)*a + (1 - a)
	return 0.2126*linearise(r) + 0.7152*linearise(g) + 0.0722*linearise(b)
}

// linearise undoes the sRGB transfer function for one channel.
func linearise(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// ContrastRatio returns the WCAG contrast ratio between two colours, from 1
// (identical) to 21 (black on white).
func ContrastRatio(fg, bg color.NRGBA) float64 {
	l1, l2 := Luminance(fg), Luminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}
