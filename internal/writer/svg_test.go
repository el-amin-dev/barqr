package writer

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

// svgDoc is enough of the SVG grammar to assert on what the writer emits.
// Decoding through encoding/xml is deliberate: it proves the document is
// well-formed as well as correct.
type svgDoc struct {
	XMLName xml.Name  `xml:"svg"`
	Width   string    `xml:"width,attr"`
	Height  string    `xml:"height,attr"`
	ViewBox string    `xml:"viewBox,attr"`
	Shape   string    `xml:"shape-rendering,attr"`
	Rects   []svgRect `xml:"rect"`
	Paths   []svgPath `xml:"path"`
	Texts   []svgText `xml:"text"`
}

type svgRect struct {
	Width  string `xml:"width,attr"`
	Height string `xml:"height,attr"`
	Fill   string `xml:"fill,attr"`
}

type svgPath struct {
	D        string `xml:"d,attr"`
	Fill     string `xml:"fill,attr"`
	Opacity  string `xml:"fill-opacity,attr"`
	FillRule string `xml:"fill-rule,attr"`
}

type svgText struct {
	Value  string `xml:",chardata"`
	Font   string `xml:"font-family,attr"`
	Size   string `xml:"font-size,attr"`
	Anchor string `xml:"text-anchor,attr"`
}

// parseSVG decodes the writer's output, failing the test if it is not
// well-formed XML.
func parseSVG(t *testing.T, out []byte) svgDoc {
	t.Helper()

	var doc svgDoc
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not well-formed XML: %v\n%s", err, out)
	}
	if doc.XMLName.Local != "svg" {
		t.Fatalf("root element is %q, want svg", doc.XMLName.Local)
	}
	if doc.XMLName.Space != "http://www.w3.org/2000/svg" {
		t.Errorf("root namespace is %q, want the SVG namespace", doc.XMLName.Space)
	}
	return doc
}

func TestSVGWriterIdentity(t *testing.T) {
	t.Parallel()

	w, err := Get(FormatSVG)
	if err != nil {
		t.Fatalf("Get(svg): %v", err)
	}
	if got := w.MIME(); got != "image/svg+xml" {
		t.Errorf("MIME = %q, want image/svg+xml", got)
	}
	if got := w.Extension(); got != "svg" {
		t.Errorf("Extension = %q, want svg", got)
	}
	if w.Binary() {
		t.Error("Binary = true, want false for an SVG")
	}
}

func TestSVGCarriesModuleViewBoxAndPixelSize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		scale int
	}{
		{name: "default scale", scale: 0},
		{name: "scale 4", scale: 4},
		{name: "scale 17", scale: 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "viewbox")
			o := vecTestOpts(FormatSVG)
			o.Scale = tc.scale

			out, err := svgWriter{}.Write(c, o)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			doc := parseSVG(t, out)

			w, h, _, err := PixelSize(c, o)
			if err != nil {
				t.Fatalf("PixelSize: %v", err)
			}
			if doc.Width != fmt.Sprint(w) || doc.Height != fmt.Sprint(h) {
				t.Errorf("width/height = %s/%s, want %d/%d", doc.Width, doc.Height, w, h)
			}
			if want := fmt.Sprintf("0 0 %d %d", c.Cols, c.Rows); doc.ViewBox != want {
				t.Errorf("viewBox = %q, want %q", doc.ViewBox, want)
			}
			// Anti-aliased module edges are a real cause of scan failures at
			// small sizes, so this attribute is not optional.
			if doc.Shape != "crispEdges" {
				t.Errorf("shape-rendering = %q, want crispEdges", doc.Shape)
			}
		})
	}
}

