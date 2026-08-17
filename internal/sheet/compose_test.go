package sheet

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// makePNG renders a w-by-h PNG filled with one colour, which is enough for
// every geometry and embedding assertion here.
func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// cellsFrom builds n cells from distinct PNGs, so each one is its own XObject.
func cellsFrom(t *testing.T, n int) []Cell {
	t.Helper()
	out := make([]Cell, n)
	for i := range n {
		out[i] = Cell{
			PNG:     makePNG(t, 8+i, 8+i, color.NRGBA{R: uint8(i * 7), A: 255}),
			Caption: fmt.Sprintf("label %d", i+1),
		}
	}
	return out
}

// pdfDoc is a parsed PDF, used to assert the structure of our own output.
type pdfDoc struct {
	raw     []byte
	objects map[int][]byte
	size    int
}

// parsePDF walks the cross-reference table the way a reader does.
//
// This is the test that matters for a hand-written PDF: every object is found
// through a byte offset in the xref table, so an off-by-one produces a file
// that opens in one viewer and is rejected by the next. Parsing our own output
// is the only way to catch that without a human opening it.
func parsePDF(t *testing.T, doc []byte) pdfDoc {
	t.Helper()

	if !bytes.HasPrefix(doc, []byte("%PDF-1.4\n")) {
		t.Fatalf("document does not start with a PDF header: %q", head(doc))
	}
	if !bytes.HasSuffix(doc, []byte("%%EOF\n")) {
		t.Fatal("document does not end with the EOF marker")
	}

	marker := bytes.LastIndex(doc, []byte("startxref\n"))
	if marker < 0 {
		t.Fatal("no startxref")
	}
	rest := doc[marker+len("startxref\n"):]
	end := bytes.IndexByte(rest, '\n')
	if end < 0 {
		t.Fatal("startxref has no offset")
	}
	xrefAt, err := strconv.Atoi(string(rest[:end]))
	if err != nil {
		t.Fatalf("startxref offset is not a number: %v", err)
	}
	if xrefAt < 0 || xrefAt >= len(doc) {
		t.Fatalf("startxref offset %d is outside the %d-byte document", xrefAt, len(doc))
	}

	table := doc[xrefAt:]
	var count int
	if _, err := fmt.Sscanf(string(table[:min(len(table), 32)]), "xref\n0 %d", &count); err != nil {
		t.Fatalf("xref table does not start with a subsection header: %q", head(table))
	}

	// Skip "xref\n0 N\n" and read fixed-width 20-byte entries from there.
	entries := table[len(fmt.Sprintf("xref\n0 %d\n", count)):]
	if len(entries) < count*20 {
		t.Fatalf("xref table is %d bytes, too short for %d entries", len(entries), count)
	}
	if got := string(entries[:20]); got != "0000000000 65535 f \n" {
		t.Fatalf("free-list head entry = %q", got)
	}

	objects := make(map[int][]byte, count-1)
	for i := 1; i < count; i++ {
		entry := string(entries[i*20 : i*20+20])
		offset, err := strconv.Atoi(entry[:10])
		if err != nil {
			t.Fatalf("entry %d offset %q is not a number", i, entry[:10])
		}
		if offset <= 0 || offset >= len(doc) {
			t.Fatalf("entry %d points at byte %d of a %d-byte document", i, offset, len(doc))
		}
		want := fmt.Sprintf("%d 0 obj\n", i)
		if !bytes.HasPrefix(doc[offset:], []byte(want)) {
			t.Fatalf("entry %d points at %q, want it to start object %d",
				i, head(doc[offset:]), i)
		}
		body := doc[offset+len(want):]
		stop := bytes.Index(body, []byte("\nendobj\n"))
		if stop < 0 {
			t.Fatalf("object %d has no endobj", i)
		}
		objects[i] = body[:stop]
	}

	var trailerSize int
	if _, err := fmt.Sscanf(trailerOf(t, doc), "<< /Size %d", &trailerSize); err != nil {
		t.Fatalf("trailer is unreadable: %v", err)
	}
	if trailerSize != count {
		t.Fatalf("trailer /Size %d disagrees with the xref count %d", trailerSize, count)
	}

	return pdfDoc{raw: doc, objects: objects, size: count}
}

func trailerOf(t *testing.T, doc []byte) string {
	t.Helper()
	at := bytes.LastIndex(doc, []byte("trailer\n"))
	if at < 0 {
		t.Fatal("no trailer")
	}
	rest := doc[at+len("trailer\n"):]
	end := bytes.Index(rest, []byte("\nstartxref"))
	if end < 0 {
		t.Fatal("trailer is not terminated")
	}
	return string(rest[:end])
}

