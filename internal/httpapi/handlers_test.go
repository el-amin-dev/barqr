package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/httpapi"
)

const testKey = "test-key"

// serverWith builds a Server from environment overrides, with logging
// discarded. Auth defaults to open so most tests can ignore headers.
func serverWith(t *testing.T, env map[string]string) *httpapi.Server {
	t.Helper()

	full := map[string]string{"BARQR_AUTH_MODE": "open"}
	for k, v := range env {
		full[k] = v
	}

	pairs := make([]string, 0, len(full))
	for k, v := range full {
		pairs = append(pairs, k+"="+v)
	}

	cfg, _, err := config.Load(pairs)
	if err != nil {
		t.Fatalf("config.Load() returned error: %v", err)
	}
	return httpapi.New(cfg, slog.New(slog.DiscardHandler))
}

// do performs an in-process request against the handler.
func do(t *testing.T, h http.Handler, method, target string, body ...string) *http.Response {
	t.Helper()

	var r *http.Request
	if len(body) > 0 {
		r = httptest.NewRequest(method, target, strings.NewReader(body[0]))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

// readAll drains and closes a response body.
func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return buf.Bytes()
}

// faultOf decodes an error response.
func faultOf(t *testing.T, resp *http.Response) httpapi.Fault {
	t.Helper()

	var env struct {
		Error httpapi.Fault `json:"error"`
	}
	if err := json.Unmarshal(readAll(t, resp), &env); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	return env.Error
}

func TestQRRendersPNG(t *testing.T) {
	t.Parallel()

	// HELLO fits a version-1 symbol; a longer payload would silently promote
	// the version and make the pixel arithmetic below wrong rather than failing.
	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet, "/v1/qr?data=HELLO")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
	if got, want := resp.Header.Get("Content-Type"), "image/png"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("body does not start with the PNG magic bytes")
	}

	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decoding the rendered PNG: %v", err)
	}
	// A version-1 symbol is 21 modules plus a four-module quiet zone on each
	// side, at the default ten pixels per module.
	if got, want := img.Bounds().Dx(), (21+8)*10; got != want {
		t.Errorf("width = %d, want %d", got, want)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("no ETag header on a deterministic response")
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "qr.png") {
		t.Errorf("Content-Disposition = %q, want it to name qr.png", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id header")
	}
}

func TestQRFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format   string
		mime     string
		contains string
	}{
		{"svg", "image/svg+xml", "<svg"},
		{"ascii", "text/plain; charset=utf-8", "##"},
		{"ansi", "text/plain; charset=utf-8", "\x1b["},
		{"json", "application/json", `"modules"`},
		{"datauri", "text/plain; charset=utf-8", "data:image/png;base64,"},
		{"pdf", "application/pdf", "%PDF-"},
		{"eps", "application/postscript", "%!PS-Adobe"},
	}

	h := serverWith(t, nil).Handler()
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodGet, "/v1/qr?data=hello&output.format="+tt.format)
			body := readAll(t, resp)

			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("status = %d, want %d: %s", got, want, body)
			}
			if got := resp.Header.Get("Content-Type"); got != tt.mime {
				t.Errorf("Content-Type = %q, want %q", got, tt.mime)
			}
			if !bytes.Contains(body, []byte(tt.contains)) {
				t.Errorf("body does not contain %q", tt.contains)
			}
		})
	}
}

// TestQRIsDeterministic underpins the caching story: the same query must
// always produce byte-identical output, or the ETag is a lie.
func TestQRIsDeterministic(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()
	const target = "/v1/qr?data=determinism&output.format=svg"

	first := do(t, h, http.MethodGet, target)
	firstBody := readAll(t, first)
	second := do(t, h, http.MethodGet, target)
	secondBody := readAll(t, second)

	if !bytes.Equal(firstBody, secondBody) {
		t.Fatal("two identical requests produced different bytes")
	}
	if a, b := first.Header.Get("ETag"), second.Header.Get("ETag"); a != b {
		t.Errorf("ETag differs between identical requests: %q vs %q", a, b)
	}
}

