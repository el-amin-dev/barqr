package sheet

import (
	"slices"
	"strings"
)

// Template is a named label stock: a layout that matches a real product.
//
// The numbers are the manufacturer's published dimensions, not measurements
// taken off a printout. Anything else drifts: a template that is a fraction of
// a millimetre out is invisible on the first row and half a label out by the
// bottom of the sheet.
type Template struct {
	// Name is the slug a caller asks for, e.g. "avery-l7160".
	Name string
	// Title is the human-readable stock description.
	Title string
	// Layout is the grid this stock defines.
	Layout Layout
}

// templates is the built-in stock list.
//
// Each entry cites the product it matches. Avery's European (L-prefixed) and
// North American (numeric) ranges cover the overwhelming majority of adhesive
// label stock in offices; the Dymo entry is there because a roll printer has
// no sheet at all and its "page" is the label, which is a case a grid-only
// model would otherwise get wrong.
var templates = []Template{
	{
		// Avery L7160 / 3652 — 21 address labels per A4 sheet, 63.5 x 38.1 mm.
		Name:  "avery-l7160",
		Title: "Avery L7160 — 21 address labels per A4 sheet, 63.5 x 38.1 mm",
		Layout: Layout{
			Page: A4, Cols: 3, Rows: 7,
			MarginTopMM: 15.1, MarginLeftMM: 7.2,
			CellWidthMM: 63.5, CellHeightMM: 38.1,
			GutterXMM: 2.5, GutterYMM: 0,
		},
	},
	{
		// Avery L7159 — 24 labels per A4 sheet, 63.5 x 33.9 mm.
		Name:  "avery-l7159",
		Title: "Avery L7159 — 24 labels per A4 sheet, 63.5 x 33.9 mm",
		Layout: Layout{
			Page: A4, Cols: 3, Rows: 8,
			MarginTopMM: 13.1, MarginLeftMM: 7.2,
			CellWidthMM: 63.5, CellHeightMM: 33.9,
			GutterXMM: 2.5, GutterYMM: 0,
		},
	},
	{
		// Avery L7163 — 14 shipping labels per A4 sheet, 99.1 x 38.1 mm.
		Name:  "avery-l7163",
		Title: "Avery L7163 — 14 shipping labels per A4 sheet, 99.1 x 38.1 mm",
		Layout: Layout{
			Page: A4, Cols: 2, Rows: 7,
			MarginTopMM: 15.1, MarginLeftMM: 4.65,
			CellWidthMM: 99.1, CellHeightMM: 38.1,
			GutterXMM: 2.5, GutterYMM: 0,
		},
	},
	{
		// Avery L7651 — 65 mini labels per A4 sheet, 38.1 x 21.2 mm. The
		// densest common stock, and the one that exposes an off-by-one in the
		// gutter arithmetic soonest.
		Name:  "avery-l7651",
		Title: "Avery L7651 — 65 mini labels per A4 sheet, 38.1 x 21.2 mm",
		Layout: Layout{
			Page: A4, Cols: 5, Rows: 13,
			MarginTopMM: 10.7, MarginLeftMM: 4.75,
			CellWidthMM: 38.1, CellHeightMM: 21.2,
			GutterXMM: 2.5, GutterYMM: 0,
		},
	},
	{
		// Avery L7167 — one full-face label per A4 sheet, 199.6 x 289.1 mm.
		Name:  "avery-l7167",
		Title: "Avery L7167 — 1 full-sheet label per A4 sheet, 199.6 x 289.1 mm",
		Layout: Layout{
			Page: A4, Cols: 1, Rows: 1,
			MarginTopMM: 3.9, MarginLeftMM: 5.2,
			CellWidthMM: 199.6, CellHeightMM: 289.1,
		},
	},
	{
		// Avery 5160 — 30 address labels per US Letter sheet, 2 5/8 x 1 in.
		Name:  "avery-5160",
		Title: "Avery 5160 — 30 address labels per Letter sheet, 66.68 x 25.4 mm",
		Layout: Layout{
			Page: Letter, Cols: 3, Rows: 10,
			MarginTopMM: 12.7, MarginLeftMM: 4.76,
			CellWidthMM: 66.68, CellHeightMM: 25.4,
			GutterXMM: 3.18, GutterYMM: 0,
		},
	},
	{
		// Avery 5163 — 10 shipping labels per US Letter sheet, 4 x 2 in.
		Name:  "avery-5163",
		Title: "Avery 5163 — 10 shipping labels per Letter sheet, 101.6 x 50.8 mm",
		Layout: Layout{
			Page: Letter, Cols: 2, Rows: 5,
			MarginTopMM: 12.7, MarginLeftMM: 4.76,
			CellWidthMM: 101.6, CellHeightMM: 50.8,
			GutterXMM: 4.76, GutterYMM: 0,
		},
	},
	{
		// Dymo 99012 — a 89 x 36 mm roll label. There is no sheet: the page is
		// the label, which is why margins and gutters are all zero.
		Name:  "dymo-99012",
		Title: "Dymo 99012 — single large address label on a roll, 89 x 36 mm",
		Layout: Layout{
			Page: PageSize{Name: "dymo-99012", WidthMM: 89, HeightMM: 36},
			Cols: 1, Rows: 1,
			CellWidthMM: 89, CellHeightMM: 36,
		},
	},
}

// Templates returns the built-in label stock, sorted by name.
//
// The Layouts are values, so a caller may adjust one — turning captions on,
// nudging a margin for a printer that runs low — without disturbing anyone
// else's copy.
func Templates() []Template {
	out := slices.Clone(templates)
	slices.SortFunc(out, func(a, b Template) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// TemplateByName returns the stock with the given name. Lookup ignores case
// and surrounding space, because the name arrives from a query parameter.
func TemplateByName(name string) (Template, bool) {
	want := lower(name)
	for _, t := range templates {
		if t.Name == want {
			return t, true
		}
	}
	return Template{}, false
}
