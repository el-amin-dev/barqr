package render

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// GradientKind names the geometry of a gradient fill.
type GradientKind string

// Gradient geometries.
const (
	// GradientLinear sweeps along a line at Gradient.Angle.
	GradientLinear GradientKind = "linear"
	// GradientRadial sweeps outwards from the centre of the symbol.
	GradientRadial GradientKind = "radial"
)

// Stop is one colour stop of a gradient.
type Stop struct {
	// Offset is the stop's position along the gradient, 0 to 1.
	Offset float64
	// Color is the colour at that position.
	Color color.NRGBA
}

// Gradient fills the data modules with a colour ramp instead of a flat colour.
//
// It replaces Style.FG for data modules only. The finder patterns keep their
// own colour, because a gradient that runs light exactly where an eye sits
// costs the scanner the one landmark it cannot recover from losing.
type Gradient struct {
	// Kind is linear or radial.
	Kind GradientKind
	// Angle is the sweep direction in degrees for a linear gradient, measured
	// clockwise from left-to-right because the module grid's y axis points
	// down. Ignored for a radial gradient.
	Angle float64
	// Stops are the colour stops, at least two, in non-decreasing offset
	// order.
	Stops []Stop
}

// maxGradientStops caps the ramp. Anything past a handful of stops is banding
// a designer cannot see and a decoder does not care about, and an unbounded
// list is a cheap way to make an SVG document enormous.
const maxGradientStops = 16

// ParseGradient reads the compact gradient syntax used by the API.
//
// Accepted forms:
//
//	linear(45deg,#000,#00f)
//	linear(#000 0%,#888 50%,#fff 100%)
//	radial(#000,#333)
//
// The angle is optional and defaults to 90 degrees, a top-to-bottom sweep.
// Offsets are optional and are distributed evenly when omitted; a stop may
// carry a percentage or a 0..1 fraction.
func ParseGradient(s string) (*Gradient, error) {
	raw := strings.TrimSpace(s)
	open := strings.Index(raw, "(")
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return nil, fmt.Errorf(
			"%w: gradient %q: expected linear(...) or radial(...)", ErrInvalidStyle, s)
	}

	var g Gradient
	switch kind := strings.ToLower(strings.TrimSpace(raw[:open])); kind {
	case string(GradientLinear):
		g.Kind, g.Angle = GradientLinear, 90
	case string(GradientRadial):
		g.Kind = GradientRadial
	default:
		return nil, fmt.Errorf(
			"%w: gradient kind %q: expected linear or radial", ErrInvalidStyle, kind)
	}

	args := splitArgs(raw[open+1 : len(raw)-1])
	if len(args) > 0 && g.Kind == GradientLinear {
		if angle, ok := parseAngle(args[0]); ok {
			g.Angle = angle
			args = args[1:]
		}
	}

	if len(args) < 2 {
		return nil, fmt.Errorf(
			"%w: gradient %q: expected at least two colour stops, got %d",
			ErrInvalidStyle, s, len(args))
	}
	if len(args) > maxGradientStops {
		return nil, fmt.Errorf("%w: gradient has %d stops, the limit is %d",
			ErrInvalidStyle, len(args), maxGradientStops)
	}

	stops, err := parseStops(args)
	if err != nil {
		return nil, err
	}
	g.Stops = stops
	return &g, nil
}

// splitArgs splits a comma-separated argument list, dropping empty entries so
// that a trailing comma is tolerated rather than becoming a phantom stop.
func splitArgs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseAngle recognises the leading "45deg" or bare "45" of a linear gradient.
// It reports false for anything else so that the token falls through to the
// colour parser: "linear(#000,#fff)" has no angle at all. No colour syntax
// parses as a float, so the two cannot be confused.
func parseAngle(s string) (float64, bool) {
	tok := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "deg")
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return math.Mod(math.Mod(v, 360)+360, 360), true
}

