package httpapi

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/el-amin-dev/barqr/internal/batch"
	"github.com/el-amin-dev/barqr/internal/sheet"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// sheetRequest lays a batch of codes out on printable label stock.
//
// It reuses batch's Item so a caller who already builds a batch can print it
// by changing the endpoint, not the payload.
type sheetRequest struct {
	// Items are the codes, one per label.
	Items []batch.Item `json:"items,omitempty"`
	// CSV is an alternative to Items, with the same columns /v1/batch accepts.
	CSV string `json:"csv,omitempty"`
	// Defaults are dot-notation options applied to every item.
	Defaults map[string]any `json:"defaults,omitempty"`

	// Template names a real label stock, e.g. "avery-l7160". When set, it
	// supplies the whole layout and the fields below are ignored.
	Template string `json:"template,omitempty"`
	// Layout describes custom stock, for a caller measuring their own sheets.
	Layout *sheetLayout `json:"layout,omitempty"`

	// Skip leaves the first N label positions blank, which is how a part-used
	// sheet gets reused.
	Skip int `json:"skip,omitempty"`
	// Caption draws each item's id beneath its code.
	Caption bool `json:"caption,omitempty"`
}

// sheetLayout is the wire form of a custom label grid, in millimetres.
type sheetLayout struct {
	Page         string  `json:"page,omitempty"`
	Cols         int     `json:"cols"`
	Rows         int     `json:"rows"`
	MarginTopMM  float64 `json:"margin_top_mm"`
	MarginLeftMM float64 `json:"margin_left_mm"`
	CellWidthMM  float64 `json:"cell_width_mm"`
	CellHeightMM float64 `json:"cell_height_mm"`
	GutterXMM    float64 `json:"gutter_x_mm,omitempty"`
	GutterYMM    float64 `json:"gutter_y_mm,omitempty"`
}

