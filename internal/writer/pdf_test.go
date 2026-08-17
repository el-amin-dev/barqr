package writer

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"regexp"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
)

// vecTestCanvas builds a real QR canvas through the encoder and the standard
// renderer, so the vector writers are exercised against the same Canvas the
// HTTP layer hands them rather than a hand-rolled fixture.
func vecTestCanvas(t *testing.T, data string) render.Canvas {
	t.Helper()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatalf("encoder.Get(qr): %v", err)
	}
	m, err := enc.Encode(data, encoder.AutoEncodeOpts())
	if err != nil {
		t.Fatalf("encode %q: %v", data, err)
	}
	r, err := render.Get(render.StandardRenderer)
	if err != nil {
		t.Fatalf("render.Get: %v", err)
	}
	c, err := r.Render(m, render.DefaultStyle())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return c
}

// vecTestOpts returns output options with the pixel defaults and no cap.
func vecTestOpts(format string) OutputOpts {
	o := DefaultOutputOpts(format)
	o.MaxPixels = 0
	return o
}

var (
	pdfStreamRE    = regexp.MustCompile(`<< /Length (\d+) /Filter /FlateDecode >>\nstream\n`)
	pdfMediaBoxRE  = regexp.MustCompile(`/MediaBox \[0 0 ([0-9.]+) ([0-9.]+)\]`)
	pdfStartXrefRE = regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`)
	pdfSizeRE      = regexp.MustCompile(`/Size (\d+)`)
	pdfObjRE       = regexp.MustCompile(`(?m)^\d+ 0 obj$`)
)

// pdfDecodedStream returns the inflated content stream of a document.
func pdfDecodedStream(t *testing.T, doc []byte) string {
	t.Helper()

	loc := pdfStreamRE.FindSubmatchIndex(doc)
	if loc == nil {
		t.Fatal("no FlateDecode stream object in the document")
	}
	var n int
	if _, err := fmt.Sscanf(string(doc[loc[2]:loc[3]]), "%d", &n); err != nil {
		t.Fatalf("parsing /Length: %v", err)
	}

	raw := doc[loc[1] : loc[1]+n]
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("stream is not valid zlib: %v", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("inflating the content stream: %v", err)
	}
	return string(out)
}

func TestPDFWriterIdentity(t *testing.T) {
	t.Parallel()

	w, err := Get(FormatPDF)
	if err != nil {
		t.Fatalf("Get(pdf): %v", err)
	}
	if got := w.MIME(); got != "application/pdf" {
		t.Errorf("MIME = %q, want application/pdf", got)
	}
	if got := w.Extension(); got != "pdf" {
		t.Errorf("Extension = %q, want pdf", got)
	}
	if !w.Binary() {
		t.Error("Binary = false, want true for a pdf")
	}
}

func TestPDFStructureIsValid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		hri  string
		objs int // catalog, pages, page, contents, and a font only with HRI
	}{
		{name: "without hri", objs: 4},
		{name: "with hri", hri: "9781234567897", objs: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "structure")
			c.HRI = tc.hri

			doc, err := pdfWriter{}.Write(c, vecTestOpts(FormatPDF))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			if !bytes.HasPrefix(doc, []byte("%PDF-1.4\n")) {
				t.Errorf("document does not start with the PDF header: %q", doc[:9])
			}
			if !bytes.HasSuffix(doc, []byte("%%EOF\n")) {
				t.Error("document does not end with the EOF marker")
			}

			m := pdfStartXrefRE.FindSubmatch(doc)
			if m == nil {
				t.Fatal("no startxref trailer before the EOF marker")
			}
			var off int
			if _, err := fmt.Sscanf(string(m[1]), "%d", &off); err != nil {
				t.Fatalf("parsing startxref: %v", err)
			}
			if off < 0 || off+4 > len(doc) || string(doc[off:off+4]) != "xref" {
				t.Fatalf("startxref %d does not point at the xref keyword", off)
			}

			if got := len(pdfObjRE.FindAll(doc, -1)); got != tc.objs {
				t.Errorf("object count = %d, want %d", got, tc.objs)
			}
			size := pdfSizeRE.FindSubmatch(doc)
			if size == nil {
				t.Fatal("trailer has no /Size")
			}
			if want := fmt.Sprint(tc.objs + 1); string(size[1]) != want {
				t.Errorf("/Size = %s, want %s (objects plus the free entry)", size[1], want)
			}
		})
	}
}

func TestPDFXrefOffsetsAddressObjects(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "xref offsets")
	doc, err := pdfWriter{}.Write(c, vecTestOpts(FormatPDF))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	m := pdfStartXrefRE.FindSubmatch(doc)
	if m == nil {
		t.Fatal("no startxref trailer")
	}
	var start int
	if _, err := fmt.Sscanf(string(m[1]), "%d", &start); err != nil {
		t.Fatalf("parsing startxref: %v", err)
	}

	// Entries are fixed-width: "xref\n0 N \n" then 20 bytes each, the first of
	// which is the free-list head.
	table := doc[start:]
	entryRE := regexp.MustCompile(`(?m)^(\d{10}) 00000 n $`)
	entries := entryRE.FindAllSubmatch(table, -1)
	if len(entries) != 4 {
		t.Fatalf("in-use xref entries = %d, want 4", len(entries))
	}

	for i, e := range entries {
		var off int
		if _, err := fmt.Sscanf(string(e[1]), "%d", &off); err != nil {
			t.Fatalf("parsing offset %d: %v", i, err)
		}
		want := fmt.Sprintf("%d 0 obj\n", i+1)
		if off+len(want) > len(doc) || string(doc[off:off+len(want)]) != want {
			t.Errorf("xref entry %d points at %q, want %q", i+1,
				doc[off:min(off+len(want), len(doc))], want)
		}
	}
}

func TestPDFPhysicalSizeHonoursUnits(t *testing.T) {
	t.Parallel()

	// A pixel size is floored to a whole number of pixels per module by
	// PixelSize, so only mm and in land on the requested figure exactly; the
	// pixel case is checked against the resolved pixel width instead.
	for _, tc := range []struct {
		name  string
		size  float64
		unit  Unit
		dpi   int
		width func(px int) float64 // expected page width in points
	}{
		{
			name: "millimetres", size: 30, unit: UnitMM, dpi: 300,
			width: func(int) float64 { return 30 / 25.4 * pointsPerInch },
		},
		{
			name: "inches", size: 2, unit: UnitIn, dpi: 300,
			width: func(int) float64 { return 2 * pointsPerInch },
		},
		{
			name: "pixels via dpi", size: 300, unit: UnitPx, dpi: 150,
			width: func(px int) float64 { return float64(px) / 150 * pointsPerInch },
		},
		{
			name: "no size falls back to scale and dpi", dpi: 72,
			width: func(px int) float64 { return float64(px) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "physical size")
			o := vecTestOpts(FormatPDF)
			o.Size, o.Unit, o.DPI = tc.size, tc.unit, tc.dpi

			px, _, _, err := PixelSize(c, o)
			if err != nil {
				t.Fatalf("PixelSize: %v", err)
			}

			doc, err := pdfWriter{}.Write(c, o)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			m := pdfMediaBoxRE.FindSubmatch(doc)
			if m == nil {
				t.Fatal("no /MediaBox on the page object")
			}
			want := vecNum(tc.width(px))
			if string(m[1]) != want {
				t.Errorf("MediaBox width = %s, want %s", m[1], want)
			}
			if string(m[2]) != want {
				t.Errorf("MediaBox height = %s, want %s (a QR is square)", m[2], want)
			}
		})
	}
}

func TestPDFMergesRunsAndCompresses(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "https://example.com/a-reasonably-long-payload-to-densify")
	doc, err := pdfWriter{}.Write(c, vecTestOpts(FormatPDF))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	stream := pdfDecodedStream(t, doc)
	rects := bytes.Count([]byte(stream), []byte(" re\n"))
	if rects < 2 {
		t.Fatalf("content stream draws %d rectangles, expected the code plus a background", rects)
	}
	// One rectangle per dark module would defeat the merge; the background rect
	// is the one extra.
	if rects-1 >= c.Dark() {
		t.Errorf("merging produced %d module rectangles for %d dark modules, want fewer",
			rects-1, c.Dark())
	}
	if !bytes.Contains([]byte(stream), []byte(" rg\n")) {
		t.Error("content stream never sets a fill colour")
	}
}

func TestPDFTransparentBackgroundDrawsNoPaper(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		bg      color.NRGBA
		wantBG  bool
		wantRGB string
	}{
		{name: "opaque white", bg: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			wantBG: true, wantRGB: "1 1 1 rg"},
		{name: "transparent", bg: render.Transparent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "background")
			c.Style.BG = tc.bg

			doc, err := pdfWriter{}.Write(c, vecTestOpts(FormatPDF))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			stream := pdfDecodedStream(t, doc)

			page, err := vecPageSize(c, vecTestOpts(FormatPDF))
			if err != nil {
				t.Fatalf("vecPageSize: %v", err)
			}
			paper := fmt.Sprintf("0 0 %s %s re\nf\n", vecNum(page.W), vecNum(page.H))

			if got := bytes.Contains([]byte(stream), []byte(paper)); got != tc.wantBG {
				t.Errorf("background rectangle present = %t, want %t", got, tc.wantBG)
			}
			if tc.wantRGB != "" && !bytes.Contains([]byte(stream), []byte(tc.wantRGB)) {
				t.Errorf("content stream does not set the paper colour %q", tc.wantRGB)
			}
		})
	}
}

func TestPDFHRIIsEscaped(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "hri")
	c.HRI = `A(B)C\ <script>`

	doc, err := pdfWriter{}.Write(c, vecTestOpts(FormatPDF))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	stream := pdfDecodedStream(t, doc)

	const want = `(A\(B\)C\\ <script>) Tj`
	if !bytes.Contains([]byte(stream), []byte(want)) {
		t.Errorf("content stream does not contain the escaped string %q\ngot: %s", want, stream)
	}
	if !bytes.Contains(doc, []byte("/BaseFont /Helvetica")) {
		t.Error("no Helvetica font object for the human-readable line")
	}
	if !bytes.Contains([]byte(stream), []byte("BT\n")) {
		t.Error("no text object in the content stream")
	}
}

func TestPDFRejectsOversizedOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts func(OutputOpts) OutputOpts
		want error
	}{
		{
			name: "max pixels",
			opts: func(o OutputOpts) OutputOpts { o.MaxPixels = 16; return o },
			want: ErrCanvasTooLarge,
		},
		{
			name: "unknown unit",
			opts: func(o OutputOpts) OutputOpts { o.Size, o.Unit = 10, Unit("furlong"); return o },
			want: ErrInvalidOutput,
		},
		{
			name: "size below one pixel per module",
			opts: func(o OutputOpts) OutputOpts { o.Size, o.Unit = 4, UnitPx; return o },
			want: ErrInvalidOutput,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := vecTestCanvas(t, "limits")
			if _, err := (pdfWriter{}).Write(c, tc.opts(vecTestOpts(FormatPDF))); !errors.Is(err, tc.want) {
				t.Errorf("Write error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVecRunsMergeAdjacentModules(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "runs")
	runs := vecRuns(c)

	total := 0
	for _, r := range runs {
		if r.Len < 1 {
			t.Fatalf("run %+v has a non-positive length", r)
		}
		for i := range r.Len {
			if !c.At(r.X+i, r.Y) {
				t.Fatalf("run %+v covers the light module (%d, %d)", r, r.X+i, r.Y)
			}
		}
		// A run must be maximal: the module just past its end cannot be dark
		// in the same colour, or two runs would draw what should be one.
		if end := r.X + r.Len; end < c.Cols && c.At(end, r.Y) && c.ColorAt(end, r.Y) == r.Color {
			t.Fatalf("run %+v stops short of the module at (%d, %d)", r, end, r.Y)
		}
		total += r.Len
	}

	if total != c.Dark() {
		t.Errorf("runs cover %d modules, want the %d dark ones", total, c.Dark())
	}
	if len(runs) >= c.Dark() {
		t.Errorf("merging produced %d runs for %d dark modules, want fewer", len(runs), c.Dark())
	}
}

func TestVecStringEscapesPostScriptLiterals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, want string }{
		{name: "plain", in: "9781234567897", want: "(9781234567897)"},
		{name: "parentheses and backslash", in: `A(B)C\`, want: `(A\(B\)C\\)`},
		{name: "markup is not special here", in: "<script>", want: "(<script>)"},
		{name: "control bytes become octal", in: "a\nb", want: `(a\012b)`},
		{name: "non-ascii becomes octal", in: "é", want: `(\303\251)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := vecString(tc.in); got != tc.want {
				t.Errorf("vecString(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestVecOverFlattensAlpha(t *testing.T) {
	t.Parallel()

	opaqueBlack := color.NRGBA{A: 0xFF}
	half := color.NRGBA{A: 0x80}

	for _, tc := range []struct {
		name   string
		fg, bg color.NRGBA
		want   color.NRGBA
	}{
		{name: "opaque passes through", fg: opaqueBlack, bg: render.Transparent, want: opaqueBlack},
		{
			name: "half over white paper",
			fg:   half,
			bg:   color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			want: color.NRGBA{R: 0x7F, G: 0x7F, B: 0x7F, A: 0xFF},
		},
		{
			name: "transparent background is treated as white",
			fg:   half,
			bg:   render.Transparent,
			want: color.NRGBA{R: 0x7F, G: 0x7F, B: 0x7F, A: 0xFF},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := vecOver(tc.fg, tc.bg); got != tc.want {
				t.Errorf("vecOver(%v, %v) = %v, want %v", tc.fg, tc.bg, got, tc.want)
			}
		})
	}
}

func TestVecPageSizeReservesTheHRIBand(t *testing.T) {
	t.Parallel()

	c := vecTestCanvas(t, "band")
	o := vecTestOpts(FormatPDF)

	plain, err := vecPageSize(c, o)
	if err != nil {
		t.Fatalf("vecPageSize: %v", err)
	}

	c.HRI = "9781234567897"
	withText, err := vecPageSize(c, o)
	if err != nil {
		t.Fatalf("vecPageSize with hri: %v", err)
	}

	if withText.W != plain.W {
		t.Errorf("width changed with an HRI: %v, want %v", withText.W, plain.W)
	}
	if want := plain.H + hriBandModules*plain.Module; math.Abs(withText.H-want) > 1e-9 {
		t.Errorf("height with an HRI = %v, want %v", withText.H, want)
	}
	if got := withText.baseline(c); got <= 0 || got >= withText.H-float64(c.Rows)*withText.Module {
		t.Errorf("baseline %v does not sit inside the human-readable band", got)
	}
}

func TestVecRGBAndNumFormatCompactly(t *testing.T) {
	t.Parallel()

	if got := vecRGB(color.NRGBA{R: 0xFF, A: 0xFF}); got != "1 0 0" {
		t.Errorf("vecRGB(red) = %q, want %q", got, "1 0 0")
	}
	if got := vecNum(1.0 / 3.0); got != "0.333" {
		t.Errorf("vecNum(1/3) = %q, want %q", got, "0.333")
	}
	if got := vecNum(72); got != "72" {
		t.Errorf("vecNum(72) = %q, want %q", got, "72")
	}
}