func TestQRETagRevalidation(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()
	const target = "/v1/qr?data=etag"

	first := do(t, h, http.MethodGet, target)
	_ = readAll(t, first)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	for _, header := range []string{etag, "W/" + etag, "*", `"other", ` + etag} {
		t.Run(header, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("If-None-Match", header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if got, want := rec.Code, http.StatusNotModified; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if rec.Body.Len() != 0 {
				t.Error("a 304 must not carry a body")
			}
		})
	}

	// A stale validator must serve the real response.
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("If-None-Match", `"stale"`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status for a stale ETag = %d, want %d", got, want)
	}
}

func TestQRPostJSON(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodPost, "/v1/qr",
		`{"type":"wifi","payload":{"ssid":"Lobby","password":"guest2026","auth":"WPA"},
		  "output":{"format":"svg"}}`)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
	if !bytes.Contains(body, []byte("<svg")) {
		t.Error("body is not SVG")
	}
	// The Wi-Fi password must never appear in the rendered SVG's text nodes;
	// it lives in the modules, not the markup.
	if bytes.Contains(body, []byte("guest2026")) {
		t.Error("the SVG leaked the payload as text")
	}
}

func TestErrorShape(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name       string
		target     string
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name:       "nothing to encode",
			target:     "/v1/qr",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeMissingData,
			wantField:  "data",
		},
		{
			name:       "unknown output format",
			target:     "/v1/qr?data=hi&output.format=bmp",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeUnknownFormat,
			wantField:  "output.format",
		},
		{
			name:       "unknown module shape",
			target:     "/v1/qr?data=hi&style.module=hexagon",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeUnknownShape,
		},
		{
			name:       "invalid colour",
			target:     "/v1/qr?data=hi&style.fg=notacolour",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeInvalidColor,
		},
		{
			name:       "unknown symbology",
			target:     "/v1/barcode/notreal?data=hi",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeUnknownSymbology,
			wantField:  "symbology",
		},
		{
			name:       "payload beyond capacity",
			target:     "/v1/qr?data=" + strings.Repeat("x", 3000),
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeDataTooLong,
		},
		{
			name:       "canvas cap",
			target:     "/v1/qr?data=hi&output.scale=100000",
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   httpapi.CodeCanvasTooLarge,
		},
		{
			name:       "unknown endpoint",
			target:     "/v1/nope",
			wantStatus: http.StatusNotFound,
			wantCode:   httpapi.CodeNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodGet, tt.target)
			if got := resp.StatusCode; got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
			if got, want := resp.Header.Get("Content-Type"),
				"application/json; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
			if got, want := resp.Header.Get("Cache-Control"), "no-store"; got != want {
				t.Errorf("Cache-Control = %q, want %q; errors must never be cached", got, want)
			}

			f := faultOf(t, resp)
			if f.Code != tt.wantCode {
				t.Errorf("code = %q, want %q (message: %s)", f.Code, tt.wantCode, f.Message)
			}
			if tt.wantField != "" && f.Field != tt.wantField {
				t.Errorf("field = %q, want %q", f.Field, tt.wantField)
			}
			if f.Message == "" {
				t.Error("message is empty")
			}
			if f.RequestID == "" {
				t.Error("request_id is empty; the log line cannot be correlated")
			}
		})
	}
}

// TestErrorsNeverLeakInternals is the disclosure guard: an error message must
// never hand a caller a file path, a Go type name, or a stack frame.
func TestErrorsNeverLeakInternals(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()
	forbidden := []string{
		"/home/", "/src/", ".go:", "goroutine ",
		"github.com/el-amin-dev", "runtime.",
		"encoder.", "render.", "writer.", "httpapi.",
	}
	// A pixel dimension such as 2900000x2900000 contains a literal "0x2900000",
	// so a pointer is only a pointer when the "0x" starts a token.
	pointer := regexp.MustCompile(`(^|[^0-9A-Za-z])0x[0-9a-f]{6,}`)

	for _, target := range []string{
		"/v1/qr",
		"/v1/qr?data=hi&output.format=bmp",
		"/v1/qr?data=hi&style.fg=zzz",
		"/v1/qr?data=hi&output.scale=100000",
		"/v1/barcode/notreal?data=hi",
		"/v1/build/notreal?payload.x=1",
		"/v1/nope",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			body := string(readAll(t, do(t, h, http.MethodGet, target)))
			for _, bad := range forbidden {
				if strings.Contains(body, bad) {
					t.Errorf("response leaks %q: %s", bad, body)
				}
			}
			if pointer.MatchString(body) {
				t.Errorf("response leaks a pointer: %s", body)
			}
		})
	}
}

