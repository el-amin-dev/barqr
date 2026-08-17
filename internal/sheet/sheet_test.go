package sheet

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

// grid is a small, obviously-correct layout for the geometry tests.
func grid() Layout {
	return Layout{
		Page: A4, Cols: 3, Rows: 4,
		MarginTopMM: 10, MarginLeftMM: 5,
		CellWidthMM: 60, CellHeightMM: 40,
		GutterXMM: 4, GutterYMM: 2,
	}
}

func TestPagesAreTheDocumentedSizes(t *testing.T) {
	t.Parallel()

	want := map[string][2]float64{
		"a4":     {210, 297},
		"letter": {215.9, 279.4},
		"a3":     {297, 420},
	}

	pages := Pages()
	if len(pages) != len(want) {
		t.Fatalf("Pages() = %v, want %d sizes", pages, len(want))
	}
	for _, p := range pages {
		w, ok := want[p.Name]
		if !ok {
			t.Fatalf("unexpected page %q", p.Name)
		}
		if p.WidthMM != w[0] || p.HeightMM != w[1] {
			t.Fatalf("%s = %gx%g, want %gx%g", p.Name, p.WidthMM, p.HeightMM, w[0], w[1])
		}
	}
}

func TestPageByNameIgnoresCaseAndSpace(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"a4", "A4", "  A4 "} {
		p, ok := PageByName(name)
		if !ok || p.WidthMM != 210 {
			t.Fatalf("PageByName(%q) = %+v, %v", name, p, ok)
		}
	}
	if _, ok := PageByName("b5"); ok {
		t.Fatal("PageByName accepted an unsupported size")
	}
}

