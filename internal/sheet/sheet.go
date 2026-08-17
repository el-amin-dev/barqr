// Package sheet lays rendered codes out on a page for printing.
//
// The unit of work is a Layout: a page size, a grid of cells, and the margins
// and gutters that put the grid where the die-cut labels actually are. Sheet
// knows nothing about encoding or rendering — Compose takes PNGs that someone
// else produced and arranges them — so the geometry can be reasoned about and
// tested without an encoder anywhere near it.
//
// Everything physical is in millimetres, because that is the unit every label
// manufacturer publishes their stock in, and converting once at the PDF
// boundary is less error-prone than carrying points through the arithmetic.
// The origin for a Layout is the top-left of the page with y increasing
// downwards, which is how a datasheet describes a label sheet; Compose flips
// it to PDF's bottom-left origin at the last possible moment.
package sheet

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for the sheet package.
var (
	// ErrInvalidLayout means the grid cannot be placed on the page.
	ErrInvalidLayout = errors.New("invalid sheet layout")
	// ErrNoCells means Compose was given nothing to lay out.
	ErrNoCells = errors.New("no cells to compose")
	// ErrTooManyCells means the job exceeds what one document may carry.
	ErrTooManyCells = errors.New("too many cells")
	// ErrBadImage means a cell's PNG could not be decoded or is too large.
	ErrBadImage = errors.New("invalid cell image")
)

// mmPerInch and pointsPerInch convert the layout's millimetres into the
// PostScript points a PDF measures in. They are exact by definition, so the
// conversion introduces no error of its own.
const (
	mmPerInch     = 25.4
	pointsPerInch = 72.0
)

// fitTolerance is the slack allowed when checking that a grid fits its page.
//
// Label stock is published to a tenth of a millimetre and the arithmetic that
// follows accumulates a rounding error per column, so an exact comparison
// would reject templates that print perfectly. A twentieth of a millimetre is
// well inside the registration tolerance of any office printer.
const fitTolerance = 0.05

// maxCellsPerPage bounds a single page's grid. The densest stock in real use
// is around 189 labels per A4 sheet; a thousand is a comfortable ceiling that
// still stops a layout of one-micrometre cells from generating a document
// nothing can open.
const maxCellsPerPage = 1000

// PageSize is a named sheet of paper, in millimetres.
type PageSize struct {
	// Name is the size's common name, e.g. "A4".
	Name string
	// WidthMM and HeightMM are the portrait dimensions.
	WidthMM, HeightMM float64
}

// The page sizes barqr lays out on. A4 is the ISO default everywhere outside
// North America, Letter is the default inside it, and A3 is what a print shop
// uses to gang up several sheets.
var (
	// A4 is ISO 216 A4.
	A4 = PageSize{Name: "a4", WidthMM: 210, HeightMM: 297}
	// Letter is ANSI A, 8.5 by 11 inches.
	Letter = PageSize{Name: "letter", WidthMM: 215.9, HeightMM: 279.4}
	// A3 is ISO 216 A3.
	A3 = PageSize{Name: "a3", WidthMM: 297, HeightMM: 420}
)

// Pages returns the supported page sizes.
func Pages() []PageSize { return []PageSize{A4, Letter, A3} }

// PageByName returns a page size by its lowercase name.
func PageByName(name string) (PageSize, bool) {
	for _, p := range Pages() {
		if p.Name == lower(name) {
			return p, true
		}
	}
	return PageSize{}, false
}

// Layout is a grid of cells on a page.
//
// A cell is where one code goes. The grid is described the way a label
// manufacturer describes it — offset to the first label, label size, gap to
// the next — rather than as a set of absolute positions, because that is what
// the datasheet gives and what a user measuring their own stock can supply.
type Layout struct {
	// Page is the sheet being printed on.
	Page PageSize
	// Cols and Rows are the grid dimensions.
	Cols, Rows int
	// MarginTopMM and MarginLeftMM position the first cell's top-left corner.
	MarginTopMM, MarginLeftMM float64
	// CellWidthMM and CellHeightMM are one cell's dimensions.
	CellWidthMM, CellHeightMM float64
	// GutterXMM and GutterYMM are the gaps between cells. Most die-cut stock
	// has a horizontal gutter and none vertically.
	GutterXMM, GutterYMM float64
	// LabelCaption draws each cell's caption beneath its code.
	LabelCaption bool
}

