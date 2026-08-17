package writer

import (
	"bytes"
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
	if doc.Texts[0].Size != svgNum(hriFontModules) {
		t.Errorf("font-size = %q, want %q", doc.Texts[0].Size, svgNum(hriFontModules))
	}

	// The band is added below the code, in both the viewBox and the pixel size.
	if want := fmt.Sprintf("0 0 %d %d", c.Cols, c.Rows+hriBandModules); doc.ViewBox != want {
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
