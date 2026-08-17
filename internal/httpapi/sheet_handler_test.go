package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSheetTemplates(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet, "/v1/sheet/templates")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Templates []struct {
			Name         string  `json:"name"`
			Title        string  `json:"title"`
			PerPage      int     `json:"per_page"`
			CellWidthMM  float64 `json:"cell_width_mm"`
			CellHeightMM float64 `json:"cell_height_mm"`
		} `json:"templates"`
		Pages []struct {
			Name    string  `json:"name"`
			WidthMM float64 `json:"width_mm"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(out.Templates) < 6 {
		t.Errorf("templates = %d, want at least six real label stocks", len(out.Templates))
	}
	for _, tpl := range out.Templates {
		if tpl.Name == "" || tpl.Title == "" {
			t.Errorf("template %+v is missing a name or title", tpl)
		}
		if tpl.PerPage <= 0 || tpl.CellWidthMM <= 0 || tpl.CellHeightMM <= 0 {
			t.Errorf("template %q has nonsense dimensions: %+v", tpl.Name, tpl)
		}
	}
	if len(out.Pages) == 0 {
		t.Error("no page sizes listed")
	}
}

func TestSheetRendersPDF(t *testing.T) {
	t.Parallel()

	// Enough labels to spill onto a second page of a 3x7 sheet.
	items := make([]string, 0, 25)
	for i := range 25 {
		items = append(items, `{"id":"SKU-`+string(rune('A'+i%26))+
			`","data":"https://example.com/`+string(rune('a'+i%26))+`"}`)
	}
	body := `{"template":"avery-l7160","caption":true,"skip":2,"items":[` +
		strings.Join(items, ",") + `]}`

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/sheet", body)
	pdf := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, pdf)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/pdf"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatal("the response is not a PDF")
	}
	if !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Error("the PDF has no EOF marker")
	}
	if got := resp.Header.Get("X-Sheet-Labels"); got != "27" {
		t.Errorf("X-Sheet-Labels = %q, want 27 (25 codes plus 2 skipped)", got)
	}
	if got := resp.Header.Get("X-Sheet-Failed"); got != "" {
		t.Errorf("X-Sheet-Failed = %q, want none", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "labels.pdf") {
		t.Errorf("Content-Disposition = %q, want labels.pdf", got)
	}
}

func TestSheetCustomLayout(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/sheet", `{
		"layout": {"page":"a4","cols":2,"rows":2,
		           "margin_top_mm":10,"margin_left_mm":10,
		           "cell_width_mm":80,"cell_height_mm":100},
		"items": [{"data":"one"},{"data":"two"}]
	}`)
	pdf := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, pdf)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("the response is not a PDF")
	}
}

func TestSheetFromCSV(t *testing.T) {
	t.Parallel()

	csv := "id,data\\nA,https://example.com/a\\nB,https://example.com/b\\n"
	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/sheet",
		`{"template":"avery-l7160","csv":"`+csv+`"}`)
	pdf := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, pdf)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("the response is not a PDF")
	}
}

// TestSheetPartialFailure asserts a bad row leaves a blank label rather than
// costing the whole sheet, and that the caller is told before they print it.
func TestSheetPartialFailure(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/sheet", `{
		"template": "avery-l7160",
		"items": [
			{"id":"ok","data":"https://example.com"},
			{"id":"bad","data":"12345","options":{"symbology":"ean13"}}
		]
	}`)
	pdf := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, pdf)
	}
	if got, want := resp.Header.Get("X-Sheet-Failed"), "1"; got != want {
		t.Errorf("X-Sheet-Failed = %q, want %q", got, want)
	}
}

func TestSheetRejections(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantHint   string
	}{
		{
			name:       "neither template nor layout",
			body:       `{"items":[{"data":"a"}]}`,
			wantStatus: http.StatusBadRequest,
			wantHint:   "avery",
		},
		{
			name:       "unknown template suggests a real one",
			body:       `{"template":"avery-l7161","items":[{"data":"a"}]}`,
			wantStatus: http.StatusNotFound,
			wantHint:   "avery-l7160",
		},
		{
			name:       "no items",
			body:       `{"template":"avery-l7160","items":[]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "items and csv together",
			body:       `{"template":"avery-l7160","items":[{"data":"a"}],"csv":"data\na"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown page size",
			body:       `{"layout":{"page":"a9","cols":1,"rows":1,"cell_width_mm":10,"cell_height_mm":10},"items":[{"data":"a"}]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "a layout wider than its page",
			body: `{"layout":{"page":"a4","cols":10,"rows":1,` +
				`"cell_width_mm":100,"cell_height_mm":20},"items":[{"data":"a"}]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative skip",
			body:       `{"template":"avery-l7160","skip":-1,"items":[{"data":"a"}]}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodPost, "/v1/sheet", tt.body)
			if got := resp.StatusCode; got != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", got, tt.wantStatus, readAll(t, resp))
			}

			f := faultOf(t, resp)
			if f.Code == "" || f.Message == "" {
				t.Errorf("incomplete fault: %+v", f)
			}
			if tt.wantHint != "" && !strings.Contains(f.Hint, tt.wantHint) {
				t.Errorf("hint = %q, want it to mention %q", f.Hint, tt.wantHint)
			}
		})
	}
}
