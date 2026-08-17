package render

import (
	"errors"
	"image/color"
	"math"
	"strings"
	"testing"
)

func TestParseGradientAcceptsDocumentedForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		kind    GradientKind
		angle   float64
		stops   int
		offsets []float64
	}{
		{
			name: "linear with angle", in: "linear(45deg,#000,#00f)",
			kind: GradientLinear, angle: 45, stops: 2, offsets: []float64{0, 1},
		},
		{
			name: "linear without angle defaults to top-to-bottom",
			in:   "linear(#000,#fff)", kind: GradientLinear, angle: 90, stops: 2,
		},
		{
			name: "bare angle without the deg suffix", in: "linear(180,#000,#fff)",
			kind: GradientLinear, angle: 180, stops: 2,
		},
		{
			name: "negative angle normalises", in: "linear(-90deg,#000,#fff)",
			kind: GradientLinear, angle: 270, stops: 2,
		},
		{
			name: "radial", in: "radial(#000,#333)",
			kind: GradientRadial, stops: 2, offsets: []float64{0, 1},
		},
		{
			name: "explicit percentage offsets",
			in:   "linear(#000 0%,#888 50%,#fff 100%)",
			kind: GradientLinear, angle: 90, stops: 3, offsets: []float64{0, 0.5, 1},
		},
		{
			name: "explicit fractional offsets", in: "radial(#000 0,#fff 0.25)",
			kind: GradientRadial, stops: 2, offsets: []float64{0, 0.25},
		},
		{
			name: "colour names and whitespace", in: " LINEAR( 0deg , black , white ) ",
			kind: GradientLinear, angle: 0, stops: 2,
		},
		{
			name: "three evenly spaced stops", in: "linear(#000,#888,#fff)",
			kind: GradientLinear, angle: 90, stops: 3, offsets: []float64{0, 0.5, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := ParseGradient(tt.in)
			if err != nil {
				t.Fatalf("ParseGradient(%q): %v", tt.in, err)
			}
			if g.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", g.Kind, tt.kind)
			}
			if g.Kind == GradientLinear && g.Angle != tt.angle {
				t.Errorf("Angle = %v, want %v", g.Angle, tt.angle)
			}
			if len(g.Stops) != tt.stops {
				t.Fatalf("got %d stops, want %d", len(g.Stops), tt.stops)
			}
			for i, want := range tt.offsets {
				if math.Abs(g.Stops[i].Offset-want) > 1e-9 {
					t.Errorf("stop %d offset = %v, want %v", i, g.Stops[i].Offset, want)
				}
			}
		})
	}
}

func TestParseGradientRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"no parentheses", "linear 45deg #000 #fff"},
		{"unclosed", "linear(#000,#fff"},
		{"unknown kind", "conic(#000,#fff)"},
		{"single stop", "linear(#000)"},
		{"no stops at all", "radial()"},
		{"angle only", "linear(45deg)"},
		{"bad colour", "linear(#0g0,#fff)"},
		{"bad offset", "linear(#000 nope,#fff)"},
		{"offset out of range", "linear(#000 -10%,#fff)"},
		{"offset above one", "linear(#000 0,#fff 200%)"},
		{"offsets out of order", "linear(#000 80%,#fff 20%)"},
		{"too many fields in a stop", "linear(#000 0% extra,#fff)"},
		{"too many stops", "linear(" + strings.Repeat("#000,", 20) + "#fff)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := ParseGradient(tt.in)
			if err == nil {
				t.Fatalf("ParseGradient(%q) = %+v, want an error", tt.in, g)
			}
			if !errors.Is(err, ErrInvalidStyle) && !errors.Is(err, ErrInvalidColor) {
				t.Errorf("error %v does not wrap a package sentinel", err)
			}
		})
	}
}

func TestGradientColorAtSpansTheArea(t *testing.T) {
	t.Parallel()

	black := color.NRGBA{A: 0xFF}
	white := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	tests := []struct {
		name       string
		spec       string
		start, end [2]int
	}{
		{"left to right", "linear(0deg,#000,#fff)", [2]int{0, 5}, [2]int{9, 5}},
		{"top to bottom", "linear(90deg,#000,#fff)", [2]int{5, 0}, [2]int{5, 9}},
		{"right to left", "linear(180deg,#000,#fff)", [2]int{9, 5}, [2]int{0, 5}},
		{"radial from the centre", "radial(#000,#fff)", [2]int{5, 5}, [2]int{0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := ParseGradient(tt.spec)
			if err != nil {
				t.Fatalf("ParseGradient: %v", err)
			}
			lo := g.ColorAt(tt.start[0], tt.start[1], 10, 10)
			hi := g.ColorAt(tt.end[0], tt.end[1], 10, 10)
			if Luminance(lo) >= Luminance(hi) {
				t.Errorf("gradient did not run from dark to light: %v then %v", lo, hi)
			}
			if ContrastRatio(lo, black) > 2 {
				t.Errorf("start colour %v is far from the first stop", lo)
			}
			if ContrastRatio(hi, white) > 2 {
				t.Errorf("end colour %v is far from the last stop", hi)
			}
		})
	}
}