func TestSVGBackgroundFollowsAlpha(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		bg        color.NRGBA
		wantRects int
		wantFill  string
		wantAlpha string
	}{
		{
			name:      "opaque white is painted",
			bg:        color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			wantRects: 1,
			wantFill:  "#ffffff",
		},
		{
			name:      "translucent uses fill-opacity",
			bg:        color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x80},
			wantRects: 1,
			wantFill:  "#112233",
			wantAlpha: "0.502",
		},
		{name: "fully transparent is omitted", bg: render.Transparent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "background")
			c.Style.BG = tc.bg

			out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			doc := parseSVG(t, out)

			if len(doc.Rects) != tc.wantRects {
				t.Fatalf("rect count = %d, want %d", len(doc.Rects), tc.wantRects)
			}
			if tc.wantRects == 0 {
				return
			}
			r := doc.Rects[0]
			// #rrggbbaa is not honoured everywhere an SVG is consumed, so the
			// fill stays six digits and alpha rides on fill-opacity.
			if r.Fill != tc.wantFill {
				t.Errorf("background fill = %q, want %q", r.Fill, tc.wantFill)
			}
			if r.Width != fmt.Sprint(c.Cols) || r.Height != fmt.Sprint(c.Rows) {
				t.Errorf("background is %sx%s, want %dx%d", r.Width, r.Height, c.Cols, c.Rows)
			}
			if got := svgAttr(string(out), "fill-opacity"); got != tc.wantAlpha {
				t.Errorf("fill-opacity = %q, want %q", got, tc.wantAlpha)
			}
		})
	}
}

// svgAttr returns the value of the first occurrence of an attribute, or "".
func svgAttr(out, name string) string {
	key := name + `="`
	i := strings.Index(out, key)
	if i < 0 {
		return ""
	}
	rest := out[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestSVGEyeOriginsAgreeWithCanvasRoles(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "finder patterns")
	origins := svgEyeOrigins(c)
	if len(origins) != 3 {
		t.Fatalf("found %d finder patterns, want 3", len(origins))
	}

	for _, e := range origins {
		for dy := range svgEyeModules {
			for dx := range svgEyeModules {
				got := c.Role(e[0]+dx, e[1]+dy)
				inBall := dx >= svgEyeBallOffset && dx < svgEyeBallOffset+3 &&
					dy >= svgEyeBallOffset && dy < svgEyeBallOffset+3
				want := render.RoleEyeFrame
				if inBall {
					want = render.RoleEyeBall
				}
				if got != want {
					t.Fatalf("role at (%d, %d) = %v, want %v", e[0]+dx, e[1]+dy, got, want)
				}
			}
		}
	}
}

func TestSVGPaintsEyesAsWholeShapes(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "eyes")
	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc := parseSVG(t, out)

	if len(doc.Paths) != 3 {
		t.Fatalf("path count = %d, want modules, eye frames and eye balls", len(doc.Paths))
	}

	frames := doc.Paths[1]
	// The square frame is an outer square containing an inner one; only the
	// even-odd rule leaves the ring hollow, and a solid eye does not scan.
	if frames.FillRule != "evenodd" {
		t.Errorf("eye frame fill-rule = %q, want evenodd", frames.FillRule)
	}
	if got := strings.Count(frames.D, "M"); got != 6 {
		t.Errorf("eye frame path has %d subpaths, want 6 (three rings of two)", got)
	}
	if got := strings.Count(doc.Paths[2].D, "M"); got != 3 {
		t.Errorf("eye ball path has %d subpaths, want 3", got)
	}

	// Eye modules belong to the eye paths only; drawing them twice would
	// double-fill and defeat a hollow or translucent eye shape.
	origins := svgEyeOrigins(c)
	for _, e := range origins {
		frag := fmt.Sprintf("M%d %dh1v1h-1z", e[0], e[1])
		if strings.Contains(doc.Paths[0].D, frag) {
			t.Errorf("module path also draws the eye module at (%d, %d)", e[0], e[1])
		}
	}
}