func TestAuth(t *testing.T) {
	t.Parallel()

	h := serverWith(t, map[string]string{
		"BARQR_AUTH_MODE": "required",
		"BARQR_API_KEYS":  testKey + ",second-key",
	}).Handler()

	t.Run("operational endpoints are open", func(t *testing.T) {
		for _, path := range []string{"/v1/healthz", "/v1/readyz", "/v1/version"} {
			resp := do(t, h, http.MethodGet, path)
			_ = readAll(t, resp)
			// readyz is 503 before Serve runs; the point is that it is not 401.
			if resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("%s requires a key; a probe cannot hold one", path)
			}
		}
	})

	t.Run("rendering requires a key", func(t *testing.T) {
		resp := do(t, h, http.MethodGet, "/v1/qr?data=hi")
		if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		f := faultOf(t, resp)
		if f.Code != httpapi.CodeUnauthorized {
			t.Errorf("code = %q, want %q", f.Code, httpapi.CodeUnauthorized)
		}
		if f.Hint == "" {
			t.Error("no hint telling the caller how to authenticate")
		}
	})

	for _, tc := range []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"x-api-key", "X-API-Key", testKey, http.StatusOK},
		{"bearer", "Authorization", "Bearer " + testKey, http.StatusOK},
		{"second key", "X-API-Key", "second-key", http.StatusOK},
		{"wrong key", "X-API-Key", "nope", http.StatusUnauthorized},
		{"basic auth is not accepted", "Authorization", "Basic " + testKey, http.StatusUnauthorized},
		{"empty key", "X-API-Key", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/qr?data=hi", nil)
			r.Header.Set(tc.header, tc.value)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	t.Run("responses are private when auth is on", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/qr?data=hi", nil)
		r.Header.Set("X-API-Key", testKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "private") {
			t.Errorf("Cache-Control = %q, want it to be private; a shared cache "+
				"must not serve one caller's code to another", got)
		}
	})
}

func TestRateLimit(t *testing.T) {
	t.Parallel()

	h := serverWith(t, map[string]string{"BARQR_RATE_LIMIT": "3/min"}).Handler()

	var limited bool
	for i := range 6 {
		resp := do(t, h, http.MethodGet, "/v1/qr?data=hi&output.format=ascii")

		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			if resp.Header.Get("Retry-After") == "" {
				t.Error("no Retry-After on a 429")
			}
			if f := faultOf(t, resp); f.Code != httpapi.CodeRateLimited {
				t.Errorf("code = %q, want %q", f.Code, httpapi.CodeRateLimited)
			}
			break
		}
		_ = readAll(t, resp)
		_ = i
	}
	if !limited {
		t.Error("six requests against a 3/min limit were all allowed")
	}
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()

	h := serverWith(t, map[string]string{"BARQR_MAX_BODY": "512"}).Handler()

	body := `{"data":"` + strings.Repeat("x", 4096) + `"}`
	resp := do(t, h, http.MethodPost, "/v1/qr", body)
	_ = readAll(t, resp)

	if resp.StatusCode == http.StatusOK {
		t.Fatal("an oversized body was accepted")
	}
}