// parseStops turns the colour arguments into stops, filling in any offsets the
// caller left out.
func parseStops(args []string) ([]Stop, error) {
	stops := make([]Stop, len(args))
	explicit := make([]bool, len(args))

	for i, arg := range args {
		fields := strings.Fields(arg)
		c, err := ParseColor(fields[0])
		if err != nil {
			return nil, fmt.Errorf("gradient stop %d: %w", i+1, err)
		}
		stops[i].Color = c

		switch len(fields) {
		case 1:
		case 2:
			off, err := parseOffset(fields[1])
			if err != nil {
				return nil, fmt.Errorf("gradient stop %d: %w", i+1, err)
			}
			stops[i].Offset, explicit[i] = off, true
		default:
			return nil, fmt.Errorf(
				"%w: gradient stop %d: expected \"colour\" or \"colour offset\", got %q",
				ErrInvalidStyle, i+1, arg)
		}
	}

	// Even distribution is what a caller who wrote no offsets means, and it is
	// also what CSS does.
	for i := range stops {
		if !explicit[i] {
			stops[i].Offset = float64(i) / float64(len(stops)-1)
		}
	}
	for i := 1; i < len(stops); i++ {
		if stops[i].Offset < stops[i-1].Offset {
			return nil, fmt.Errorf(
				"%w: gradient stop %d has offset %g, behind the previous stop's %g",
				ErrInvalidStyle, i+1, stops[i].Offset, stops[i-1].Offset)
		}
	}
	return stops, nil
}

// parseOffset accepts "50%" or "0.5".
func parseOffset(s string) (float64, error) {
	tok := strings.TrimSpace(s)
	scale := 1.0
	if strings.HasSuffix(tok, "%") {
		tok, scale = strings.TrimSuffix(tok, "%"), 0.01
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%w: offset %q: expected a percentage or a 0..1 fraction",
			ErrInvalidStyle, s)
	}
	v *= scale
	if v < 0 || v > 1 {
		return 0, fmt.Errorf("%w: offset %q is outside 0..100%%", ErrInvalidStyle, s)
	}
	return v, nil
}

// validate reports whether the gradient can be sampled at all.
func (g *Gradient) validate() error {
	if g == nil {
		return nil
	}
	if g.Kind != GradientLinear && g.Kind != GradientRadial {
		return fmt.Errorf("%w: gradient kind %q: expected linear or radial",
			ErrInvalidStyle, g.Kind)
	}
	if len(g.Stops) < 2 {
		return fmt.Errorf("%w: gradient needs at least two colour stops, got %d",
			ErrInvalidStyle, len(g.Stops))
	}
	if len(g.Stops) > maxGradientStops {
		return fmt.Errorf("%w: gradient has %d stops, the limit is %d",
			ErrInvalidStyle, len(g.Stops), maxGradientStops)
	}
	return nil
}

// ColorAt samples the gradient at module (x, y) of a cols by rows area.
//
// Coordinates are module-space and the sample is taken at the module's centre,
// so every pixel of a module gets one flat colour. Sampling per pixel instead
// would put a ramp across each module and give a scanner's binariser a
// gradient to threshold inside the very unit it is trying to classify.
func (g *Gradient) ColorAt(x, y, cols, rows int) color.NRGBA {
	if g == nil || len(g.Stops) == 0 {
		return color.NRGBA{}
	}
	if len(g.Stops) == 1 || cols <= 0 || rows <= 0 {
		return g.Stops[0].Color
	}

	w, h := float64(cols), float64(rows)
	px, py := float64(x)+0.5, float64(y)+0.5

	var t float64
	if g.Kind == GradientRadial {
		cx, cy := w/2, h/2
		if d := math.Hypot(cx, cy); d > 0 {
			t = math.Hypot(px-cx, py-cy) / d
		}
	} else {
		rad := g.Angle * math.Pi / 180
		dx, dy := math.Cos(rad), math.Sin(rad)
		// Normalise over the projection of the whole area onto the sweep
		// direction, so the ramp spans the symbol exactly at every angle.
		lo := min(0, w*dx) + min(0, h*dy)
		hi := max(0, w*dx) + max(0, h*dy)
		if hi > lo {
			t = (px*dx + py*dy - lo) / (hi - lo)
		}
	}
	return g.sample(clamp01(t))
}

// sample interpolates the stop list at t.
func (g *Gradient) sample(t float64) color.NRGBA {
	if t <= g.Stops[0].Offset {
		return g.Stops[0].Color
	}
	last := g.Stops[len(g.Stops)-1]
	if t >= last.Offset {
		return last.Color
	}
	for i := 1; i < len(g.Stops); i++ {
		a, b := g.Stops[i-1], g.Stops[i]
		if t > b.Offset {
			continue
		}
		span := b.Offset - a.Offset
		if span <= 0 {
			return b.Color
		}
		return lerpColor(a.Color, b.Color, (t-a.Offset)/span)
	}
	return last.Color
}