func TestSVGHRIIsXMLEscaped(t *testing.T) {
	t.Parallel()

	const hostile = `A(B)C\ <script>alert("x")&</script>`

	c := vecTestCanvas(t, "hri")
	c.HRI = hostile

	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if bytes.Contains(out, []byte("<script>")) {
		t.Fatalf("raw markup survived into the document:\n%s", out)
	}

	doc := parseSVG(t, out)
	if len(doc.Texts) != 1 {
		t.Fatalf("text element count = %d, want 1", len(doc.Texts))
	}
	if doc.Texts[0].Value != hostile {
		t.Errorf("text content = %q, want %q", doc.Texts[0].Value, hostile)
	}
	if doc.Texts[0].Font != "monospace" {
		t.Errorf("font-family = %q, want monospace", doc.Texts[0].Font)
	}
	if doc.Texts[0].Anchor != "middle" {
		t.Errorf("text-anchor = %q, want middle", doc.Texts[0].Anchor)
	}
	if doc.Texts[0].Size != svgNum(hriFontModules(c)) {
		t.Errorf("font-size = %q, want %q", doc.Texts[0].Size, svgNum(hriFontModules(c)))
	}

	// The band is added below the code, in both the viewBox and the pixel size.
	if want := fmt.Sprintf("0 0 %d %s", c.Cols,
		svgNum(float64(c.Rows)+hriBandModules(c))); doc.ViewBox != want {
		t.Errorf("viewBox = %q, want %q with room for the text", doc.ViewBox, want)
	}
}

func TestSVGRejectsUnknownShapes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(*render.Canvas)
	}{
		{name: "module shape", mutate: func(c *render.Canvas) { c.Style.Module = "no-such-shape" }},
		{name: "eye shape", mutate: func(c *render.Canvas) { c.Style.Eye = "no-such-shape" }},
		{name: "eye ball shape", mutate: func(c *render.Canvas) { c.Style.EyeBall = "no-such-shape" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "shapes")
			tc.mutate(&c)

			_, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
			if !errors.Is(err, ErrInvalidOutput) {
				t.Errorf("Write error = %v, want ErrInvalidOutput", err)
			}
			if !errors.Is(err, render.ErrUnknownShape) {
				t.Errorf("Write error = %v, want it to wrap render.ErrUnknownShape", err)
			}
		})
	}
}

func TestSVGPropagatesMaxPixels(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "limits")
	o := vecTestOpts(FormatSVG)
	o.MaxPixels = 1000

	if _, err := (svgWriter{}).Write(c, o); !errors.Is(err, ErrCanvasTooLarge) {
		t.Errorf("Write error = %v, want ErrCanvasTooLarge", err)
	}
}

func TestSVGSkipsEyePaintingWithoutFinderPatterns(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "no eyes")
	// A canvas whose quiet zone leaves no room for a finder pattern must not
	// get eye shapes painted over live data.
	c.QuietZone = c.Cols

	if got := svgEyeOrigins(c); got != nil {
		t.Errorf("svgEyeOrigins = %v, want nil for a canvas with no room for eyes", got)
	}

	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc := parseSVG(t, out)
	if len(doc.Paths) != 1 {
		t.Errorf("path count = %d, want only the module path", len(doc.Paths))
	}
}

func TestSVGEmitsNoPathWhenNothingIsDark(t *testing.T) {
	t.Parallel()

	p, err := render.ModuleShape(render.ShapeSquare)
	if err != nil {
		t.Fatalf("ModuleShape: %v", err)
	}

	// An empty <path d=""/> is legal but pointless, and a d attribute that is
	// merely whitespace trips some older consumers.
	var b bytes.Buffer
	svgModulePath(&b, render.Canvas{}, p, false)
	if b.Len() != 0 {
		t.Errorf("wrote %q for a canvas with no modules, want nothing", b.String())
	}
}

// svgDecorated is the second half of the grammar: the elements the decoration
// pass emits. It is kept separate from svgDoc so that the assertions on a plain
// code and the assertions on a decorated one cannot drift into each other.
type svgDecorated struct {
	XMLName xml.Name  `xml:"svg"`
	Defs    svgDefs   `xml:"defs"`
	Paths   []svgPath `xml:"path"`
	Images  []svgImg  `xml:"image"`
	Texts   []svgText `xml:"text"`
}

type svgDefs struct {
	Linear []svgGradientDef `xml:"linearGradient"`
	Radial []svgGradientDef `xml:"radialGradient"`
}

type svgGradientDef struct {
	ID    string `xml:"id,attr"`
	Stops []struct {
		Offset string `xml:"offset,attr"`
		Color  string `xml:"stop-color,attr"`
	} `xml:"stop"`
}

type svgImg struct {
	X        string `xml:"x,attr"`
	Y        string `xml:"y,attr"`
	Width    string `xml:"width,attr"`
	Height   string `xml:"height,attr"`
	Preserve string `xml:"preserveAspectRatio,attr"`
	Href     string `xml:"href,attr"`
}