func TestSymbologies(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet, "/v1/symbologies")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var doc struct {
		Symbologies []struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		} `json:"symbologies"`
		Formats []struct {
			Name string `json:"name"`
			MIME string `json:"mime"`
		} `json:"formats"`
		Builders []struct {
			Name string `json:"name"`
		} `json:"builders"`
		Styles map[string][]string `json:"styles"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	var sawQR, sawUnavailable bool
	for _, s := range doc.Symbologies {
		if s.Name == "qr" && s.Available {
			sawQR = true
		}
		if !s.Available {
			sawUnavailable = true
			if s.Reason == "" {
				t.Errorf("%s is unavailable with no reason", s.Name)
			}
		}
	}
	if !sawQR {
		t.Error("qr is not listed as available")
	}
	if !sawUnavailable {
		t.Error("no unavailable symbology listed; the capability matrix should be honest " +
			"about what this build cannot do")
	}
	if len(doc.Formats) == 0 || len(doc.Builders) == 0 {
		t.Error("formats or builders are empty")
	}
	if len(doc.Styles["module"]) == 0 {
		t.Error("no module shapes listed")
	}
}

func TestOpenAPI(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet, "/v1/openapi.json")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}

	paths, _ := doc["paths"].(map[string]any)
	for _, want := range []string{"/v1/qr", "/v1/barcode/{symbology}", "/v1/validate",
		"/v1/decode", "/v1/symbologies"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("the document omits %s", want)
		}
	}
}

func TestBuildEndpoint(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodGet,
		"/v1/build/wifi?payload.ssid=Lobby&payload.password=guest2026&payload.auth=WPA")
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}

	var out struct {
		Type   string `json:"type"`
		Data   string `json:"data"`
		Length int    `json:"length"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Type != "wifi" {
		t.Errorf("type = %q, want wifi", out.Type)
	}
	if !strings.HasPrefix(out.Data, "WIFI:") {
		t.Errorf("data = %q, want a WIFI: string", out.Data)
	}
	if out.Length != len(out.Data) {
		t.Errorf("length = %d, want %d", out.Length, len(out.Data))
	}
	// A build response is request-specific and must not be cached publicly.
	if got := resp.Header.Get("Cache-Control"); got == "" {
		t.Error("no Cache-Control on the build response")
	}
}

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	t.Run("a clean design passes", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodPost, "/v1/validate", `{"data":"https://example.com"}`)
		body := readAll(t, resp)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d: %s", got, want, body)
		}

		var out struct {
			Valid        bool `json:"valid"`
			Scannability struct {
				Score int    `json:"score"`
				Grade string `json:"grade"`
			} `json:"scannability"`
			Matrix struct {
				Cols      int `json:"cols"`
				QuietZone int `json:"quiet_zone"`
			} `json:"matrix"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if !out.Valid {
			t.Error("valid = false for a plain code")
		}
		if out.Scannability.Grade != "excellent" {
			t.Errorf("grade = %q, want excellent", out.Scannability.Grade)
		}
		if out.Matrix.Cols == 0 || out.Matrix.QuietZone != 4 {
			t.Errorf("matrix = %+v, want a real grid with the QR quiet zone", out.Matrix)
		}
	})

	t.Run("a low-contrast design is flagged", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodPost, "/v1/validate",
			`{"data":"hi","style":{"fg":"#eeeeee","bg":"#ffffff"}}`)
		body := readAll(t, resp)

		if !bytes.Contains(body, []byte("LOW_CONTRAST")) {
			t.Errorf("a near-invisible code was not flagged: %s", body)
		}
		if !bytes.Contains(body, []byte("unscannable")) {
			t.Errorf("grade is not unscannable: %s", body)
		}
	})
}

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	// Generate some traffic so the counters are non-zero.
	_ = readAll(t, do(t, h, http.MethodGet, "/v1/qr?data=metrics&output.format=ascii"))

	resp := do(t, h, http.MethodGet, "/metrics")
	body := string(readAll(t, resp))

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	for _, want := range []string{
		"barqr_http_requests_total",
		"barqr_http_request_duration_seconds",
		"barqr_renders_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output omits %s", want)
		}
	}
	// The route label must be the chi pattern, not the raw path, or every
	// distinct query creates a new time series.
	if strings.Contains(body, `route="/v1/qr?data=metrics`) {
		t.Error("the route label contains the raw path; label cardinality will explode")
	}
}

func TestMetricsDisabled(t *testing.T) {
	t.Parallel()

	h := serverWith(t, map[string]string{"BARQR_METRICS": "false"}).Handler()

	resp := do(t, h, http.MethodGet, "/metrics")
	_ = readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("status = %d, want %d when metrics are disabled", got, want)
	}
}

func TestCORS(t *testing.T) {
	t.Parallel()

	t.Run("no headers without configured origins", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
		r.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		serverWith(t, nil).Handler().ServeHTTP(rec, r)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want none by default", got)
		}
	})

	t.Run("configured origin is echoed", func(t *testing.T) {
		t.Parallel()

		h := serverWith(t, map[string]string{
			"BARQR_CORS_ORIGINS": "https://app.example",
		}).Handler()

		r := httptest.NewRequest(http.MethodOptions, "/v1/qr", nil)
		r.Header.Set("Origin", "https://app.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got, want := rec.Header().Get("Access-Control-Allow-Origin"),
			"https://app.example"; got != want {
			t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, want)
		}
		if got, want := rec.Code, http.StatusNoContent; got != want {
			t.Errorf("preflight status = %d, want %d", got, want)
		}

		// An unlisted origin gets nothing.
		r2 := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
		r2.Header.Set("Origin", "https://evil.example")
		rec2 := httptest.NewRecorder()
		h.ServeHTTP(rec2, r2)
		if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("an unlisted origin was allowed: %q", got)
		}
	})
}

