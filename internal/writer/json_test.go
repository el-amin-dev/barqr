package writer

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
)

// decodeJSON writes the canvas and unmarshals the result into T.
func decodeJSON[T any](t *testing.T, c render.Canvas) T {
	t.Helper()

	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got T
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// assertRect compares an emitted rectangle against the canvas geometry it is
// supposed to describe.
func assertRect(t *testing.T, what string, got jsonRect, want image.Rectangle) {
	t.Helper()

	expect := jsonRect{X: want.Min.X, Y: want.Min.Y, Cols: want.Dx(), Rows: want.Dy()}
	if got != expect {
		t.Errorf("%s = %+v, want %+v", what, got, expect)
	}
}

func TestJSONWriterIsRegisteredAsText(t *testing.T) {
	t.Parallel()

	w, err := Get(JSON)
	if err != nil {
		t.Fatalf("Get(json): %v", err)
	}
	if w.MIME() != "application/json" || w.Extension() != "json" || w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestJSONWriteDescribesTheCanvas(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("output does not end with a newline")
	}

	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Symbology != encoder.QR || got.Kind != string(encoder.Kind2D) {
		t.Errorf("symbology/kind = %q/%q, want %q/%q",
			got.Symbology, got.Kind, encoder.QR, encoder.Kind2D)
	}
	if got.Cols != c.Cols || got.Rows != c.Rows || got.QuietZone != c.QuietZone {
		t.Errorf("geometry = %dx%d quiet %d, want %dx%d quiet %d",
			got.Cols, got.Rows, got.QuietZone, c.Cols, c.Rows, c.QuietZone)
	}
	if got.FG != "#000000" || got.BG != "#ffffff" {
		t.Errorf("colours = %q on %q, want #000000 on #ffffff", got.FG, got.BG)
	}
	if got.HRI != "" {
		t.Errorf("hri = %q, want empty for a QR symbol", got.HRI)
	}

	if len(got.Modules) != c.Rows {
		t.Fatalf("%d module rows, want %d", len(got.Modules), c.Rows)
	}
	for y, row := range got.Modules {
		if len(row) != c.Cols {
			t.Fatalf("row %d is %d characters, want %d", y, len(row), c.Cols)
		}
		for x := range c.Cols {
			want := byte('0')
			if c.At(x, y) {
				want = '1'
			}
			if row[x] != want {
				t.Fatalf("module (%d,%d) = %q, want %q", x, y, row[x], want)
			}
		}
	}
}

func TestJSONWriteCarriesTheHumanReadableText(t *testing.T) {
	t.Parallel()

	c := rasterLinear(t, render.DefaultStyle(), "1234567890")
	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HRI != "1234567890" {
		t.Errorf("hri = %q, want 1234567890", got.HRI)
	}
	if got.Kind != string(encoder.Kind1D) {
		t.Errorf("kind = %q, want %q", got.Kind, encoder.Kind1D)
	}
}

// TestJSONWriteKeepsTheKeysClientsAlreadyParse is a compatibility gate, not a
// restatement of the struct tags.
//
// Every key below shipped before the geometry sections were added, so a client
// written against that document must keep working. Renaming or dropping one is
// a breaking change and has to fail here rather than in someone's app.
func TestJSONWriteKeepsTheKeysClientsAlreadyParse(t *testing.T) {
	t.Parallel()

	c := rasterLinear(t, render.DefaultStyle(), "1234567890")
	got := decodeJSON[map[string]json.RawMessage](t, c)

	for _, key := range []string{
		"symbology", "kind", "cols", "rows", "quiet_zone", "fg", "bg", "hri", "modules",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q is gone from the document", key)
		}
	}
}

// TestJSONWriteOmitsDecorationTheStyleDidNotAskFor keeps an absent logo absent.
// A zero rectangle would be indistinguishable from a logo squeezed to nothing,
// and a client cannot tell those apart after the fact.
func TestJSONWriteOmitsDecorationTheStyleDidNotAskFor(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	got := decodeJSON[map[string]json.RawMessage](t, c)

	for _, key := range []string{"logo", "frame", "caption", "gradient"} {
		if _, ok := got[key]; ok {
			t.Errorf("key %q is present on a canvas with no %s", key, key)
		}
	}
	// The symbol is not decoration: it always exists, so it is always reported.
	if _, ok := got["symbol"]; !ok {
		t.Error("key \"symbol\" is missing")
	}
}

// TestJSONWriteReportsTheSymbolInsideItsMargins pins the field a client needs
// most: the code's own bounds, which are not the canvas bounds as soon as a
// quiet zone or a frame is in play.
func TestJSONWriteReportsTheSymbolInsideItsMargins(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	got := decodeJSON[jsonCanvas](t, c)

	assertRect(t, "symbol", got.Symbol, c.SymbolRect())
	if got.Symbol.X != c.QuietZone || got.Symbol.Y != c.QuietZone {
		t.Errorf("symbol origin = (%d,%d), want the quiet zone %d on both axes",
			got.Symbol.X, got.Symbol.Y, c.QuietZone)
	}
	if got.Symbol.Cols != c.Cols-2*c.QuietZone {
		t.Errorf("symbol is %d columns wide, want %d", got.Symbol.Cols, c.Cols-2*c.QuietZone)
	}
}