// parseDecoratedSVG decodes the writer's output, failing the test if it is not
// well-formed XML.
func parseDecoratedSVG(t *testing.T, out []byte) svgDecorated {
	t.Helper()

	var doc svgDecorated
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not well-formed XML: %v\n%s", err, out)
	}
	return doc
}

// svgStyled renders the raster tests' QR through a style, so that both writers
// are asserted against the same canvas.
func svgStyled(t *testing.T, s render.Style) render.Canvas {
	t.Helper()
	return rasterQR(t, s)
}

func TestSVGEmitsAGradientDefinitionAndReferencesIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		kind     render.GradientKind
		wantElem string
	}{
		{name: "linear", kind: render.GradientLinear, wantElem: "<linearGradient"},
		{name: "radial", kind: render.GradientRadial, wantElem: "<radialGradient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Gradient = &render.Gradient{
				Kind: tc.kind,
				Stops: []render.Stop{
					{Offset: 0, Color: color.NRGBA{A: 0xFF}},
					{Offset: 1, Color: color.NRGBA{R: 0xFF, A: 0xFF}},
				},
			}
			c := svgStyled(t, s)

			out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if !bytes.Contains(out, []byte(tc.wantElem)) {
				t.Fatalf("no %s in the document:\n%s", tc.wantElem, out)
			}

			doc := parseDecoratedSVG(t, out)
			defs := append(append([]svgGradientDef{}, doc.Defs.Linear...), doc.Defs.Radial...)
			if len(defs) != 1 {
				t.Fatalf("gradient definition count = %d, want 1", len(defs))
			}
			if defs[0].ID != svgGradientID {
				t.Errorf("gradient id = %q, want %q", defs[0].ID, svgGradientID)
			}
			if len(defs[0].Stops) != 2 {
				t.Errorf("stop count = %d, want 2", len(defs[0].Stops))
			}

			// A definition nothing points at renders flat, which is exactly the
			// bug this replaces.
			want := "url(#" + svgGradientID + ")"
			if doc.Paths[0].Fill != want {
				t.Errorf("module path fill = %q, want %q", doc.Paths[0].Fill, want)
			}
			// The finder patterns keep a flat colour: a ramp running light
			// across an eye costs the scanner its landmark.
			for i, p := range doc.Paths[1:] {
				if strings.HasPrefix(p.Fill, "url(") {
					t.Errorf("eye path %d is filled with %q, want a flat colour", i, p.Fill)
				}
			}
		})
	}
}

func TestSVGWithoutAGradientStaysFlat(t *testing.T) {
	t.Parallel()

	c := svgStyled(t, render.DefaultStyle())
	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if bytes.Contains(out, []byte("<defs>")) {
		t.Errorf("a plain code carries a <defs> block:\n%s", out)
	}
	if bytes.Contains(out, []byte("url(#")) {
		t.Error("a plain code references a paint server")
	}
}

func TestSVGDrawsEveryFrameKind(t *testing.T) {
	t.Parallel()

	frameInk := color.NRGBA{R: 0xE0, B: 0x90, A: 0xFF}

	for _, tc := range []struct {
		name      string
		kind      string
		wantPaths int
		wantArc   bool
	}{
		// Three paths are the undecorated baseline: modules, eye frames, eye
		// balls. Everything above that is the frame.
		{name: "none draws nothing", kind: render.FrameNone, wantPaths: 3},
		{name: "border is one ring", kind: render.FrameBorder, wantPaths: 4},
		{name: "rounded is one ring with arcs", kind: render.FrameRounded, wantPaths: 4, wantArc: true},
		{name: "banner adds the caption bar", kind: render.FrameBanner, wantPaths: 5},
		{name: "bubble adds the bar and a tail", kind: render.FrameBubble, wantPaths: 6, wantArc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Frame = &render.Frame{
				Kind:         tc.kind,
				Color:        frameInk,
				Width:        3,
				Caption:      "BARQR",
				CaptionColor: color.NRGBA{G: 0x90, B: 0x40, A: 0xFF},
			}
			c := svgStyled(t, s)

			out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			doc := parseDecoratedSVG(t, out)

			if len(doc.Paths) != tc.wantPaths {
				t.Fatalf("path count = %d, want %d", len(doc.Paths), tc.wantPaths)
			}
			if tc.kind == render.FrameNone {
				return
			}

			ring := doc.Paths[0]
			if ring.Fill != render.HexColor(frameInk) {
				t.Errorf("frame fill = %q, want %q", ring.Fill, render.HexColor(frameInk))
			}
			// The ring is an outer boundary plus the hole it leaves; only the
			// even-odd rule keeps the middle empty, and a solid frame would
			// bury the code.
			if ring.FillRule != "evenodd" {
				t.Errorf("frame fill-rule = %q, want evenodd", ring.FillRule)
			}
			if got := strings.Count(ring.D, "M"); got != 2 {
				t.Errorf("frame path has %d subpaths, want 2", got)
			}
			if got := strings.Contains(ring.D, "A"); got != tc.wantArc {
				t.Errorf("frame path contains an arc = %v, want %v", got, tc.wantArc)
			}
		})
	}
}

