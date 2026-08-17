package writer

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// FormatEPS is the registry name of the Encapsulated PostScript writer.
const FormatEPS = "eps"

// epsRectProc defines the one procedure the body needs: fill the rectangle
// (x y w h) taken from the stack.
//
// Spelling the rectangle out per module would repeat six operators several
// thousand times. With the procedure each module run costs five tokens, which
// is the difference between a 40 KB and a 250 KB file for a dense code.
const epsRectProc = "/R { newpath 4 2 roll moveto 1 index 0 rlineto 0 exch rlineto " +
	"neg 0 rlineto closepath fill } bind def"

func init() { Register(epsWriter{}) }

// epsWriter emits Encapsulated PostScript.
//
// EPS exists in this set for the printing and prepress workflows that still
// require it — sign cutters, label imposition software, older Illustrator
// pipelines — where a PDF is not accepted. It shares all of its geometry with
// the PDF writer, since both measure in points from a bottom-left origin; only
// the syntax differs.
type epsWriter struct{}

func (epsWriter) Name() string      { return FormatEPS }
func (epsWriter) MIME() string      { return "application/postscript" }
func (epsWriter) Extension() string { return "eps" }
func (epsWriter) Binary() bool      { return false }

func (epsWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	page, err := vecPageSize(c, o)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.Grow(c.Cols*c.Rows*2 + 512)

	epsHeader(&b, c, page)

	// gsave/grestore around the body so that an EPS placed inside another
	// PostScript document leaves the enclosing graphics state untouched.
	b.WriteString("gsave\n")
	epsBody(&b, c, page)
	epsText(&b, c, page)
	b.WriteString("grestore\nshowpage\n%%EOF\n")

	return b.Bytes(), nil
}

// epsHeader writes the DSC comment block and the prolog.
//
// The BoundingBox is the part consumers actually read: a placing application
// scales and positions the figure from it alone, so it must be integer points
// and must not clip the artwork. It is therefore rounded outwards, with the
// exact size given alongside as a HiResBoundingBox for tools that honour it.
func epsHeader(b *bytes.Buffer, c render.Canvas, page vecPage) {
	b.WriteString("%!PS-Adobe-3.0 EPSF-3.0\n")
	b.WriteString("%%Creator: barqr\n")
	fmt.Fprintf(b, "%%%%Title: %s\n", epsComment(c.Symbology))
	fmt.Fprintf(b, "%%%%BoundingBox: 0 0 %d %d\n",
		int(math.Ceil(page.W)), int(math.Ceil(page.H)))
	fmt.Fprintf(b, "%%%%HiResBoundingBox: 0 0 %s %s\n", vecNum(page.W), vecNum(page.H))
	b.WriteString("%%LanguageLevel: 2\n")
	b.WriteString("%%Pages: 1\n")
	b.WriteString("%%EndComments\n")
	b.WriteString("%%BeginProlog\n")
	b.WriteString(epsRectProc + "\n")
	b.WriteString("%%EndProlog\n")
}

// epsBody paints the background and the dark modules.
func epsBody(b *bytes.Buffer, c render.Canvas, page vecPage) {
	paper := c.Style.BG
	if paper.A != 0 {
		fmt.Fprintf(b, "%s setrgbcolor\n0 0 %s %s R\n",
			vecRGB(vecOver(paper, vecPaper)), vecNum(page.W), vecNum(page.H))
	}

	// Horizontally merged runs, as in the PDF writer: one R per run instead of
	// one per module.
	var current color.NRGBA
	first := true
	for _, r := range vecRuns(c) {
		ink := vecOver(r.Color, paper)
		if first || ink != current {
			fmt.Fprintf(b, "%s setrgbcolor\n", vecRGB(ink))
			current, first = ink, false
		}
		x, y, w, h := page.rect(r)
		fmt.Fprintf(b, "%s %s %s %s R\n", vecNum(x), vecNum(y), vecNum(w), vecNum(h))
	}
}

// epsText draws the human-readable line, centred beneath the code.
//
// PostScript can measure the string itself, so unlike the PDF writer this does
// not have to approximate Helvetica's metrics: stringwidth returns the exact
// advance and the text is shifted back by half of it.
func epsText(b *bytes.Buffer, c render.Canvas, page vecPage) {
	if c.HRI == "" {
		return
	}

	fmt.Fprintf(b, "%s setrgbcolor\n", vecRGB(vecOver(c.Style.FG, c.Style.BG)))
	fmt.Fprintf(b, "/Helvetica findfont %s scalefont setfont\n",
		vecNum(hriFontModules*page.Module))
	fmt.Fprintf(b, "%s %s moveto\n", vecNum(page.W/2), vecNum(page.baseline(c)))
	fmt.Fprintf(b, "%s dup stringwidth pop 2 div neg 0 rmoveto show\n", vecString(c.HRI))
}

// epsComment sanitises a value destined for a DSC comment.
//
// A DSC comment is terminated by the line break, so an embedded newline would
// let the remainder of the value be read as PostScript. Only printable ASCII
// survives; the field is decorative and a lost character costs nothing.
func epsComment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		if ch := s[i]; ch >= 0x20 && ch <= 0x7E {
			b.WriteByte(ch)
		}
	}
	if b.Len() == 0 {
		return "barqr code"
	}
	return b.String()
}