// PerPage returns how many cells fit on one page.
func (l Layout) PerPage() int {
	if l.Cols <= 0 || l.Rows <= 0 {
		return 0
	}
	return l.Cols * l.Rows
}

// Validate reports whether the layout can be printed.
//
// The check that matters is the fit: a grid whose columns are wider than the
// page produces a PDF that silently loses its right-hand labels, which is only
// discovered after someone has fed a sheet of adhesive stock through a
// printer. The error names the overflow in millimetres so the caller knows how
// much to take off.
func (l Layout) Validate() error {
	switch {
	case l.Page.WidthMM <= 0 || l.Page.HeightMM <= 0:
		return fmt.Errorf("%w: page is %s; both dimensions must be positive",
			ErrInvalidLayout, dims(l.Page.WidthMM, l.Page.HeightMM))
	case l.Cols <= 0 || l.Rows <= 0:
		return fmt.Errorf("%w: grid is %dx%d; both must be at least 1",
			ErrInvalidLayout, l.Cols, l.Rows)
	case l.PerPage() > maxCellsPerPage:
		return fmt.Errorf("%w: %d cells per page exceeds the limit of %d",
			ErrInvalidLayout, l.PerPage(), maxCellsPerPage)
	case l.CellWidthMM <= 0 || l.CellHeightMM <= 0:
		return fmt.Errorf("%w: cell is %s; both dimensions must be positive",
			ErrInvalidLayout, dims(l.CellWidthMM, l.CellHeightMM))
	case l.MarginLeftMM < 0 || l.MarginTopMM < 0:
		return fmt.Errorf("%w: margins are %s; neither may be negative",
			ErrInvalidLayout, dims(l.MarginLeftMM, l.MarginTopMM))
	case l.GutterXMM < 0 || l.GutterYMM < 0:
		return fmt.Errorf("%w: gutters are %s; neither may be negative",
			ErrInvalidLayout, dims(l.GutterXMM, l.GutterYMM))
	}

	if err := l.fits("width", l.Cols, l.MarginLeftMM, l.CellWidthMM, l.GutterXMM,
		l.Page.WidthMM); err != nil {
		return err
	}
	return l.fits("height", l.Rows, l.MarginTopMM, l.CellHeightMM, l.GutterYMM, l.Page.HeightMM)
}

// fits checks one axis of the grid against the page and reports the overflow.
func (l Layout) fits(axis string, count int, margin, cell, gutter, page float64) error {
	// The last cell has no gutter after it, which is the off-by-one that makes
	// a grid look like it does not fit when it does.
	needed := margin + float64(count)*cell + float64(count-1)*gutter
	if needed <= page+fitTolerance {
		return nil
	}
	unit := "columns"
	if axis == "height" {
		unit = "rows"
	}
	return fmt.Errorf(
		"%w: %d %s of %.2fmm with %.2fmm gutters and a %.2fmm margin need %.2fmm, "+
			"overflowing the %.2fmm page %s by %.2fmm",
		ErrInvalidLayout, count, unit, cell, gutter, margin, needed, page, axis, needed-page)
}

// CellRectMM returns the cell at index, in millimetres from the page's
// top-left corner with y increasing downwards.
//
// Cells are numbered left to right, then top to bottom — reading order, which
// is the order someone peeling labels off a sheet expects. ok is false when
// index is outside the page's grid, which is how a caller walks a page.
func (l Layout) CellRectMM(index int) (x, y, w, h float64, ok bool) {
	if index < 0 || index >= l.PerPage() {
		return 0, 0, 0, 0, false
	}
	col := index % l.Cols
	row := index / l.Cols
	x = l.MarginLeftMM + float64(col)*(l.CellWidthMM+l.GutterXMM)
	y = l.MarginTopMM + float64(row)*(l.CellHeightMM+l.GutterYMM)
	return x, y, l.CellWidthMM, l.CellHeightMM, true
}

// dims formats a millimetre pair for an error message.
func dims(a, b float64) string { return fmt.Sprintf("%.2fmm by %.2fmm", a, b) }

// lower normalises a name from a query parameter, where case and stray space
// are the caller's typing rather than their intent.
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
