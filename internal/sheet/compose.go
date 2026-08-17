package sheet

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"
)

// Limits on a compose job. They exist because the PDF holds every image
// uncompressed in memory while it is being built, so an unbounded job is an
// out-of-memory kill rather than a slow response.
const (
	// MaxCells caps a whole document. Five hundred labels is seven A4 sheets
	// of the densest stock, which is more than one print run.
	MaxCells = 500

	// maxImagePixels caps one cell's PNG. A 63.5mm label at 600 dpi is about
	// 1500 pixels square, so four megapixels is double what any sane render
	// produces and a tenth of what an accidental full-page PNG would.
	maxImagePixels = 4 << 20

	// maxTotalPixels caps the sum across a document, which is what actually
	// bounds the memory: identical images are stored once, so a sheet of the
	// same code repeated costs one image no matter how many cells it fills.
	maxTotalPixels = 24 << 20
)

// Caption geometry, in millimetres and points.
const (
	// captionBandMM is the strip reserved beneath a code for its caption. It
	// is a fixed height rather than a fraction of the cell, because the text
	// is a fixed size: a caption that scales with the label becomes unreadable
	// on 21mm mini stock and absurd on a full-sheet label.
	captionBandMM = 4.0
	// captionFontPt is the caption's size. Around 7pt is the smallest text an
	// office laser reliably renders and a person reliably reads.
	captionFontPt = 7.0
	// captionPadMM separates the code from its caption so the descenders do
	// not touch the quiet zone.
	captionPadMM = 0.6
)

// helveticaMeanWidth is the mean advance width of Helvetica as a fraction of
// the font size. Carrying the full 224-entry metrics table to centre one short
// caption is not worth the bytes; digits are 0.556em and most punctuation is
// narrower, so this lands within a character width for any real caption.
const helveticaMeanWidth = 0.5

// Cell is one position on the sheet.
//
// A Cell with no PNG leaves its position blank, which is how a caller prints
// onto a part-used sheet: pad the front of the slice with empty cells until
// the first code lands on the first unused label.
type Cell struct {
	// PNG is the rendered code. Empty means "skip this position".
	PNG []byte
	// Caption is drawn beneath the code when the layout asks for it.
	Caption string
}

// Compose lays cells out across as many pages as they need and returns a PDF.
//
// The document is written by hand: PDF 1.4, one page object per sheet, each
// image embedded as a /DeviceRGB /FlateDecode XObject. There is no PDF library
// involved because the whole grammar used here is a dozen operators, and a
// dependency would bring font embedding, ICC profiles and XMP metadata to a
// file that should be a few tens of kilobytes.
//
// Identical PNGs are embedded once and referenced many times. That is not a
// micro-optimisation: a sheet of sixty-five copies of one asset tag is a
// completely ordinary request, and it is the difference between a 40 KB file
// and a 2.6 MB one.
func Compose(cells []Cell, l Layout) ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	if len(cells) == 0 {
		return nil, ErrNoCells
	}
	if len(cells) > MaxCells {
		return nil, fmt.Errorf("%w: %d cells exceeds the limit of %d",
			ErrTooManyCells, len(cells), MaxCells)
	}

	images, refs, err := decodeCells(cells)
	if err != nil {
		return nil, err
	}

	perPage := l.PerPage()
	pageCount := (len(cells) + perPage - 1) / perPage

	// Object numbering is positional and fixed so that every reference below
	// can be computed rather than tracked: 1 catalog, 2 page tree, 3 font,
	// then the images, then a content stream and a page object per sheet.
	const firstImageObj = 4
	firstPageObj := firstImageObj + len(images)

	kids := make([]string, pageCount)
	for p := range pageCount {
		kids[p] = strconv.Itoa(firstPageObj+2*p+1) + " 0 R"
	}

	objs := make([][]byte, 0, 3+len(images)+2*pageCount)
	objs = append(objs,
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		fmt.Appendf(nil, "<< /Type /Pages /Kids [%s] /Count %d >>",
			strings.Join(kids, " "), pageCount),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica "+
			"/Encoding /WinAnsiEncoding >>"),
	)
	for _, img := range images {
		objs = append(objs, imageObject(img))
	}

	for p := range pageCount {
		from := p * perPage
		to := min(from+perPage, len(cells))

		content, used := pageContent(cells[from:to], refs[from:to], images, l)
		stream, err := deflate(content)
		if err != nil {
			return nil, err
		}
		objs = append(objs,
			streamObject(stream),
			pageObject(l.Page, used, firstImageObj, firstPageObj+2*p),
		)
	}

	return serialise(objs), nil
}

