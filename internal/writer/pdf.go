package writer

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image/color"

	"github.com/el-amin-dev/barqr/internal/render"
)

// FormatPDF is the registry name of the PDF writer.
const FormatPDF = "pdf"

// Geometry shared by the three vector writers (svg, pdf, eps). It lives here
// because PDF is the canonical print path; SVG borrows only the human-readable
// band, EPS borrows the lot.
const (
	// pointsPerInch is the PostScript point, the unit both PDF and EPS measure
	// in. Everything physical in these two writers is expressed in points.
	pointsPerInch = 72.0

	// hriBandPadModules is the clearance around the human-readable line,
	// added to whatever text height the style asks for. Sizing the text
	// relative to the module keeps the line legible at every output size
	// without a second scale factor to tune.
	hriBandPadModules = 1
)

// Mean advance width of the base-14 faces, as a fraction of the font size.
//
// These centre the human-readable line. The real widths live in the fonts'
// metrics, and carrying a 224-entry table to centre one short string is not
// worth the bytes. Courier is monospaced, so 0.6 is not an approximation at
// all but the exact advance of every glyph; Helvetica's digits are 0.556em
// and its common punctuation narrower, so 0.55 lands within a character width
// for any realistic HRI.
const (
	pdfCourierWidth   = 0.6
	pdfHelveticaWidth = 0.55
)

func init() { Register(pdfWriter{}) }

// pdfWriter emits a minimal, valid PDF 1.4 document.
//
// There is no PDF library involved. A code is one page, one content stream and
// no external resources beyond a base-14 font, which is a few hundred lines of
// object plumbing — far less than the surface area of a general-purpose PDF
// toolkit, and with no risk of a dependency deciding to embed fonts, ICC
// profiles or XMP metadata into what should be a two-kilobyte file.
type pdfWriter struct{}

func (pdfWriter) Name() string      { return FormatPDF }
func (pdfWriter) MIME() string      { return "application/pdf" }
func (pdfWriter) Extension() string { return "pdf" }
func (pdfWriter) Binary() bool      { return true }

func (pdfWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	page, err := vecPageSize(c, o)
	if err != nil {
		return nil, err
	}

	stream, err := pdfDeflate(pdfContent(c, page))
	if err != nil {
		return nil, err
	}

	// Object numbers are positional: the catalog is always 1, and the font,
	// when there is one, is always 5. Keeping the layout fixed is what lets the
	// references below be written as literals.
	resources := "<< /ProcSet [/PDF] >>"
	if c.HRI != "" {
		resources = "<< /ProcSet [/PDF /Text] /Font << /F1 5 0 R >> >>"
	}

	objs := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		fmt.Appendf(nil,
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources %s /Contents 4 0 R >>",
			vecNum(page.W), vecNum(page.H), resources),
		pdfStreamObject(stream),
	}
	if c.HRI != "" {
		objs = append(objs,
			[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /"+vecFontName(c.Style.HRIFont)+" "+
				"/Encoding /WinAnsiEncoding >>"))
	}

	return pdfSerialise(objs), nil
}

// pdfStreamObject wraps compressed content in a stream object.
func pdfStreamObject(stream []byte) []byte {
	obj := fmt.Appendf(nil, "<< /Length %d /Filter /FlateDecode >>\nstream\n", len(stream))
	obj = append(obj, stream...)
	return append(obj, "\nendstream"...)
}

// pdfSerialise writes the file body, the cross-reference table and the
// trailer.
//
// The xref table is a list of byte offsets into the file, so it can only be
// built while writing: each offset is recorded as its object begins, and
// startxref records where the table itself starts. Every reader locates the
// catalog through those numbers, so an off-by-one here produces a file that
// opens in one viewer and is rejected by the next.
func pdfSerialise(objs [][]byte) []byte {
	var b bytes.Buffer

	b.WriteString("%PDF-1.4\n")
	// A comment of four high bytes on the second line. The specification asks
	// for it so that tools sniffing the head of the file — FTP clients, older
	// mail gateways — classify the document as binary and do not "helpfully"
	// translate its line endings.
	b.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})

	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		b.Write(body)
		b.WriteString("\nendobj\n")
	}

	xref := b.Len()
	// Entry zero is the head of the free list and is always this exact string.
	// Each entry is 20 bytes including the trailing space before the newline;
	// readers index into the table arithmetically, so the padding is load
	// bearing rather than cosmetic.
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}

	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xref)

	return b.Bytes()
}

// pdfContent builds the page content stream: a background rectangle, the dark
// modules as merged runs, and the human-readable line.
func pdfContent(c render.Canvas, page vecPage) []byte {
	var b bytes.Buffer
	b.Grow(c.Cols * c.Rows * 2)

	paper := c.Style.BG
	if paper.A != 0 {
		fmt.Fprintf(&b, "%s rg\n0 0 %s %s re\nf\n",
			vecRGB(vecOver(paper, vecPaper)), vecNum(page.W), vecNum(page.H))
	}

	// Rectangles are accumulated into one path per colour and filled in a
	// single `f`: a fill operator per module would triple the stream for no
	// visual difference.
	var current color.NRGBA
	pending := false
	for _, r := range vecRuns(c) {
		ink := vecOver(r.Color, paper)
		if !pending || ink != current {
			if pending {
				b.WriteString("f\n")
			}
			fmt.Fprintf(&b, "%s rg\n", vecRGB(ink))
			current, pending = ink, true
		}
		x, y, w, h := page.rect(r)
		fmt.Fprintf(&b, "%s %s %s %s re\n", vecNum(x), vecNum(y), vecNum(w), vecNum(h))
	}
	if pending {
		b.WriteString("f\n")
	}

	pdfText(&b, c, page)
	return b.Bytes()
}

// pdfText draws the human-readable line in a base-14 face, centred under the
// code.
func pdfText(b *bytes.Buffer, c render.Canvas, page vecPage) {
	if c.HRI == "" {
		return
	}

	size := hriFontModules(c) * page.Module
	width := float64(len(c.HRI)) * size * vecMeanWidth(c.Style.HRIFont)
	x := max((page.W-width)/2, 0)

	fmt.Fprintf(b, "%s rg\nBT\n/F1 %s Tf\n%s %s Td\n%s Tj\nET\n",
		vecRGB(vecOver(c.Style.FG, c.Style.BG)),
		vecNum(size), vecNum(x), vecNum(page.baseline(c)), vecString(c.HRI))
}

// pdfDeflate compresses the content stream. /FlateDecode is understood by
// every reader since PDF 1.2 and typically takes a code's stream to under a
// tenth of its size, because a merged-run stream is highly repetitive text.
func pdfDeflate(p []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(p); err != nil {
		return nil, fmt.Errorf("%w: compressing the pdf content stream: %w", ErrInvalidOutput, err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("%w: compressing the pdf content stream: %w", ErrInvalidOutput, err)
	}
	return out.Bytes(), nil
}