func TestPerPageCountsTheGrid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cols, rows int
		want       int
	}{
		{"three by four", 3, 4, 12},
		{"single", 1, 1, 1},
		{"no columns", 0, 4, 0},
		{"no rows", 3, 0, 0},
		{"negative", -3, 4, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := Layout{Cols: tc.cols, Rows: tc.rows}
			if got := l.PerPage(); got != tc.want {
				t.Fatalf("PerPage() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCellRectMMWalksTheGridInReadingOrder(t *testing.T) {
	t.Parallel()

	l := grid()
	cases := []struct {
		name           string
		index          int
		wantX, wantY   float64
		wantW, wantH   float64
		wantOutOfRange bool
	}{
		{name: "first", index: 0, wantX: 5, wantY: 10, wantW: 60, wantH: 40},
		{name: "second column", index: 1, wantX: 5 + 64, wantY: 10, wantW: 60, wantH: 40},
		{name: "third column", index: 2, wantX: 5 + 128, wantY: 10, wantW: 60, wantH: 40},
		{name: "wraps to the next row", index: 3, wantX: 5, wantY: 52, wantW: 60, wantH: 40},
		{name: "last", index: 11, wantX: 5 + 128, wantY: 10 + 3*42, wantW: 60, wantH: 40},
		{name: "past the end", index: 12, wantOutOfRange: true},
		{name: "negative", index: -1, wantOutOfRange: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x, y, w, h, ok := l.CellRectMM(tc.index)
			if tc.wantOutOfRange {
				if ok {
					t.Fatalf("CellRectMM(%d) reported a cell outside the grid", tc.index)
				}
				return
			}
			if !ok {
				t.Fatalf("CellRectMM(%d) = not ok", tc.index)
			}
			if !nearly(x, tc.wantX) || !nearly(y, tc.wantY) ||
				!nearly(w, tc.wantW) || !nearly(h, tc.wantH) {
				t.Fatalf("CellRectMM(%d) = %g,%g,%g,%g, want %g,%g,%g,%g",
					tc.index, x, y, w, h, tc.wantX, tc.wantY, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestCellRectMMKeepsEveryCellOnThePage(t *testing.T) {
	t.Parallel()

	// The property that matters: a layout that validates never places a cell
	// off the paper, on any template.
	for _, tpl := range Templates() {
		t.Run(tpl.Name, func(t *testing.T) {
			t.Parallel()
			l := tpl.Layout
			for i := range l.PerPage() {
				x, y, w, h, ok := l.CellRectMM(i)
				if !ok {
					t.Fatalf("cell %d is missing", i)
				}
				if x < 0 || y < 0 {
					t.Fatalf("cell %d starts at %g,%g", i, x, y)
				}
				if x+w > l.Page.WidthMM+fitTolerance {
					t.Fatalf("cell %d ends at %gmm, past the %gmm page width",
						i, x+w, l.Page.WidthMM)
				}
				if y+h > l.Page.HeightMM+fitTolerance {
					t.Fatalf("cell %d ends at %gmm, past the %gmm page height",
						i, y+h, l.Page.HeightMM)
				}
			}
		})
	}
}

func TestValidateAcceptsAWorkableGrid(t *testing.T) {
	t.Parallel()

	if err := grid().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnprintableLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*Layout)
		wants  string
	}{
		{"no page", func(l *Layout) { l.Page = PageSize{Name: "none"} }, "page is 0.00mm"},
		{"negative page", func(l *Layout) { l.Page.HeightMM = -1 }, "must be positive"},
		{"no columns", func(l *Layout) { l.Cols = 0 }, "grid is 0x4"},
		{"no rows", func(l *Layout) { l.Rows = 0 }, "grid is 3x0"},
		{"zero cell", func(l *Layout) { l.CellWidthMM = 0 }, "cell is 0.00mm"},
		{"negative cell", func(l *Layout) { l.CellHeightMM = -5 }, "must be positive"},
		{"negative margin", func(l *Layout) { l.MarginLeftMM = -1 }, "margins are"},
		{"negative gutter", func(l *Layout) { l.GutterYMM = -1 }, "gutters are"},
		{"too dense", func(l *Layout) { l.Cols, l.Rows = 40, 40 }, "exceeds the limit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := grid()
			tc.mutate(&l)
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLayout) {
				t.Fatalf("err = %v, want ErrInvalidLayout", err)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestValidateNamesTheOverflowInMillimetres(t *testing.T) {
	t.Parallel()

	// Four 99.1mm columns with 2.5mm gutters and a 4.65mm margin need
	// 4.65 + 396.4 + 7.5 = 408.55mm on a 210mm page: 198.55mm too wide.
	wide := Layout{
		Page: A4, Cols: 4, Rows: 1,
		MarginLeftMM: 4.65, MarginTopMM: 10,
		CellWidthMM: 99.1, CellHeightMM: 38.1,
		GutterXMM: 2.5,
	}

	err := wide.Validate()
	if !errors.Is(err, ErrInvalidLayout) {
		t.Fatalf("err = %v, want ErrInvalidLayout", err)
	}
	for _, want := range []string{"width", "408.55mm", "210.00mm", "by 198.55mm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestValidateNamesAHeightOverflow(t *testing.T) {
	t.Parallel()

	tall := grid()
	tall.Rows = 8 // 10 + 8*40 + 7*2 = 344mm on a 297mm page

	err := tall.Validate()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"height", "rows", "344.00mm", "by 47.00mm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestValidateToleratesTheRoundingInPublishedStock(t *testing.T) {
	t.Parallel()

	// A grid that exactly fills the page must pass, and one a hair over the
	// tolerance must not — that boundary is what stops real templates being
	// rejected while still catching a genuine overflow.
	exact := Layout{
		Page: A4, Cols: 2, Rows: 1,
		CellWidthMM: 105, CellHeightMM: 50,
	}
	if err := exact.Validate(); err != nil {
		t.Fatalf("an exactly-fitting grid was rejected: %v", err)
	}

	over := exact
	over.CellWidthMM = 105 + fitTolerance
	if err := over.Validate(); err == nil {
		t.Fatal("a grid past the tolerance was accepted")
	}
}

func TestEveryBuiltInTemplateValidates(t *testing.T) {
	t.Parallel()

	tpls := Templates()
	if len(tpls) < 6 {
		t.Fatalf("got %d templates, want at least six real ones", len(tpls))
	}
	for _, tpl := range tpls {
		t.Run(tpl.Name, func(t *testing.T) {
			t.Parallel()
			if err := tpl.Layout.Validate(); err != nil {
				t.Fatalf("%s does not fit its page: %v", tpl.Name, err)
			}
			if tpl.Title == "" {
				t.Fatalf("%s has no title", tpl.Name)
			}
		})
	}
}

func TestTemplatesAreSortedAndUnique(t *testing.T) {
	t.Parallel()

	tpls := Templates()
	names := make([]string, len(tpls))
	for i, tpl := range tpls {
		names[i] = tpl.Name
	}
	if !slices.IsSorted(names) {
		t.Fatalf("Templates() = %v, want them sorted", names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("template %q is listed twice", n)
		}
		seen[n] = true
	}
}

func TestTemplatesReturnsACopy(t *testing.T) {
	t.Parallel()

	// Callers adjust a template — turning captions on, nudging a margin for a
	// printer that runs low — and must not disturb anyone else's copy.
	Templates()[0].Layout.MarginTopMM = 999
	if got := Templates()[0].Layout.MarginTopMM; got == 999 {
		t.Fatal("Templates() handed out the package's own slice")
	}
}

func TestTemplateByNameFindsRealStock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		lookup     string
		cols, rows int
		cellW      float64
		cellH      float64
	}{
		{"avery-l7160", "avery-l7160", 3, 7, 63.5, 38.1},
		{"case insensitive", "Avery-L7160", 3, 7, 63.5, 38.1},
		{"padded", "  avery-l7651  ", 5, 13, 38.1, 21.2},
		{"letter stock", "avery-5160", 3, 10, 66.68, 25.4},
		{"roll label", "dymo-99012", 1, 1, 89, 36},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tpl, ok := TemplateByName(tc.lookup)
			if !ok {
				t.Fatalf("TemplateByName(%q) not found", tc.lookup)
			}
			l := tpl.Layout
			if l.Cols != tc.cols || l.Rows != tc.rows {
				t.Fatalf("%s grid = %dx%d, want %dx%d", tc.lookup, l.Cols, l.Rows, tc.cols, tc.rows)
			}
			if !nearly(l.CellWidthMM, tc.cellW) || !nearly(l.CellHeightMM, tc.cellH) {
				t.Fatalf("%s cell = %gx%g, want %gx%g",
					tc.lookup, l.CellWidthMM, l.CellHeightMM, tc.cellW, tc.cellH)
			}
		})
	}

	if _, ok := TemplateByName("avery-nonexistent"); ok {
		t.Fatal("TemplateByName invented a template")
	}
}

func TestAveryL7160HasTwentyOneLabels(t *testing.T) {
	t.Parallel()

	// The headline number on the box. If the grid ever disagrees with it, the
	// template is describing different stock than its name claims.
	tpl, ok := TemplateByName("avery-l7160")
	if !ok {
		t.Fatal("avery-l7160 is missing")
	}
	if got := tpl.Layout.PerPage(); got != 21 {
		t.Fatalf("PerPage() = %d, want 21", got)
	}
}

func TestDimsFormatsAMillimetrePair(t *testing.T) {
	t.Parallel()

	if got := dims(1.5, 2); got != "1.50mm by 2.00mm" {
		t.Fatalf("dims = %q", got)
	}
}

// nearly compares millimetres with a tolerance well below what a printer can
// resolve, so the tests do not depend on float64 associativity.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