// cellImage is one decoded, compressed image ready to embed.
type cellImage struct {
	width, height int
	// data is the zlib-compressed RGB triples, row-major from the top.
	data []byte
}

// decodeCells decodes every distinct PNG once and returns, per cell, the index
// of the image it uses (-1 for a blank cell).
func decodeCells(cells []Cell) ([]cellImage, []int, error) {
	var images []cellImage
	refs := make([]int, len(cells))
	// Keyed by content hash: the same PNG in fifty cells is one XObject.
	seen := make(map[[32]byte]int, len(cells))
	total := 0

	for i, cell := range cells {
		if len(cell.PNG) == 0 {
			refs[i] = -1
			continue
		}
		key := sha256.Sum256(cell.PNG)
		if idx, ok := seen[key]; ok {
			refs[i] = idx
			continue
		}

		img, err := decodePNG(cell.PNG, i)
		if err != nil {
			return nil, nil, err
		}
		total += img.width * img.height
		if total > maxTotalPixels {
			return nil, nil, fmt.Errorf(
				"%w: the distinct images total more than %d pixels; render the codes smaller",
				ErrTooManyCells, maxTotalPixels)
		}

		refs[i] = len(images)
		seen[key] = len(images)
		images = append(images, img)
	}

	return images, refs, nil
}

// decodePNG turns one cell's PNG into raw RGB.
//
// Alpha is composited over white rather than carried into the PDF: an /SMask
// would double the object count for a transparency that means "paper" on every
// sheet of label stock there is.
func decodePNG(data []byte, index int) (cellImage, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return cellImage{}, fmt.Errorf("%w: cell %d is not a readable png", ErrBadImage, index+1)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return cellImage{}, fmt.Errorf("%w: cell %d has no pixels", ErrBadImage, index+1)
	}
	if w*h > maxImagePixels {
		return cellImage{}, fmt.Errorf("%w: cell %d is %dx%d, over the %d-pixel limit",
			ErrBadImage, index+1, w, h, maxImagePixels)
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)

	// After compositing over opaque white every pixel is alpha 255, so the
	// premultiplied RGBA values are already the straight RGB the PDF wants.
	raw := make([]byte, 0, w*h*3)
	for y := range h {
		row := y * dst.Stride
		for x := range w {
			p := row + x*4
			raw = append(raw, dst.Pix[p], dst.Pix[p+1], dst.Pix[p+2])
		}
	}

	packed, err := deflate(raw)
	if err != nil {
		return cellImage{}, err
	}
	return cellImage{width: w, height: h, data: packed}, nil
}

// imageObject builds the XObject dictionary and stream for one image.
func imageObject(img cellImage) []byte {
	obj := fmt.Appendf(nil,
		"<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB "+
			"/BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n",
		img.width, img.height, len(img.data))
	obj = append(obj, img.data...)
	return append(obj, "\nendstream"...)
}

// pageContent draws one page and reports which images it referenced, so the
// page's resource dictionary names only what it uses.
func pageContent(cells []Cell, refs []int, images []cellImage, l Layout) ([]byte, []int) {
	var b bytes.Buffer
	var used []int
	seen := make(map[int]bool, len(cells))

	// Text is black; the codes carry their own colour in their pixels.
	b.WriteString("0 g\n")

	for i, cell := range cells {
		x, y, w, h, ok := l.CellRectMM(i)
		if !ok {
			// Unreachable: the slice is already bounded by PerPage.
			break
		}

		band := 0.0
		caption := ""
		if l.LabelCaption && cell.Caption != "" {
			band = captionBandMM
			caption = winAnsi(cell.Caption)
		}

		if ref := refs[i]; ref >= 0 {
			drawImage(&b, l.Page, images[ref], ref, x, y, w, h-band-captionPadMM)
			if !seen[ref] {
				seen[ref] = true
				used = append(used, ref)
			}
		}
		if caption != "" {
			drawCaption(&b, l.Page, caption, x, y+h-band, w)
		}
	}

	return b.Bytes(), used
}

// drawImage places one image inside a cell, centred and aspect-preserving.
//
// A code stretched to fill a non-square cell is a code that will not scan, so
// the fit is always uniform and the spare space stays as margin.
func drawImage(b *bytes.Buffer, page PageSize, img cellImage, ref int, x, y, w, h float64) {
	if w <= 0 || h <= 0 {
		return
	}
	aspect := float64(img.width) / float64(img.height)
	drawW := min(w, h*aspect)
	drawH := drawW / aspect

	// Centre within the cell, then flip into PDF space, whose origin is the
	// bottom-left of the page rather than the top-left of the datasheet.
	left := x + (w-drawW)/2
	top := y + (h-drawH)/2
	bottom := page.HeightMM - top - drawH

	// `w 0 0 h dx dy cm` maps the unit square an image occupies onto the
	// rectangle we want, which is the whole of image placement in PDF.
	fmt.Fprintf(b, "q\n%s 0 0 %s %s %s cm\n/Im%d Do\nQ\n",
		pt(drawW), pt(drawH), pt(left), pt(bottom), ref)
}