func head(b []byte) string { return string(b[:min(len(b), 48)]) }

func TestComposeProducesAStructurallyValidPDF(t *testing.T) {
	t.Parallel()

	l := grid()
	l.LabelCaption = true

	doc, err := Compose(cellsFrom(t, 5), l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	parsed := parsePDF(t, doc)

	// 1 catalog + 1 page tree + 1 font + 5 images + 1 content + 1 page, plus
	// the free-list head that every xref table begins with.
	if want := 10 + 1; parsed.size != want {
		t.Fatalf("document has %d xref entries, want %d", parsed.size, want)
	}
	if got := string(parsed.objects[1]); !strings.Contains(got, "/Type /Catalog") {
		t.Fatalf("object 1 = %q, want the catalog", got)
	}
	if got := string(parsed.objects[2]); !strings.Contains(got, "/Count 1") {
		t.Fatalf("page tree = %q, want one page", got)
	}
	if got := string(parsed.objects[3]); !strings.Contains(got, "/BaseFont /Helvetica") {
		t.Fatalf("object 3 = %q, want the font", got)
	}
}

func TestComposePaginatesAcrossSheets(t *testing.T) {
	t.Parallel()

	l := Layout{
		Page: A4, Cols: 2, Rows: 2,
		MarginTopMM: 10, MarginLeftMM: 10,
		CellWidthMM: 80, CellHeightMM: 80,
		GutterXMM: 5,
	}

	cases := []struct {
		name      string
		cells     int
		wantPages int
	}{
		{"one cell", 1, 1},
		{"a full page", 4, 1},
		{"one over", 5, 2},
		{"two full pages", 8, 2},
		{"three pages", 9, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, err := Compose(cellsFrom(t, tc.cells), l)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			parsed := parsePDF(t, doc)

			if got := countMatches(doc, `/Type /Page\b`); got != tc.wantPages {
				t.Fatalf("got %d page objects, want %d", got, tc.wantPages)
			}
			if !strings.Contains(string(parsed.objects[2]),
				fmt.Sprintf("/Count %d", tc.wantPages)) {
				t.Fatalf("page tree = %q, want /Count %d", parsed.objects[2], tc.wantPages)
			}
			// Every /Kids reference must resolve to a real page object.
			for _, num := range kidNumbers(t, string(parsed.objects[2])) {
				body, ok := parsed.objects[num]
				if !ok {
					t.Fatalf("/Kids names object %d, which is not in the xref", num)
				}
				if !strings.Contains(string(body), "/Type /Page") {
					t.Fatalf("object %d is not a page: %q", num, body)
				}
			}
		})
	}
}

func TestComposeEmbedsEachDistinctImageOnce(t *testing.T) {
	t.Parallel()

	shared := makePNG(t, 16, 16, color.Black)
	other := makePNG(t, 16, 16, color.NRGBA{R: 255, A: 255})

	cells := make([]Cell, 12)
	for i := range cells {
		cells[i] = Cell{PNG: shared}
	}
	cells[3] = Cell{PNG: other}

	doc, err := Compose(cells, grid())
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	parsePDF(t, doc)

	// Twelve cells, two distinct PNGs: a naive implementation writes twelve
	// XObjects and a file six times the size.
	if got := countMatches(doc, `/Subtype /Image`); got != 2 {
		t.Fatalf("got %d image objects, want 2", got)
	}
	// Both are still referenced from the page.
	if got := countMatches(doc, `/Im0 `); got < 1 {
		t.Fatal("the shared image is not referenced")
	}
	if got := countMatches(doc, `/Im1 `); got < 1 {
		t.Fatal("the second image is not referenced")
	}
}

