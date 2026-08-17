package httpapi_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/httpapi"
)

// postCSV sends a raw CSV body to an endpoint.
func postCSV(t *testing.T, h http.Handler, target, csv string) *http.Response {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(csv))
	r.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

func TestBatchJSONOutput(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/batch", `{
		"items": [
			{"id": "one", "data": "https://example.com/1"},
			{"id": "two", "data": "https://example.com/2"},
			{"id": "wifi", "type": "wifi",
			 "payload": {"ssid": "Lobby", "password": "guest2026", "auth": "WPA"}}
		],
		"defaults": {"output.format": "svg", "output.scale": "4"},
		"output": "json"
	}`)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Results []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Data  string `json:"data"`
			Body  string `json:"body"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if got, want := len(out.Results), 3; got != want {
		t.Fatalf("results = %d, want %d", got, want)
	}
	// Results must come back in input order even though rendering is
	// concurrent; a caller lines them up against its own input by position.
	for i, want := range []string{"one", "two", "wifi"} {
		if out.Results[i].ID != want {
			t.Errorf("results[%d].id = %q, want %q", i, out.Results[i].ID, want)
		}
		if !out.Results[i].OK {
			t.Errorf("results[%d] failed: %s", i, out.Results[i].Error)
		}
	}

	// The built payload is echoed, so a caller can see what was encoded.
	if !strings.HasPrefix(out.Results[2].Data, "WIFI:") {
		t.Errorf("the wifi item encoded %q", out.Results[2].Data)
	}

	decoded, err := base64.StdEncoding.DecodeString(out.Results[0].Body)
	if err != nil {
		t.Fatalf("body is not base64: %v", err)
	}
	if !bytes.Contains(decoded, []byte("<svg")) {
		t.Error("the batch default output.format=svg was not applied")
	}
}

func TestBatchZipOutput(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/batch", `{
		"items": [{"id": "alpha", "data": "a"}, {"id": "beta", "data": "b"}],
		"output": "zip"
	}`)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/zip"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the response is not a valid zip: %v", err)
	}

	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
		// Zip-slip protection applies even to archives we write ourselves.
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Errorf("unsafe zip entry name %q", f.Name)
		}
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"alpha", "beta", "results.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("zip entries %v omit %q", names, want)
		}
	}
}

func TestBatchCSV(t *testing.T) {
	t.Parallel()

	csv := "id,data,style.module\n" +
		"first,https://example.com/1,square\n" +
		"second,https://example.com/2,square\n"

	resp := postCSV(t, serverWith(t, nil).Handler(),
		"/v1/batch?output=json&output.format=ascii", csv)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Results []struct {
			ID string `json:"id"`
			OK bool   `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got, want := len(out.Results), 2; got != want {
		t.Fatalf("results = %d, want %d", got, want)
	}
	for i, r := range out.Results {
		if !r.OK {
			t.Errorf("results[%d] failed", i)
		}
	}
}

// TestBatchPartialFailure is the property that makes a batch usable: one bad
// row must not cost the caller the other nine hundred.
func TestBatchPartialFailure(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/batch", `{
		"items": [
			{"id": "good", "data": "https://example.com"},
			{"id": "bad",  "data": "12345", "options": {"symbology": "ean13"}},
			{"id": "also-good", "data": "https://example.org"}
		],
		"output": "json"
	}`)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Results []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(out.Results))
	}
	if !out.Results[0].OK || !out.Results[2].OK {
		t.Error("a valid item failed alongside an invalid one")
	}
	if out.Results[1].OK {
		t.Error("a five-digit EAN-13 was accepted")
	}
	if out.Results[1].Error == "" {
		t.Error("the failed item carries no reason")
	}
}

func TestBatchRejections(t *testing.T) {
	t.Parallel()

	h := serverWith(t, map[string]string{"BARQR_MAX_BATCH_ITEMS": "2"}).Handler()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty batch", `{"items":[]}`, http.StatusBadRequest},
		{"no items and no csv", `{"output":"zip"}`, http.StatusBadRequest},
		{
			name:       "over the item cap",
			body:       `{"items":[{"data":"a"},{"data":"b"},{"data":"c"}]}`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "pdf points at the sheet endpoint",
			body:       `{"items":[{"data":"a"}],"output":"pdf"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown output",
			body:       `{"items":[{"data":"a"}],"output":"tarball"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodPost, "/v1/batch", tt.body)
			if got := resp.StatusCode; got != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", got, tt.wantStatus, readAll(t, resp))
			}
			f := faultOf(t, resp)
			if f.Code == "" || f.Message == "" {
				t.Errorf("incomplete fault: %+v", f)
			}
		})
	}

	t.Run("pdf suggests the sheet endpoint", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodPost, "/v1/batch", `{"items":[{"data":"a"}],"output":"pdf"}`)
		if got := faultOf(t, resp).Hint; !strings.Contains(got, "/v1/sheet") {
			t.Errorf("hint = %q, want it to point at /v1/sheet", got)
		}
	})
}

func TestPresetList(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet, "/v1/preset")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Presets []struct {
			Name    string         `json:"name"`
			Options map[string]any `json:"options"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Presets) == 0 {
		t.Fatal("no presets listed")
	}

	names := make(map[string]bool, len(out.Presets))
	for _, p := range out.Presets {
		names[p.Name] = true
		if len(p.Options) == 0 {
			t.Errorf("preset %q has no options", p.Name)
		}
	}
	for _, want := range []string{"default", "print", "terminal", "web"} {
		if !names[want] {
			t.Errorf("the built-in preset %q is missing", want)
		}
	}
}