func TestSVGKeepsTheFrameOutOfTheQuietZone(t *testing.T) {
	t.Parallel()

	// The geometry, not the ink, is what has to be checked here: an SVG is not
	// rasterised in this test, so the assertion is that the frame's hole is
	// never smaller than the margin the decoder needs.
	for _, kind := range []string{
		render.FrameBorder, render.FrameRounded, render.FrameBanner, render.FrameBubble,
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Frame = &render.Frame{Kind: kind, Color: color.NRGBA{A: 0xFF}, Width: 4, Caption: "BARQR"}
			c := svgStyled(t, s)

			f, ok := resolveFrame(c)
			if !ok {
				t.Fatal("resolveFrame reported nothing to draw")
			}

			quiet := c.SymbolRect().Inset(-c.QuietZone)
			if !quiet.In(f.inner) {
				t.Errorf("the frame's hole %v does not contain the quiet zone %v", f.inner, quiet)
			}
			if f.band.Overlaps(quiet) {
				t.Errorf("the caption bar %v overlaps the quiet zone %v", f.band, quiet)
			}
			if f.tail.Overlaps(quiet) {
				t.Errorf("the bubble tail %v overlaps the quiet zone %v", f.tail, quiet)
			}
			// A radius larger than the thickness would round the hole too, and
			// the bulge would land in the quiet zone's corners.
			if f.radius > 0 && f.inner != f.outer.Inset(4) {
				t.Errorf("the hole is %v, want the canvas inset by the frame width", f.inner)
			}
		})
	}
}

func TestSVGEmbedsTheLogoAsADataURI(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.Logo = &render.Logo{
		Image:    solidImage(color.NRGBA{R: 0x22, G: 0x88, B: 0xEE, A: 0xFF}, 24, 24),
		Scale:    0.3,
		Excavate: true,
	}
	c := svgStyled(t, s)

	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc := parseDecoratedSVG(t, out)

	if len(doc.Images) != 1 {
		t.Fatalf("image count = %d, want 1", len(doc.Images))
	}
	img := doc.Images[0]

	rect, ok := c.LogoRect()
	if !ok {
		t.Fatal("LogoRect reported no logo")
	}
	if img.X != fmt.Sprint(rect.Min.X) || img.Y != fmt.Sprint(rect.Min.Y) {
		t.Errorf("image at %s,%s, want %d,%d", img.X, img.Y, rect.Min.X, rect.Min.Y)
	}
	if img.Width != fmt.Sprint(rect.Dx()) || img.Height != fmt.Sprint(rect.Dy()) {
		t.Errorf("image is %sx%s, want %dx%d", img.Width, img.Height, rect.Dx(), rect.Dy())
	}
	// Anything but "meet" distorts the artwork or crops it.
	if img.Preserve != "xMidYMid meet" {
		t.Errorf("preserveAspectRatio = %q, want %q", img.Preserve, "xMidYMid meet")
	}
	// A referenced file would make the document a fetch rather than a picture.
	if !strings.HasPrefix(img.Href, "data:image/png;base64,") {
		t.Errorf("href = %.40q, want an inline PNG data URI", img.Href)
	}
	if _, err := base64.StdEncoding.DecodeString(
		strings.TrimPrefix(img.Href, "data:image/png;base64,")); err != nil {
		t.Errorf("the embedded payload is not valid base64: %v", err)
	}
}

