package writer

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

var epsBBoxRE = regexp.MustCompile(`(?m)^%%BoundingBox: 0 0 (\d+) (\d+)$`)

func TestEPSWriterIdentity(t *testing.T) {
	t.Parallel()

	w, err := Get(FormatEPS)
	if err != nil {
		t.Fatalf("Get(eps): %v", err)
	}
	if got := w.MIME(); got != "application/postscript" {
		t.Errorf("MIME = %q, want application/postscript", got)
	}
	if got := w.Extension(); got != "eps" {
		t.Errorf("Extension = %q, want eps", got)
	}
	if w.Binary() {
		t.Error("Binary = true, want false for EPS")
	}
}

func TestEPSStructureIsValid(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "eps structure")
	out, err := epsWriter{}.Write(c, vecTestOpts(FormatEPS))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc := string(out)

	if !strings.HasPrefix(doc, "%!PS-Adobe-3.0 EPSF-3.0\n") {
		t.Errorf("first line is %q, want the EPSF magic", strings.SplitN(doc, "\n", 2)[0])
	}
	for _, want := range []string{
		"%%Creator: barqr\n",
		"%%EndComments\n",
		"%%BeginProlog\n",
		"%%EndProlog\n",
		"showpage\n",
		"%%EOF\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document is missing %q", want)
		}
	}
	if !strings.HasSuffix(doc, "showpage\n%%EOF\n") {
		t.Error("document does not end with showpage followed by the EOF marker")
	}

	// An EPS placed inside another document must not leak graphics state.
	if !strings.Contains(doc, "gsave\n") || !strings.Contains(doc, "grestore\n") {
		t.Error("body is not bracketed by gsave/grestore")
	}

	// The rectangle procedure has to be defined before its first use, or the
	// interpreter fails on an undefined name.
	def := strings.Index(doc, "/R {")
	if def < 0 {
		t.Fatal("no /R rectangle procedure defined")
	}
	use := strings.Index(doc, " R\n")
	if use < 0 {
		t.Fatal("body never calls the rectangle procedure")
	}
	if use < def {
		t.Errorf("/R is used at %d before it is defined at %d", use, def)
	}
}

func TestEPSBoundingBoxRoundsOutwards(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		size float64
		unit Unit
	}{
		{name: "millimetres", size: 30, unit: UnitMM},
		{name: "inches", size: 2, unit: UnitIn},
		{name: "default pixels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "bounding box")
			o := vecTestOpts(FormatEPS)
			o.Size, o.Unit = tc.size, tc.unit

			page, err := vecPageSize(c, o)
			if err != nil {
				t.Fatalf("vecPageSize: %v", err)
			}

			out, err := epsWriter{}.Write(c, o)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			m := epsBBoxRE.FindSubmatch(out)
			if m == nil {
				t.Fatalf("no integer %%%%BoundingBox in:\n%s", firstLines(out, 8))
			}
			// A placing application scales from the BoundingBox alone, so it
			// must never clip the artwork: round outwards, never in.
			wantW := fmt.Sprint(int(math.Ceil(page.W)))
			wantH := fmt.Sprint(int(math.Ceil(page.H)))
			if string(m[1]) != wantW || string(m[2]) != wantH {
				t.Errorf("BoundingBox = %s %s, want %s %s", m[1], m[2], wantW, wantH)
			}
			if !bytes.Contains(out, []byte("%%HiResBoundingBox: 0 0 "+vecNum(page.W))) {
				t.Error("no HiResBoundingBox carrying the exact size")
			}
		})
	}
}

// firstLines returns the first n lines of p, for readable failure output.
func firstLines(p []byte, n int) string {
	lines := strings.SplitN(string(p), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func TestEPSBackgroundFollowsAlpha(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		bg     color.NRGBA
		wantBG bool
	}{
		{name: "opaque", bg: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, wantBG: true},
		{name: "transparent", bg: render.Transparent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "eps background")
			c.Style.BG = tc.bg

			o := vecTestOpts(FormatEPS)
			page, err := vecPageSize(c, o)
			if err != nil {
				t.Fatalf("vecPageSize: %v", err)
			}

			out, err := epsWriter{}.Write(c, o)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			paper := fmt.Sprintf("0 0 %s %s R\n", vecNum(page.W), vecNum(page.H))
			if got := bytes.Contains(out, []byte(paper)); got != tc.wantBG {
				t.Errorf("background rectangle present = %t, want %t", got, tc.wantBG)
			}
		})
	}
}