func TestRequestIDIsAdoptedAndSanitised(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"upstream id is kept", "abc-123_XYZ.4", "abc-123_XYZ.4"},
		{"injection characters are stripped", "a\nb\rc d", "abcd"},
		{"quotes and escapes are stripped", `a"b'c\d`, "abcd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
			r.Header.Set("X-Request-Id", tt.header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if got := rec.Header().Get("X-Request-Id"); got != tt.want {
				t.Errorf("X-Request-Id = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("an overlong id is truncated", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
		r.Header.Set("X-Request-Id", strings.Repeat("a", 500))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got := len(rec.Header().Get("X-Request-Id")); got > 64 {
			t.Errorf("id length = %d, want it capped at 64", got)
		}
	})

	t.Run("an id is generated when absent", func(t *testing.T) {
		t.Parallel()

		first := do(t, h, http.MethodGet, "/v1/healthz").Header.Get("X-Request-Id")
		second := do(t, h, http.MethodGet, "/v1/healthz").Header.Get("X-Request-Id")

		if first == "" || second == "" {
			t.Fatal("no generated request id")
		}
		if first == second {
			t.Error("two requests were given the same generated id")
		}
	})
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()

	resp := do(t, serverWith(t, nil).Handler(), http.MethodDelete, "/v1/qr")
	if got, want := resp.StatusCode, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if f := faultOf(t, resp); f.Code != httpapi.CodeMethodNotAllowed {
		t.Errorf("code = %q, want %q", f.Code, httpapi.CodeMethodNotAllowed)
	}
}

func TestFilenameFromMeta(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"default name", "/v1/qr?data=hi", "qr.png"},
		{"explicit name", "/v1/qr?data=hi&meta.filename=badge", "badge.png"},
		{"extension is forced to match", "/v1/qr?data=hi&meta.filename=badge.jpg", "badge.jpg.png"},
		{"a path is stripped", "/v1/qr?data=hi&meta.filename=../../etc/passwd", "passwd.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodGet, tt.target)
			_ = readAll(t, resp)

			got := resp.Header.Get("Content-Disposition")
			if !strings.Contains(got, `filename="`+tt.want+`"`) {
				t.Errorf("Content-Disposition = %q, want it to name %q", got, tt.want)
			}
		})
	}

	t.Run("attachment switches the disposition", func(t *testing.T) {
		t.Parallel()

		resp := do(t, h, http.MethodGet, "/v1/qr?data=hi&meta.attachment=true")
		_ = readAll(t, resp)

		if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
			t.Errorf("Content-Disposition = %q, want an attachment", got)
		}
	})
}

func TestServeLifecycle(t *testing.T) {
	t.Parallel()

	srv := serverWith(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ln, err := srv.Listen(ctx)
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/v1/readyz") //nolint:gosec,noctx // fixed loopback URL in a test
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A real end-to-end render over a real socket.
	resp, err := http.Get(base + "/v1/qr?data=lifecycle") //nolint:gosec,noctx // fixed loopback URL
	if err != nil {
		t.Fatalf("GET /v1/qr: %v", err)
	}
	body := readAll(t, resp)
	if !bytes.HasPrefix(body, []byte("\x89PNG")) {
		t.Error("the served response is not a PNG")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return after cancellation")
	}
}

func BenchmarkQREndpoint(b *testing.B) {
	cfg, _, err := config.Load([]string{"BARQR_AUTH_MODE=open", "BARQR_METRICS=false"})
	if err != nil {
		b.Fatal(err)
	}
	h := httpapi.New(cfg, slog.New(slog.DiscardHandler)).Handler()
	r := httptest.NewRequest(http.MethodGet, "/v1/qr?data=https://example.com/benchmark", nil)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