func TestComposeEmbedsThePixelsAsFlateRGB(t *testing.T) {
	t.Parallel()

	const w, h = 5, 3
	red := color.NRGBA{R: 0xFF, G: 0x11, B: 0x22, A: 255}

	l := Layout{Page: A4, Cols: 1, Rows: 1, CellWidthMM: 100, CellHeightMM: 60}
	doc, err := Compose([]Cell{{PNG: makePNG(t, w, h, red)}}, l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	parsed := parsePDF(t, doc)

	// The single image is object 4: catalog, pages, font, then images.
	obj := parsed.objects[4]
	for _, want := range []string{
		"/Subtype /Image", "/ColorSpace /DeviceRGB", "/BitsPerComponent 8",
		"/Filter /FlateDecode", fmt.Sprintf("/Width %d", w), fmt.Sprintf("/Height %d", h),
	} {
		if !bytes.Contains(obj, []byte(want)) {
			t.Fatalf("image dictionary is missing %q: %q", want, head(obj))
		}
	}

	raw := inflateStream(t, obj)
	if len(raw) != w*h*3 {
		t.Fatalf("stream is %d bytes, want %d RGB triples", len(raw), w*h*3)
	}
	for i := 0; i < len(raw); i += 3 {
		if raw[i] != 0xFF || raw[i+1] != 0x11 || raw[i+2] != 0x22 {
			t.Fatalf("pixel %d = %v, want the source colour", i/3, raw[i:i+3])
		}
	}
}

func TestComposeCompositesTransparencyOntoWhite(t *testing.T) {
	t.Parallel()

	// A fully transparent PNG means "paper" on label stock. Carried into the
	// PDF as premultiplied zeros it would print as solid black.
	l := Layout{Page: A4, Cols: 1, Rows: 1, CellWidthMM: 100, CellHeightMM: 60}
	doc, err := Compose([]Cell{{PNG: makePNG(t, 4, 4, color.NRGBA{})}}, l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	raw := inflateStream(t, parsePDF(t, doc).objects[4])
	for i, b := range raw {
		if b != 0xFF {
			t.Fatalf("byte %d = %#x, want white where the source was transparent", i, b)
		}
	}
}

func TestComposePlacesTheImageInsideItsCell(t *testing.T) {
	t.Parallel()

	// One 100x50mm cell at the page's top-left, holding a square image: the
	// square must be fitted to the 50mm height, not stretched to 100mm.
	l := Layout{Page: A4, Cols: 1, Rows: 1, CellWidthMM: 100, CellHeightMM: 50}
	doc, err := Compose([]Cell{{PNG: makePNG(t, 20, 20, color.Black)}}, l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	content := string(inflateStream(t, parsePDF(t, doc).objects[5]))
	drawW, drawH, x, y := placement(t, content)

	wantSide := 50 * pointsPerInch / mmPerInch
	if !nearly2(drawW, wantSide) || !nearly2(drawH, wantSide) {
		t.Fatalf("image drawn %gx%gpt, want a %gpt square", drawW, drawH, wantSide)
	}
	// Centred horizontally in the 100mm cell.
	wantX := 25 * pointsPerInch / mmPerInch
	if !nearly2(x, wantX) {
		t.Fatalf("image at x=%gpt, want %gpt", x, wantX)
	}
	// PDF's origin is the bottom-left: a cell at the top of an A4 page sits
	// (297 - 50)mm up from it.
	wantY := (297 - 50) * pointsPerInch / mmPerInch
	if !nearly2(y, wantY) {
		t.Fatalf("image at y=%gpt, want %gpt", y, wantY)
	}
}

func TestComposeDrawsCaptionsOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	cells := []Cell{{PNG: makePNG(t, 16, 16, color.Black), Caption: "SKU-4471"}}

	off := grid()
	doc, err := Compose(cells, off)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if bytes.Contains(inflateStream(t, parsePDF(t, doc).objects[5]), []byte("SKU-4471")) {
		t.Fatal("a caption was drawn with LabelCaption off")
	}

	on := grid()
	on.LabelCaption = true
	doc, err = Compose(cells, on)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	content := inflateStream(t, parsePDF(t, doc).objects[5])
	if !bytes.Contains(content, []byte("(SKU-4471) Tj")) {
		t.Fatalf("caption missing from %q", content)
	}
	if !bytes.Contains(content, []byte("/F1 7 Tf")) {
		t.Fatalf("caption is not set in the base-14 font: %q", content)
	}
}

func TestComposeSkipsCellsWithNoImage(t *testing.T) {
	t.Parallel()

	// Padding the front of the slice is how a caller prints onto a part-used
	// sheet, so a blank cell must consume its position and draw nothing.
	l := grid()
	l.LabelCaption = true
	cells := []Cell{{}, {}, {PNG: makePNG(t, 16, 16, color.Black), Caption: "third"}}

	doc, err := Compose(cells, l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	parsed := parsePDF(t, doc)

	if got := countMatches(doc, "/Subtype /Image"); got != 1 {
		t.Fatalf("got %d images, want 1", got)
	}
	content := string(inflateStream(t, parsed.objects[5]))
	if strings.Count(content, "Do\n") != 1 {
		t.Fatalf("content draws %d images: %q", strings.Count(content, "Do\n"), content)
	}

	// The surviving code must sit inside the third cell, not the first: a
	// blank cell consumes its position rather than shuffling the rest along.
	drawW, _, x, _ := placement(t, content)
	cellX, _, cellW, _, _ := l.CellRectMM(2)
	left, right := cellX*pointsPerInch/mmPerInch, (cellX+cellW)*pointsPerInch/mmPerInch
	if x < left-0.01 || x+drawW > right+0.01 {
		t.Fatalf("the image spans %g..%gpt, outside the third cell's %g..%gpt",
			x, x+drawW, left, right)
	}
}

func TestComposeRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	good := makePNG(t, 8, 8, color.Black)

	cases := []struct {
		name    string
		cells   []Cell
		layout  Layout
		wantErr error
		wants   string
	}{
		{
			"no cells",
			nil,
			grid(),
			ErrNoCells,
			"no cells",
		},
		{
			"invalid layout",
			[]Cell{{PNG: good}},
			Layout{Page: A4, Cols: 0, Rows: 1, CellWidthMM: 10, CellHeightMM: 10},
			ErrInvalidLayout,
			"grid is 0x1",
		},
		{
			"overflowing layout",
			[]Cell{{PNG: good}},
			Layout{Page: A4, Cols: 3, Rows: 1, CellWidthMM: 100, CellHeightMM: 10},
			ErrInvalidLayout,
			"overflowing",
		},
		{
			"not a png",
			[]Cell{{PNG: []byte("GIF89a")}},
			grid(),
			ErrBadImage,
			"cell 1 is not a readable png",
		},
		{
			"truncated png",
			[]Cell{{PNG: good}, {PNG: good[:20]}},
			grid(),
			ErrBadImage,
			"cell 2 is not a readable png",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Compose(tc.cells, tc.layout)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestComposeRejectsTooManyCells(t *testing.T) {
	t.Parallel()

	cells := make([]Cell, MaxCells+1)
	_, err := Compose(cells, grid())
	if !errors.Is(err, ErrTooManyCells) {
		t.Fatalf("err = %v, want ErrTooManyCells", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("exceeds the limit of %d", MaxCells)) {
		t.Fatalf("err = %q", err)
	}
}

func TestDecodePNGRejectsAnOversizedImage(t *testing.T) {
	t.Parallel()

	// Built at the pixel level rather than through Compose: encoding a
	// four-megapixel PNG for a unit test costs more than the assertion is
	// worth, so the guard is exercised directly.
	side := 2049 // 2049^2 is just over the 4Mi-pixel cap
	big := image.NewNRGBA(image.Rect(0, 0, side, side))
	var buf bytes.Buffer
	if err := png.Encode(&buf, big); err != nil {
		t.Fatal(err)
	}

	_, err := decodePNG(buf.Bytes(), 6)
	if !errors.Is(err, ErrBadImage) {
		t.Fatalf("err = %v, want ErrBadImage", err)
	}
	if !strings.Contains(err.Error(), "cell 7") {
		t.Fatalf("err = %q, want it to name the cell in 1-based terms", err)
	}
}

func TestComposeOnEveryTemplate(t *testing.T) {
	t.Parallel()

	// The end-to-end check: every shipped template composes a readable
	// document with one code in every cell of a full sheet.
	code := makePNG(t, 32, 32, color.Black)

	for _, tpl := range Templates() {
		t.Run(tpl.Name, func(t *testing.T) {
			t.Parallel()
			l := tpl.Layout
			l.LabelCaption = true

			n := min(l.PerPage(), MaxCells)
			cells := make([]Cell, n)
			for i := range cells {
				cells[i] = Cell{PNG: code, Caption: fmt.Sprintf("%s #%d", tpl.Name, i+1)}
			}

			doc, err := Compose(cells, l)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			parsePDF(t, doc)
			// One image for the whole sheet: the codes are identical.
			if got := countMatches(doc, "/Subtype /Image"); got != 1 {
				t.Fatalf("got %d images for %d identical cells", got, n)
			}
		})
	}
}

func TestEscapeMakesAStringSafeInsideAPDFLiteral(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{"plain", "SKU-1", "SKU-1"},
		{"open paren", "a(b", `a\(b`},
		{"close paren", "a)b", `a\)b`},
		{"backslash", `a\b`, `a\\b`},
		{"newline", "a\nb", "a b"},
		{"carriage return", "a\rb", "ab"},
		{"all at once", `(\)`, `\(\\\)`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := escape(tc.in); got != tc.want {
				t.Fatalf("escape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestComposeEscapesACaptionThatWouldBreakTheStream(t *testing.T) {
	t.Parallel()

	// An unbalanced parenthesis in a caption silently swallows the rest of the
	// content stream, so the whole page vanishes.
	l := grid()
	l.LabelCaption = true
	cells := []Cell{{PNG: makePNG(t, 16, 16, color.Black), Caption: `Ac(me \ Ltd`}}

	doc, err := Compose(cells, l)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	content := string(inflateStream(t, parsePDF(t, doc).objects[5]))
	if !strings.Contains(content, `(Ac\(me \\ Ltd) Tj`) {
		t.Fatalf("caption is not escaped: %q", content)
	}
}

func TestWinAnsiReducesACaptionToWhatTheFontCanDraw(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{"ascii", "SKU-1", "SKU-1"},
		{"latin-1 becomes one winansi byte", "Café", "Caf\xe9"},
		{"beyond latin-1", "价格", "??"},
		{"emoji", "ok 🎉", "ok ?"},
		{"tab", "a\tb", "a b"},
		{"control", "a\x01b", "ab"},
		{"trimmed", "  spaced  ", "spaced"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := winAnsi(tc.in); got != tc.want {
				t.Fatalf("winAnsi(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNumFormatsPDFNumbersWithoutTrailingZeros(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   float64
		want string
	}{
		{72, "72"},
		{0, "0"},
		{1.0 / 3.0, "0.333"},
		{210 * pointsPerInch / mmPerInch, "595.276"},
		{-1.5, "-1.5"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := num(tc.in); got != tc.want {
				t.Fatalf("num(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPTConvertsMillimetresToPoints(t *testing.T) {
	t.Parallel()

	if got := pt(25.4); got != "72" {
		t.Fatalf("pt(25.4) = %q, want 72", got)
	}
}

func TestDrawCaptionSkipsACellTooNarrowForOneCharacter(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	drawCaption(&b, A4, "x", 0, 0, 0.5)
	if b.Len() != 0 {
		t.Fatalf("wrote %q into a cell that fits no text", b.String())
	}
}

func TestDrawCaptionClipsTextToTheCellWidth(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	drawCaption(&b, A4, strings.Repeat("W", 500), 0, 0, 20)
	out := b.String()
	// 20mm at 7pt Helvetica fits roughly sixteen characters; the point is that
	// it is clipped rather than running across the neighbouring labels.
	drawn := strings.Count(out, "W")
	if drawn == 0 || drawn > 30 {
		t.Fatalf("drew %d characters into a 20mm cell: %q", drawn, out)
	}
}

// countMatches counts regexp matches in a document.
func countMatches(doc []byte, pattern string) int {
	return len(regexp.MustCompile(pattern).FindAll(doc, -1))
}

// kidNumbers extracts the object numbers from a /Kids array.
func kidNumbers(t *testing.T, pages string) []int {
	t.Helper()
	m := regexp.MustCompile(`/Kids \[([^\]]*)\]`).FindStringSubmatch(pages)
	if m == nil {
		t.Fatalf("no /Kids in %q", pages)
	}
	var out []int
	for _, ref := range regexp.MustCompile(`(\d+) 0 R`).FindAllStringSubmatch(m[1], -1) {
		n, err := strconv.Atoi(ref[1])
		if err != nil {
			t.Fatalf("bad reference %q", ref[0])
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatalf("/Kids is empty in %q", pages)
	}
	return out
}

// inflateStream decompresses the FlateDecode stream inside a PDF object.
func inflateStream(t *testing.T, obj []byte) []byte {
	t.Helper()
	start := bytes.Index(obj, []byte("stream\n"))
	if start < 0 {
		t.Fatalf("object has no stream: %q", head(obj))
	}
	body := obj[start+len("stream\n"):]
	end := bytes.LastIndex(body, []byte("\nendstream"))
	if end < 0 {
		t.Fatalf("stream is not terminated: %q", head(obj))
	}

	zr, err := zlib.NewReader(bytes.NewReader(body[:end]))
	if err != nil {
		t.Fatalf("stream is not zlib data: %v", err)
	}
	defer func() { _ = zr.Close() }()

	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("stream does not inflate: %v", err)
	}
	return raw
}

// placement pulls the width, height and origin out of the `cm` matrix that
// positions the first image in a content stream.
func placement(t *testing.T, content string) (w, h, x, y float64) {
	t.Helper()
	m := regexp.MustCompile(`q\n([-\d.]+) 0 0 ([-\d.]+) ([-\d.]+) ([-\d.]+) cm`).
		FindStringSubmatch(content)
	if m == nil {
		t.Fatalf("no image placement in the content stream: %q", content)
	}
	return mustFloat(t, m[1]), mustFloat(t, m[2]), mustFloat(t, m[3]), mustFloat(t, m[4])
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%q is not a number: %v", s, err)
	}
	return f
}

// nearly2 compares points with a tolerance of a thousandth, which is the
// precision the content stream is written to.
func nearly2(a, b float64) bool { return a-b < 0.002 && b-a < 0.002 }