func TestEPSMergesRuns(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "https://example.com/a-reasonably-long-payload-to-densify")
	out, err := epsWriter{}.Write(c, vecTestOpts(FormatEPS))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// One R per run, plus the background rectangle.
	rects := bytes.Count(out, []byte(" R\n"))
	if rects-1 != len(vecRuns(c)) {
		t.Errorf("body draws %d module rectangles, want %d runs", rects-1, len(vecRuns(c)))
	}
	if rects-1 >= c.Dark() {
		t.Errorf("merging produced %d rectangles for %d dark modules, want fewer",
			rects-1, c.Dark())
	}
}

func TestEPSHRIIsEscapedAndCentred(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "eps hri")
	c.HRI = `A(B)C\ <script>`

	out, err := epsWriter{}.Write(c, vecTestOpts(FormatEPS))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc := string(out)

	const want = `(A\(B\)C\\ <script>) dup stringwidth pop 2 div neg 0 rmoveto show`
	if !strings.Contains(doc, want) {
		t.Errorf("body does not contain the escaped, centred show:\nwant %q", want)
	}
	// Courier is the default: a printed code's human-readable line is
	// monospaced, and it is the only choice that agrees with the raster and
	// SVG paths. style.hri_font=sans asks for the proportional face.
	if !strings.Contains(doc, "/Courier findfont") {
		t.Error("no Courier font selected for the human-readable line")
	}

	// The band must be inside the bounding box, or the text is clipped away.
	page, err := vecPageSize(c, vecTestOpts(FormatEPS))
	if err != nil {
		t.Fatalf("vecPageSize: %v", err)
	}
	m := epsBBoxRE.FindSubmatch(out)
	if m == nil {
		t.Fatal("no BoundingBox")
	}
	if got, want := string(m[2]), fmt.Sprint(int(math.Ceil(page.H))); got != want {
		t.Errorf("BoundingBox height = %s, want %s including the text band", got, want)
	}
}

func TestEPSPropagatesSizingErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts func(OutputOpts) OutputOpts
		want error
	}{
		{
			name: "max pixels",
			opts: func(o OutputOpts) OutputOpts { o.MaxPixels = 64; return o },
			want: ErrCanvasTooLarge,
		},
		{
			name: "unknown unit",
			opts: func(o OutputOpts) OutputOpts { o.Size, o.Unit = 10, Unit("cubit"); return o },
			want: ErrInvalidOutput,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "eps limits")
			if _, err := (epsWriter{}).Write(c, tc.opts(vecTestOpts(FormatEPS))); !errors.Is(err, tc.want) {
				t.Errorf("Write error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEPSCommentStripsLineBreaks(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, want string }{
		{name: "plain", in: "qr", want: "qr"},
		{name: "newline injection", in: "qr\n%%BoundingBox: 0 0 1 1", want: "qr%%BoundingBox: 0 0 1 1"},
		{name: "control bytes dropped", in: "q\x00r", want: "qr"},
		{name: "empty falls back", in: "", want: "barqr code"},
		{name: "non-ascii dropped entirely", in: "é", want: "barqr code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := epsComment(tc.in); got != tc.want {
				t.Errorf("epsComment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEPSTitleCannotBreakTheCommentBlock(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "eps title")
	c.Symbology = "qr\n%%BoundingBox: 0 0 1 1"

	out, err := epsWriter{}.Write(c, vecTestOpts(FormatEPS))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Exactly one BoundingBox: a smuggled second one would win with some
	// consumers and clip the code to a single point.
	if got := len(epsBBoxRE.FindAll(out, -1)); got != 1 {
		t.Errorf("BoundingBox comment count = %d, want 1", got)
	}
}