// drawCaption writes one line of Helvetica centred in the caption band.
func drawCaption(b *bytes.Buffer, page PageSize, text string, x, y, w float64) {
	widthPt := w * pointsPerInch / mmPerInch
	// Clip to what fits rather than letting the line run into the next label.
	maxChars := int(widthPt / (captionFontPt * helveticaMeanWidth))
	if maxChars < 1 {
		return
	}
	if len(text) > maxChars {
		text = text[:maxChars]
	}

	textPt := float64(len(text)) * captionFontPt * helveticaMeanWidth
	left := x*pointsPerInch/mmPerInch + max((widthPt-textPt)/2, 0)
	// The band's baseline sits a caption's descender above its bottom edge.
	baseline := (page.HeightMM-y-captionBandMM)*pointsPerInch/mmPerInch + captionFontPt*0.25

	fmt.Fprintf(b, "BT\n/F1 %s Tf\n%s %s Td\n(%s) Tj\nET\n",
		num(captionFontPt), num(left), num(baseline), escape(text))
}

// pageObject builds one page's dictionary, naming only the images it uses.
func pageObject(page PageSize, used []int, firstImageObj, contentObj int) []byte {
	xobjects := make([]string, 0, len(used))
	for _, ref := range used {
		xobjects = append(xobjects, fmt.Sprintf("/Im%d %d 0 R", ref, firstImageObj+ref))
	}

	resources := "<< /ProcSet [/PDF /Text /ImageC] /Font << /F1 3 0 R >>"
	if len(xobjects) > 0 {
		resources += " /XObject << " + strings.Join(xobjects, " ") + " >>"
	}
	resources += " >>"

	return fmt.Appendf(nil,
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources %s /Contents %d 0 R >>",
		pt(page.WidthMM), pt(page.HeightMM), resources, contentObj)
}

// streamObject wraps compressed bytes in a stream object.
func streamObject(stream []byte) []byte {
	obj := fmt.Appendf(nil, "<< /Length %d /Filter /FlateDecode >>\nstream\n", len(stream))
	obj = append(obj, stream...)
	return append(obj, "\nendstream"...)
}

// serialise writes the file body, the cross-reference table and the trailer.
//
// The xref table is a list of byte offsets into the file, so it can only be
// built while writing: each offset is recorded as its object begins, and
// startxref records where the table itself starts. Every reader finds the
// catalog through those numbers, so an off-by-one here produces a file that
// opens in one viewer and is rejected by the next.
func serialise(objs [][]byte) []byte {
	var b bytes.Buffer

	b.WriteString("%PDF-1.4\n")
	// A comment of four high bytes on the second line. The specification asks
	// for it so that tools sniffing the head of the file classify the document
	// as binary and do not "helpfully" translate its line endings.
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

// deflate zlib-compresses a buffer for a /FlateDecode stream.
func deflate(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("compressing a pdf stream: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compressing a pdf stream: %w", err)
	}
	return out.Bytes(), nil
}

// pt converts millimetres to PostScript points and formats the result.
func pt(mm float64) string { return num(mm * pointsPerInch / mmPerInch) }

// num formats a PDF number: three decimals is a thousandth of a point, well
// below what any output device resolves, and trimming the zeros keeps the
// content stream readable when something goes wrong.
func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// escape makes a string safe inside a PDF literal string, where an unbalanced
// parenthesis silently swallows the rest of the content stream.
func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, "\r", "", "\n", " ")
	return r.Replace(s)
}

// winAnsi reduces a caption to what the base-14 Helvetica can show.
//
// The font is referenced with /WinAnsiEncoding and no embedded glyphs, so a
// rune outside that single-byte range has nothing to draw. Substituting a
// question mark is honest: the alternative is a caption that renders as blank
// or as mojibake depending on the viewer.
func winAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7F:
			// Control characters have no glyph and can confuse a parser.
			continue
		case r >= 0xA0 && r <= 0xFF:
			// WinAnsi agrees with Latin-1 from 0xA0 up, so accented Latin
			// captions pass through unchanged.
			b.WriteByte(byte(r))
		default:
			b.WriteByte('?')
		}
	}
	return strings.TrimSpace(b.String())
}