// lerpColor blends two colours in straight (non-premultiplied) sRGB.
//
// Blending straight rather than premultiplied is what a designer expects from
// a gradient editor, and codes are drawn opaque in practice, so the usual
// premultiplied-alpha argument does not bite here.
func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5)
	}
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}

// Darkest returns the stop with the highest contrast against bg, and Lightest
// the lowest. Together they bound what the symbol will look like to a camera:
// if even the darkest stop is washed out the code is dead, and if the lightest
// stop disappears then part of the symbol does too.
func (g *Gradient) Darkest(bg color.NRGBA) (Stop, float64) {
	return g.extremeStop(bg, true)
}

// Lightest returns the stop with the least contrast against bg.
func (g *Gradient) Lightest(bg color.NRGBA) (Stop, float64) {
	return g.extremeStop(bg, false)
}

func (g *Gradient) extremeStop(bg color.NRGBA, wantMax bool) (Stop, float64) {
	if g == nil || len(g.Stops) == 0 {
		return Stop{}, 0
	}
	best, ratio := g.Stops[0], ContrastRatio(g.Stops[0].Color, bg)
	for _, s := range g.Stops[1:] {
		r := ContrastRatio(s.Color, bg)
		if (wantMax && r > ratio) || (!wantMax && r < ratio) {
			best, ratio = s, r
		}
	}
	return best, ratio
}

// SVGDef renders the gradient as an SVG element for a <defs> block, referenced
// by a fill of "url(#id)".
//
// The id is sanitised rather than trusted: it ends up unquoted inside an
// attribute, and a writer that derived it from a request field would otherwise
// hand the caller an XML injection.
func (g *Gradient) SVGDef(id string) string {
	if g == nil || len(g.Stops) == 0 {
		return ""
	}
	safe := sanitiseID(id)

	var b strings.Builder
	if g.Kind == GradientRadial {
		// The radius is the half-diagonal of the bounding box, matching how
		// ColorAt normalises distance; the SVG default of 50% would stop the
		// ramp short of the corners and disagree with the rasteriser.
		fmt.Fprintf(&b,
			`<radialGradient id="%s" cx="50%%" cy="50%%" r="70.711%%">`, safe)
	} else {
		x1, y1, x2, y2 := g.linearEndpoints()
		fmt.Fprintf(&b,
			`<linearGradient id="%s" x1="%s" y1="%s" x2="%s" y2="%s">`,
			safe, fnum(x1), fnum(y1), fnum(x2), fnum(y2))
	}

	for _, s := range g.Stops {
		fmt.Fprintf(&b, `<stop offset="%s" stop-color="#%02x%02x%02x"`,
			fnum(round3(s.Offset)), s.Color.R, s.Color.G, s.Color.B)
		if s.Color.A != 0xFF {
			fmt.Fprintf(&b, ` stop-opacity="%s"`, fnum(round3(float64(s.Color.A)/255)))
		}
		b.WriteString("/>")
	}

	if g.Kind == GradientRadial {
		b.WriteString("</radialGradient>")
	} else {
		b.WriteString("</linearGradient>")
	}
	return b.String()
}

// linearEndpoints places the gradient line across the unit bounding box for
// the configured angle, in objectBoundingBox units.
func (g *Gradient) linearEndpoints() (x1, y1, x2, y2 float64) {
	rad := g.Angle * math.Pi / 180
	dx, dy := math.Cos(rad), math.Sin(rad)
	// Stretch the line until it meets the edge of the box, so the ramp spans
	// the whole symbol rather than fading out early on a diagonal.
	if k := max(math.Abs(dx), math.Abs(dy)); k > 0 {
		dx, dy = dx/k, dy/k
	}
	return round3(0.5 - dx/2), round3(0.5 - dy/2), round3(0.5 + dx/2), round3(0.5 + dy/2)
}

// round3 trims a coordinate to three decimals: enough for a gradient, and it
// keeps the emitted document free of float noise like 0.49999999999999994.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// sanitiseID reduces an identifier to the characters that are safe unquoted in
// an XML attribute and legal at the start of an XML name.
func sanitiseID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "g" + out
	}
	return out
}