// handleSheet renders a grid of labels as a print-ready PDF.
//
// Codes are rasterised at a resolution derived from the cell size, so a label
// that is 63.5 mm wide gets a code sized for 63.5 mm rather than a screen-sized
// PNG stretched to fit. That is the difference between a sheet that scans and
// one that does not.
func (s *Server) handleSheet(w http.ResponseWriter, r *http.Request) {
	var req sheetRequest
	if err := decodeJSONBody(r.Body, &req); err != nil {
		s.fail(w, r, err)
		return
	}

	layout, err := s.sheetLayout(req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	items, err := s.sheetItems(req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(items) == 0 {
		f := newFault(http.StatusBadRequest, CodeMissingData, "no labels to print")
		f.Hint = "set `items` to a list, or `csv` to a document with a header row"
		s.fail(w, r, f)
		return
	}
	if req.Skip < 0 {
		f := newFault(http.StatusBadRequest, CodeInvalidValue, "skip cannot be negative")
		f.Field = "skip"
		s.fail(w, r, f)
		return
	}
	if total := len(items) + req.Skip; total > s.cfg.MaxBatchItems {
		f := newFault(http.StatusRequestEntityTooLarge, CodeBadRequest,
			"%d labels exceeds the limit of %d", total, s.cfg.MaxBatchItems)
		f.Hint = "split the sheet, or raise BARQR_MAX_BATCH_ITEMS"
		s.fail(w, r, f)
		return
	}

	// Render through the batch runner so one bad row does not cost the whole
	// sheet, and so a label uses exactly the pipeline /v1/qr uses.
	out, err := batch.Run(r.Context(), batch.Request{
		Items:    items,
		Defaults: sheetDefaults(req.Defaults, layout),
		Output:   batch.OutputJSON,
	}, s.renderBatchItem, batch.RunOptions{
		MaxItems:    s.cfg.MaxBatchItems,
		Concurrency: s.cfg.Concurrency,
	})
	if err != nil {
		s.fail(w, r, batchFault(err, s.cfg.MaxBatchItems))
		return
	}

	cells := make([]sheet.Cell, 0, req.Skip+len(out.Results))
	for range req.Skip {
		cells = append(cells, sheet.Cell{}) // a blank position on a part-used sheet
	}
	failed := 0
	for i, res := range out.Results {
		if !res.OK {
			failed++
			cells = append(cells, sheet.Cell{}) // leave the position blank
			continue
		}
		cell := sheet.Cell{PNG: decodeBase64(res.Body)}
		if req.Caption {
			cell.Caption = res.ID
			if cell.Caption == "" {
				cell.Caption = strconv.Itoa(i + 1)
			}
		}
		cells = append(cells, cell)
	}

	pdf, err := sheet.Compose(cells, layout)
	if err != nil {
		s.fail(w, r, sheetFault(err))
		return
	}

	if s.metrics != nil {
		s.metrics.observeRender("sheet", "pdf", len(pdf))
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	w.Header().Set("Content-Disposition", contentDisposition("labels.pdf", true))
	setRenderSecurityHeaders(w)
	w.Header().Set("X-Sheet-Labels", strconv.Itoa(len(cells)))
	if failed > 0 {
		// The sheet is still printable; the caller needs to know some
		// positions came out blank rather than discovering it on paper.
		w.Header().Set("X-Sheet-Failed", strconv.Itoa(failed))
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(pdf); err != nil {
		s.log.Debug("writing sheet output failed", "error", err.Error())
	}
}

// sheetLayout resolves a named template or a custom grid.
func (s *Server) sheetLayout(req sheetRequest) (sheet.Layout, error) {
	switch {
	case req.Template != "":
		t, ok := sheet.TemplateByName(req.Template)
		if !ok {
			f := newFault(http.StatusNotFound, CodeNotFound,
				"no label template named %q", req.Template)
			f.Field = "template"
			if best, found := closest(req.Template, templateNames()); found {
				f.Hint = "did you mean \"" + best + "\"?"
			}
			return sheet.Layout{}, f
		}
		t.Layout.LabelCaption = req.Caption
		return t.Layout, nil

	case req.Layout != nil:
		l := sheet.Layout{
			Cols:         req.Layout.Cols,
			Rows:         req.Layout.Rows,
			MarginTopMM:  req.Layout.MarginTopMM,
			MarginLeftMM: req.Layout.MarginLeftMM,
			CellWidthMM:  req.Layout.CellWidthMM,
			CellHeightMM: req.Layout.CellHeightMM,
			GutterXMM:    req.Layout.GutterXMM,
			GutterYMM:    req.Layout.GutterYMM,
			LabelCaption: req.Caption,
		}

		name := req.Layout.Page
		if name == "" {
			name = "a4"
		}
		page, ok := sheet.PageByName(name)
		if !ok {
			f := newFault(http.StatusBadRequest, CodeInvalidValue,
				"unknown page size %q", req.Layout.Page)
			f.Field = "layout.page"
			f.Expected = joinNames(pageNames())
			return sheet.Layout{}, f
		}
		l.Page = page

		if err := l.Validate(); err != nil {
			f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
			f.Field = "layout"
			return sheet.Layout{}, f
		}
		return l, nil

	default:
		f := newFault(http.StatusBadRequest, CodeMissingData,
			"set either `template` or `layout`")
		f.Field = "template"
		f.Hint = "available templates: " + joinNames(templateNames())
		return sheet.Layout{}, f
	}
}

// sheetItems resolves the items from the JSON list or the CSV document.
func (s *Server) sheetItems(req sheetRequest) ([]batch.Item, error) {
	if req.CSV != "" && len(req.Items) > 0 {
		f := newFault(http.StatusBadRequest, CodeBadRequest,
			"set either `items` or `csv`, not both")
		f.Field = "csv"
		return nil, f
	}
	if req.CSV == "" {
		return req.Items, nil
	}

	items, err := batch.ParseCSV(req.CSV)
	if err != nil {
		f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
		f.Field = "csv"
		return nil, f
	}
	return items, nil
}

// sheetDefaults forces the output options a printed label needs, on top of
// whatever the caller asked for.
//
// The format must be PNG because that is what sheet.Compose embeds, and the
// size is derived from the cell so the code is rasterised at print resolution
// rather than scaled up from a screen-sized image.
func sheetDefaults(caller map[string]any, l sheet.Layout) map[string]any {
	out := make(map[string]any, len(caller)+4)
	for k, v := range caller {
		out[k] = v
	}

	out["output.format"] = writer.PNG
	out["output.unit"] = string(writer.UnitMM)
	out["output.dpi"] = sheetDPI

	// Leave a margin inside the cell so a code never touches the die-cut edge,
	// and reserve room for the caption when one is drawn.
	usable := min(l.CellWidthMM, l.CellHeightMM) * 0.86
	if l.LabelCaption {
		usable = min(l.CellWidthMM*0.86, l.CellHeightMM*0.70)
	}
	out["output.size"] = usable

	return out
}

// sheetDPI is the resolution codes are rasterised at for print. 300 is the
// common office-printer figure and the point at which a QR module stops being
// visibly stepped.
const sheetDPI = 300

// decodeBase64 decodes a batch JSON body, returning nil on anything malformed
// so a bad cell leaves a blank label instead of failing the whole sheet.
func decodeBase64(s string) []byte {
	if s == "" {
		return nil
	}
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return out
}

// joinNames renders a name list for an error message.
func joinNames(names []string) string { return strings.Join(names, ", ") }

// templateNames lists the label stocks this build knows.
func templateNames() []string {
	all := sheet.Templates()
	out := make([]string, 0, len(all))
	for _, t := range all {
		out = append(out, t.Name)
	}
	return out
}

// pageNames lists the page sizes this build knows.
func pageNames() []string {
	all := sheet.Pages()
	out := make([]string, 0, len(all))
	for _, p := range all {
		out = append(out, p.Name)
	}
	return out
}

// sheetFault maps a composition failure onto the wire shape.
//
// Everything sheet.Compose can fail on is a property of the request — a layout
// that does not fit the page, a cell image it cannot embed — so it is a 400
// rather than a server fault.
func sheetFault(err error) error {
	f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
	f.Field = "layout"
	return f
}

// handleSheetTemplates lists the label stocks and page sizes.
func (s *Server) handleSheetTemplates(w http.ResponseWriter, r *http.Request) {
	templates := make([]map[string]any, 0, len(sheet.Templates()))
	for _, t := range sheet.Templates() {
		templates = append(templates, map[string]any{
			"name":           t.Name,
			"title":          t.Title,
			"page":           t.Layout.Page.Name,
			"cols":           t.Layout.Cols,
			"rows":           t.Layout.Rows,
			"per_page":       t.Layout.PerPage(),
			"cell_width_mm":  t.Layout.CellWidthMM,
			"cell_height_mm": t.Layout.CellHeightMM,
		})
	}

	pages := make([]map[string]any, 0, len(sheet.Pages()))
	for _, p := range sheet.Pages() {
		pages = append(pages, map[string]any{
			"name": p.Name, "width_mm": p.WidthMM, "height_mm": p.HeightMM,
		})
	}

	s.cacheable(w)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"templates": templates,
		"pages":     pages,
	})
}