func TestSVGOmitsTheLogoWhenThereIsNoImage(t *testing.T) {
	t.Parallel()

	// Geometry-only logos are legal: the caller wants the space reserved and
	// will place the artwork itself.
	s := render.DefaultStyle()
	s.Logo = &render.Logo{Scale: 0.2, Excavate: true}
	c := svgStyled(t, s)

	out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := len(parseDecoratedSVG(t, out).Images); got != 0 {
		t.Errorf("image count = %d, want 0", got)
	}
}

func TestSVGCaptionIsXMLEscaped(t *testing.T) {
	t.Parallel()

	const hostile = `A<b>&"'</b>`

	for _, tc := range []struct {
		name  string
		style func(*render.Style)
	}{
		{
			name: "on a frame",
			style: func(s *render.Style) {
				s.Frame = &render.Frame{
					Kind:         render.FrameBanner,
					Color:        color.NRGBA{A: 0xFF},
					Width:        2,
					Caption:      hostile,
					CaptionColor: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
				}
			},
		},
		{
			name:  "on the style alone",
			style: func(s *render.Style) { s.Caption = hostile },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			tc.style(&s)
			c := svgStyled(t, s)

			out, err := svgWriter{}.Write(c, vecTestOpts(FormatSVG))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if bytes.Contains(out, []byte("<b>")) {
				t.Fatalf("raw markup survived into the document:\n%s", out)
			}

			doc := parseDecoratedSVG(t, out)
			if len(doc.Texts) != 1 {
				t.Fatalf("text element count = %d, want 1", len(doc.Texts))
			}
			text := doc.Texts[0]
			if text.Value != hostile {
				t.Errorf("caption = %q, want %q", text.Value, hostile)
			}
			if text.Font != "monospace" {
				t.Errorf("font-family = %q, want monospace", text.Font)
			}
			if text.Anchor != "middle" {
				t.Errorf("text-anchor = %q, want middle", text.Anchor)
			}
		})
	}
}

func TestSVGCaptionShrinksToItsBand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "short", text: "OK"},
		{name: "long enough to need shrinking", text: strings.Repeat("BARQR ", 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := render.DefaultStyle()
			s.Frame = &render.Frame{
				Kind: render.FrameBorder, Color: color.NRGBA{A: 0xFF}, Width: 2, Caption: tc.text,
			}
			c := svgStyled(t, s)

			spec, ok := resolveCaption(c)
			if !ok {
				t.Fatal("resolveCaption reported no caption")
			}
			size := svgCaptionSize(spec)

			if size > float64(spec.rect.Dy()) {
				t.Errorf("font size %g is taller than the %d-module band", size, spec.rect.Dy())
			}
			// A monospace run is close enough to 0.6em per glyph everywhere that
			// this is what stops a caption from running out through the frame.
			if width := size * svgMonoAdvance * float64(len([]rune(tc.text))); width > float64(spec.rect.Dx())+1e-6 {
				t.Errorf("the caption is about %g modules wide, want at most %d", width, spec.rect.Dx())
			}
		})
	}
}

func TestSVGFindsTheEyesThroughAFrame(t *testing.T) {
	t.Parallel()

	// A frame moves the symbol inwards by its own thickness as well as the
	// quiet zone. Origins derived from the margin alone land on the frame, and
	// the eyes silently stop being painted as whole shapes.
	s := render.DefaultStyle()
	s.Frame = &render.Frame{Kind: render.FrameBorder, Color: color.NRGBA{A: 0xFF}, Width: 3}
	c := svgStyled(t, s)

	origins := svgEyeOrigins(c)
	if len(origins) != 3 {
		t.Fatalf("found %d finder patterns behind a frame, want 3", len(origins))
	}
	sym := c.SymbolRect()
	if origins[0][0] != sym.Min.X || origins[0][1] != sym.Min.Y {
		t.Errorf("top-left eye at %v, want the symbol's own corner (%d,%d)",
			origins[0], sym.Min.X, sym.Min.Y)
	}
}