// TestJSONWriteAcceptsWhatTheTerminalFormatsRefuse is the mirror of
// assertRefusesDecorations: json is held to the same three cases, and must
// accept every one of them because it describes the decoration instead of
// drawing it.
func TestJSONWriteAcceptsWhatTheTerminalFormatsRefuse(t *testing.T) {
	t.Parallel()

	for _, d := range decorations(t) {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			got := decodeJSON[map[string]json.RawMessage](t, rasterQR(t, d.style))
			if _, ok := got[d.name]; !ok {
				t.Fatalf("a canvas with a %s produced a document without a %q section",
					d.name, d.name)
			}
		})
	}
}

// TestJSONWriteDescribesEveryDecoration checks the geometry against the canvas
// it came from, with all four decorations at once — the arrangement where the
// rectangles actually interact, because a frame and a caption band both move
// the symbol and therefore the logo centred on it.
func TestJSONWriteDescribesEveryDecoration(t *testing.T) {
	t.Parallel()

	s := gradientStyle()
	img, err := render.ParseLogo(tinyLogoURI(t))
	if err != nil {
		t.Fatalf("ParseLogo: %v", err)
	}
	s.Logo = &render.Logo{Image: img, Scale: render.DefaultLogoScale, Excavate: true}
	s.Frame = &render.Frame{
		Kind:         render.FrameRounded,
		Width:        3,
		Color:        color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF},
		Caption:      "SCAN ME",
		CaptionColor: color.NRGBA{R: 0x44, G: 0x55, B: 0x66, A: 0x80},
	}

	c := rasterQR(t, s)
	got := decodeJSON[jsonCanvas](t, c)

	assertRect(t, "symbol", got.Symbol, c.SymbolRect())

	logo, ok := c.LogoRect()
	if !ok {
		t.Fatal("the canvas reserved no logo")
	}
	if got.Logo == nil {
		t.Fatal("logo section is missing")
	}
	assertRect(t, "logo", *got.Logo, logo)

	frame, ok := c.FrameRect()
	if !ok {
		t.Fatal("the canvas reserved no frame")
	}
	if got.Frame == nil {
		t.Fatal("frame section is missing")
	}
	assertRect(t, "frame", got.Frame.jsonRect, frame)
	if got.Frame.Kind != render.FrameRounded || got.Frame.Width != 3 {
		t.Errorf("frame kind/width = %q/%d, want %q/3",
			got.Frame.Kind, got.Frame.Width, render.FrameRounded)
	}
	if got.Frame.Color != "#112233" {
		t.Errorf("frame colour = %q, want #112233", got.Frame.Color)
	}

	caption, ok := c.CaptionRect()
	if !ok {
		t.Fatal("the canvas reserved no caption band")
	}
	if got.Caption == nil {
		t.Fatal("caption section is missing")
	}
	assertRect(t, "caption", got.Caption.jsonRect, caption)
	if got.Caption.Text != "SCAN ME" {
		t.Errorf("caption text = %q, want SCAN ME", got.Caption.Text)
	}
	// A translucent colour keeps its alpha, exactly as fg and bg do.
	if got.Caption.Color != "#44556680" {
		t.Errorf("caption colour = %q, want #44556680", got.Caption.Color)
	}

	if got.Gradient == nil {
		t.Fatal("gradient section is missing")
	}
	want := jsonGradient{
		Kind:  string(render.GradientLinear),
		Angle: 90,
		Stops: []jsonStop{{Offset: 0, Color: "#ff0000"}, {Offset: 1, Color: "#0000ff"}},
	}
	if got.Gradient.Kind != want.Kind || got.Gradient.Angle != want.Angle ||
		!slices.Equal(got.Gradient.Stops, want.Stops) {
		t.Errorf("gradient = %+v, want %+v", *got.Gradient, want)
	}
}

// TestJSONWriteResolvesAnUnsetFrameWidthAndColour reports what the renderer
// actually reserved and what the other writers actually paint, not the zero
// values the caller left behind. A client stroking a zero-width border would
// leave the reservation empty and an invisible transparent one is worse.
func TestJSONWriteResolvesAnUnsetFrameWidthAndColour(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.FG = color.NRGBA{R: 0x0a, G: 0x0b, B: 0x0c, A: 0xFF}
	s.Frame = &render.Frame{Kind: render.FrameBorder}
	s.Caption = "TOP LEVEL"

	got := decodeJSON[jsonCanvas](t, rasterQR(t, s))
	if got.Frame == nil || got.Caption == nil {
		t.Fatal("frame or caption section is missing")
	}
	if got.Frame.Width != render.DefaultFrameWidth {
		t.Errorf("frame width = %d, want the default %d",
			got.Frame.Width, render.DefaultFrameWidth)
	}
	if got.Frame.Color != "#0a0b0c" || got.Caption.Color != "#0a0b0c" {
		t.Errorf("frame/caption colour = %q/%q, want the foreground #0a0b0c",
			got.Frame.Color, got.Caption.Color)
	}
	// Style.Caption is the fallback the frame's own caption overrides.
	if got.Caption.Text != "TOP LEVEL" {
		t.Errorf("caption text = %q, want TOP LEVEL", got.Caption.Text)
	}
}

func TestJSONWriteRecordsATranslucentColourWithItsAlpha(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.BG = render.Transparent
	c := rasterQR(t, s)

	out, err := jsonWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got jsonCanvas
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BG != "#00000000" {
		t.Errorf("bg = %q, want #00000000", got.BG)
	}
}