func TestPresetRender(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	t.Run("the preset supplies the options", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodGet, "/v1/preset/terminal?data=hello")
		body := readAll(t, resp)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d: %s", got, want, body)
		}
		if got, want := resp.Header.Get("X-Barqr-Preset"), "terminal"; got != want {
			t.Errorf("X-Barqr-Preset = %q, want %q", got, want)
		}
		if !bytes.Contains(body, []byte("\x1b[")) {
			t.Error("the terminal preset did not produce ANSI output")
		}
	})

	t.Run("the request overrides the preset", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodGet, "/v1/preset/terminal?data=hello&output.format=svg")
		body := readAll(t, resp)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d: %s", got, want, body)
		}
		if !bytes.Contains(body, []byte("<svg")) {
			t.Error("the request's output.format did not override the preset")
		}
	})

	t.Run("an unknown preset suggests a real one", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodGet, "/v1/preset/termnal?data=hi")
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		f := faultOf(t, resp)
		if f.Code != httpapi.CodeNotFound {
			t.Errorf("code = %q, want %q", f.Code, httpapi.CodeNotFound)
		}
		if !strings.Contains(f.Hint, "terminal") {
			t.Errorf("hint = %q, want it to suggest \"terminal\"", f.Hint)
		}
	})
}

func TestDecodeRoundTripThroughHTTP(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()
	const payload = "https://example.com/round-trip"

	// Render a PNG through the public endpoint...
	rendered := do(t, h, http.MethodGet, "/v1/qr?data="+payload)
	png := readAll(t, rendered)
	if rendered.StatusCode != http.StatusOK {
		t.Fatalf("render failed: %s", png)
	}

	// ...then post it straight back and expect the payload out.
	r := httptest.NewRequest(http.MethodPost, "/v1/decode?parse=true", bytes.NewReader(png))
	r.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("decode status = %d, want %d: %s", got, want, rec.Body.String())
	}

	var out struct {
		Count   int `json:"count"`
		Results []struct {
			Symbology string `json:"symbology"`
			Data      string `json:"data"`
			Type      string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("count = %d, want 1", out.Count)
	}
	if got := out.Results[0].Data; got != payload {
		t.Errorf("decoded %q, want %q", got, payload)
	}
	if got, want := out.Results[0].Symbology, "qr"; got != want {
		t.Errorf("symbology = %q, want %q", got, want)
	}
	// parse=true should recognise a bare URL through the url builder.
	if out.Results[0].Type == "" {
		t.Error("parse=true returned no builder type for a plain URL")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store; a decode describes an upload", got)
	}
}

func TestDecodeRejections(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
	}{
		{"no image at all", "", "application/octet-stream", http.StatusBadRequest},
		{"random bytes", "definitely not an image", "application/octet-stream", http.StatusBadRequest},
		{"json without an image field", `{"try_harder":true}`, "application/json", http.StatusBadRequest},
		{"json with a bad data uri", `{"image":"not-a-data-uri"}`, "application/json", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/v1/decode", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}

	t.Run("a blank image finds nothing", func(t *testing.T) {
		t.Parallel()

		// A real all-white PNG: structurally valid, with no code in it.
		img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		for i := range img.Pix {
			img.Pix[i] = 0xFF
		}
		var blank bytes.Buffer
		if err := png.Encode(&blank, img); err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequest(http.MethodPost, "/v1/decode", bytes.NewReader(blank.Bytes()))
		r.Header.Set("Content-Type", "application/octet-stream")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Fatalf("status = %d, want %d: %s", got, want, rec.Body.String())
		}
	})
}