func TestGradientColorAtHandlesDegenerateInput(t *testing.T) {
	t.Parallel()

	var nilGradient *Gradient
	if got := nilGradient.ColorAt(0, 0, 10, 10); got != (color.NRGBA{}) {
		t.Errorf("a nil gradient sampled to %v, want the zero colour", got)
	}

	one := &Gradient{Kind: GradientLinear, Stops: []Stop{{Color: color.NRGBA{R: 9, A: 0xFF}}}}
	if got := one.ColorAt(3, 3, 10, 10); got.R != 9 {
		t.Errorf("single-stop gradient = %v, want the only stop", got)
	}

	g, _ := ParseGradient("linear(#000,#fff)")
	if got := g.ColorAt(0, 0, 0, 0); got != g.Stops[0].Color {
		t.Errorf("zero-sized area sampled to %v, want the first stop", got)
	}
}

func TestGradientSampleClampsOutsideTheStopRange(t *testing.T) {
	t.Parallel()

	g := &Gradient{
		Kind: GradientLinear,
		Stops: []Stop{
			{Offset: 0.25, Color: color.NRGBA{A: 0xFF}},
			{Offset: 0.25, Color: color.NRGBA{R: 0x40, A: 0xFF}},
			{Offset: 0.75, Color: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}},
		},
	}
	// The ramp does not start at 0 or end at 1, so everything outside the stop
	// range takes the nearest stop's colour rather than extrapolating.
	if got := g.sample(0); got != g.Stops[0].Color {
		t.Errorf("below the first stop = %v, want the first stop", got)
	}
	if got := g.sample(1); got != g.Stops[2].Color {
		t.Errorf("above the last stop = %v, want the last stop", got)
	}
	// Two stops at the same offset are a hard colour break: the second one
	// starts the next span rather than causing a division by zero.
	if got := g.sample(0.26); got.R <= 0x40 || got.R == 0xFF {
		t.Errorf("just past a coincident stop = %v, want the second span", got)
	}
	if got := g.sample(0.5); got.R == 0 || got.R == 0xFF {
		t.Errorf("midway = %v, want an interpolated colour", got)
	}
}

func TestGradientDarkestAndLightest(t *testing.T) {
	t.Parallel()

	white := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	g, err := ParseGradient("linear(#000,#eee)")
	if err != nil {
		t.Fatalf("ParseGradient: %v", err)
	}

	dark, dr := g.Darkest(white)
	light, lr := g.Lightest(white)
	if dark.Color.R != 0 {
		t.Errorf("darkest stop = %v, want #000", dark.Color)
	}
	if light.Color.R != 0xEE {
		t.Errorf("lightest stop = %v, want #eee", light.Color)
	}
	if dr <= lr {
		t.Errorf("darkest contrast %v should exceed lightest %v", dr, lr)
	}

	var nilGradient *Gradient
	if _, r := nilGradient.Darkest(white); r != 0 {
		t.Errorf("a nil gradient reported contrast %v, want 0", r)
	}
}

func TestGradientSVGDef(t *testing.T) {
	t.Parallel()

	linear, _ := ParseGradient("linear(0deg,#001122,#ffffff)")
	def := linear.SVGDef("fg")
	for _, want := range []string{
		`<linearGradient id="fg"`, `x1="0"`, `x2="1"`, `stop-color="#001122"`, `</linearGradient>`,
	} {
		if !strings.Contains(def, want) {
			t.Errorf("SVGDef = %q, missing %q", def, want)
		}
	}

	radial, _ := ParseGradient("radial(#000,#fff)")
	rdef := radial.SVGDef("bg")
	if !strings.Contains(rdef, "<radialGradient") || !strings.Contains(rdef, "</radialGradient>") {
		t.Errorf("radial SVGDef = %q", rdef)
	}

	// Translucent stops carry their alpha as stop-opacity: SVG 1.1 has no
	// eight-digit hex colour.
	alpha := &Gradient{Kind: GradientLinear, Stops: []Stop{
		{Offset: 0, Color: color.NRGBA{A: 0x80}},
		{Offset: 1, Color: color.NRGBA{R: 0xFF, A: 0xFF}},
	}}
	if !strings.Contains(alpha.SVGDef("x"), "stop-opacity=") {
		t.Errorf("translucent stop lost its opacity: %q", alpha.SVGDef("x"))
	}

	var nilGradient *Gradient
	if got := nilGradient.SVGDef("x"); got != "" {
		t.Errorf("nil gradient SVGDef = %q, want empty", got)
	}
}

func TestGradientSVGDefSanitisesTheID(t *testing.T) {
	t.Parallel()

	g, _ := ParseGradient("linear(#000,#fff)")
	def := g.SVGDef(`x" onload="alert(1)`)
	if strings.Contains(def, `onload=`) {
		t.Errorf("SVGDef leaked an attribute out of a hostile id: %q", def)
	}
	if !strings.Contains(def, `id="x__onload`) {
		t.Errorf("SVGDef = %q, want the id folded to safe characters", def)
	}

	if got := sanitiseID(""); got != "g" {
		t.Errorf("sanitiseID(\"\") = %q, want a legal XML name", got)
	}
	if got := sanitiseID("1abc"); got != "g1abc" {
		t.Errorf("sanitiseID(\"1abc\") = %q, want a non-numeric first character", got)
	}
}

func TestGradientValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		g       *Gradient
		wantErr bool
	}{
		{"nil is fine", nil, false},
		{"unknown kind", &Gradient{Kind: "conic", Stops: make([]Stop, 2)}, true},
		{"one stop", &Gradient{Kind: GradientLinear, Stops: make([]Stop, 1)}, true},
		{"too many stops", &Gradient{Kind: GradientLinear, Stops: make([]Stop, 40)}, true},
		{"valid", &Gradient{Kind: GradientRadial, Stops: make([]Stop, 2)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.g.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidStyle) {
				t.Errorf("error %v does not wrap ErrInvalidStyle", err)
			}
		})
	}
}
