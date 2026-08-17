package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/httpapi"
)

// docsHandler builds a handler with the documentation UI enabled. Auth is
// required by default because that is the only mode in which the cookie, the
// unlock form and the JSON/HTML split are reachable at all.
func docsHandler(t *testing.T, env map[string]string) http.Handler {
	t.Helper()

	full := map[string]string{
		"BARQR_AUTH_MODE": "required",
		"BARQR_API_KEYS":  testKey + ",second-key",
		"BARQR_DOCS":      "true",
	}
	for k, v := range env {
		full[k] = v
	}
	return serverWith(t, full).Handler()
}

// docsCall is one in-process request described the way a browser would make
// it. The zero value is a plain GET, which keeps the tables below readable.
type docsCall struct {
	method   string
	target   string
	accept   string
	apiKey   string // X-API-Key header
	bearer   string // Authorization: Bearer …
	cookie   string // the barqr_key cookie
	form     string // urlencoded body, implies POST semantics
	encoding string // Accept-Encoding, absent when empty
}

// send performs the call. Absent Accept-Encoding matters: httptest never adds
// one, so the asset handler's decompressing fallback is genuinely exercised.
func (c docsCall) send(t *testing.T, h http.Handler) *http.Response {
	t.Helper()

	method := c.method
	if method == "" {
		method = http.MethodGet
	}

	var r *http.Request
	if c.form != "" {
		r = httptest.NewRequest(method, c.target, strings.NewReader(c.form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, c.target, nil)
	}

	if c.accept != "" {
		r.Header.Set("Accept", c.accept)
	}
	if c.apiKey != "" {
		r.Header.Set("X-API-Key", c.apiKey)
	}
	if c.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: "barqr_key", Value: c.cookie})
	}
	if c.encoding != "" {
		r.Header.Set("Accept-Encoding", c.encoding)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

// cookieNamed returns the named cookie from a response, or nil.
func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// TestLandingPageIsPublic pins the one page that must render on an instance
// whose keys the visitor does not have: if the front door needed a key, an
// operator staring at a bare 401 would have nothing to read.
func TestLandingPageIsPublic(t *testing.T) {
	t.Parallel()

	resp := docsCall{target: "/", accept: browser}.send(t, docsHandler(t, nil))
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
	if got, want := resp.Header.Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	for _, want := range []string{"barqr", "github.com/el-amin-dev", "/v1/docs"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("the landing page does not mention %q", want)
		}
	}
}

// TestLandingHeadHasNoBody covers the HEAD shortcut: the headers are the whole
// answer, and a body on a HEAD is a protocol violation.
func TestLandingHeadHasNoBody(t *testing.T) {
	t.Parallel()

	resp := docsCall{method: http.MethodHead, target: "/"}.send(t, docsHandler(t, nil))
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if len(body) != 0 {
		t.Errorf("HEAD returned %d bytes of body", len(body))
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("no Content-Length on a HEAD response")
	}
}

// TestDocsDisabled checks that BARQR_DOCS=false removes the UI entirely rather
// than serving a stub — an operator who turned it off gets no HTML surface.
func TestDocsDisabled(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, map[string]string{"BARQR_DOCS": "false"})

	for _, target := range []string{"/", "/v1/docs", "/v1/docs/swagger"} {
		resp := docsCall{target: target, accept: browser}.send(t, h)
		_ = readAll(t, resp)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("%s: status = %d, want %d when BARQR_DOCS=false", target, got, want)
		}
	}
}

// TestDocsUnlockSplitsOnAccept is the point of the whole unlock design: the
// same rejection has to be a readable page for a browser and the ordinary
// error envelope for anything scripted.
func TestDocsUnlockSplitsOnAccept(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, nil)

	t.Run("a browser gets the form", func(t *testing.T) {
		t.Parallel()

		resp := docsCall{target: "/v1/docs", accept: browser}.send(t, h)
		body := readAll(t, resp)

		if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "text/html; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		// The form must post the key back to this same path under the name
		// docsAuthorize reads, or unlocking silently does nothing.
		for _, want := range []string{`method="post"`, `name="key"`, `type="password"`} {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("the unlock page has no %s", want)
			}
		}
		if bytes.Contains(body, []byte(`action=`)) {
			t.Error("the unlock form has an action; it must post to the current path")
		}
		if !bytes.Contains(body, []byte("BARQR_API_KEYS")) {
			t.Error("the unlock page does not say where the key comes from")
		}
	})

	t.Run("a client gets JSON", func(t *testing.T) {
		t.Parallel()

		resp := docsCall{target: "/v1/docs", accept: "application/json"}.send(t, h)

		if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"),
			"application/json; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}

		f := faultOf(t, resp)
		if f.Code != httpapi.CodeUnauthorized {
			t.Errorf("code = %q, want %q", f.Code, httpapi.CodeUnauthorized)
		}
		if f.Hint == "" {
			t.Error("no hint telling the caller how to authenticate")
		}
	})

	t.Run("no Accept header at all is treated as a client", func(t *testing.T) {
		t.Parallel()

		resp := docsCall{target: "/v1/docs"}.send(t, h)
		if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"),
			"application/json; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
	})
}

// TestDocsAcceptsEveryKeyForm covers the three ways a key can reach a docs
// page. A browser can send none of the header forms on a navigation, which is
// why the cookie and the one-shot query parameter exist.
func TestDocsAcceptsEveryKeyForm(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, nil)

	tests := []struct {
		name       string
		call       docsCall
		wantStatus int
		wantCookie bool
	}{
		{
			name:       "x-api-key header",
			call:       docsCall{target: "/v1/docs", accept: browser, apiKey: testKey},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "bearer token",
			call:       docsCall{target: "/v1/docs", accept: browser, bearer: testKey},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "query parameter",
			call:       docsCall{target: "/v1/docs?key=" + testKey, accept: browser},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "cookie",
			call:       docsCall{target: "/v1/docs", accept: browser, cookie: testKey},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name: "posted form field",
			call: docsCall{
				method: http.MethodPost, target: "/v1/docs",
				accept: browser, form: "key=" + testKey,
			},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "second configured key",
			call:       docsCall{target: "/v1/docs", accept: browser, apiKey: "second-key"},
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "no key at all",
			call:       docsCall{target: "/v1/docs", accept: browser},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := tt.call.send(t, h)
			body := readAll(t, resp)

			if got := resp.StatusCode; got != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", got, tt.wantStatus, body)
			}

			c := cookieNamed(resp, "barqr_key")
			if !tt.wantCookie {
				return
			}
			if c == nil {
				t.Fatal("no barqr_key cookie; the next navigation would have to " +
					"present the key all over again")
			}
			if c.Value != tt.call.expectedKey() {
				t.Errorf("cookie value = %q, want %q", c.Value, tt.call.expectedKey())
			}
			if c.MaxAge <= 0 {
				t.Errorf("cookie Max-Age = %d, want a positive lifetime", c.MaxAge)
			}
			if c.Path != "/v1" {
				t.Errorf("cookie Path = %q, want /v1", c.Path)
			}
			if c.SameSite != http.SameSiteStrictMode {
				t.Errorf("cookie SameSite = %v, want Strict", c.SameSite)
			}
		})
	}
}

// expectedKey reports which key this call presented, whichever way it did.
func (c docsCall) expectedKey() string {
	switch {
	case c.apiKey != "":
		return c.apiKey
	case c.bearer != "":
		return c.bearer
	case c.cookie != "":
		return c.cookie
	case c.form != "":
		return strings.TrimPrefix(c.form, "key=")
	default:
		if i := strings.Index(c.target, "key="); i >= 0 {
			return c.target[i+len("key="):]
		}
		return ""
	}
}

// TestDocsWrongKeyClearsCookie: a stale key in a cookie must not keep failing
// silently on every navigation. The rejection expires it.
func TestDocsWrongKeyClearsCookie(t *testing.T) {
	t.Parallel()

	resp := docsCall{
		target: "/v1/docs?key=wrong",
		accept: browser,
		cookie: "stale-key",
	}.send(t, docsHandler(t, nil))
	_ = readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	c := cookieNamed(resp, "barqr_key")
	if c == nil {
		t.Fatal("no Set-Cookie clearing the rejected key")
	}
	if c.Value != "" {
		t.Errorf("cookie value = %q, want it emptied", c.Value)
	}
	if c.MaxAge >= 0 {
		t.Errorf("cookie Max-Age = %d, want it expired", c.MaxAge)
	}
}

// TestDocsOpenModeNeedsNoKey: with authentication off there is no key to store,
// so the cookie must not appear either.
func TestDocsOpenModeNeedsNoKey(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, map[string]string{"BARQR_AUTH_MODE": "open"})

	resp := docsCall{target: "/v1/docs", accept: browser}.send(t, h)
	body := readAll(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
	if c := cookieNamed(resp, "barqr_key"); c != nil {
		t.Errorf("an open instance set a key cookie: %q", c.Value)
	}
}

// TestDocsViews walks the route family. Every view is HTML, and an unknown one
// is a normal 404 rather than a silent fallback to the dashboard.
func TestDocsViews(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, map[string]string{"BARQR_AUTH_MODE": "open"})

	tests := []struct {
		name     string
		target   string
		contains []string
	}{
		{"dashboard", "/v1/docs", []string{"barqr"}},
		{"dashboard by filename", "/v1/docs/index.html", []string{"barqr"}},
		{"swagger", "/v1/docs/swagger", []string{
			"/v1/docs/swagger-ui.js", "/v1/docs/swagger-ui.css",
			"/v1/docs/swagger-preset.js", "SwaggerUIBundle", "requestInterceptor",
		}},
		{"redoc", "/v1/docs/redoc", []string{
			"/v1/docs/redoc.js", "Redoc.init", "/v1/openapi.json",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := docsCall{target: tt.target, accept: browser}.send(t, h)
			body := readAll(t, resp)

			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("status = %d, want %d: %s", got, want, body)
			}
			if got, want := resp.Header.Get("Content-Type"),
				"text/html; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
			for _, want := range tt.contains {
				if !bytes.Contains(body, []byte(want)) {
					t.Errorf("%s does not contain %q", tt.target, want)
				}
			}
		})
	}

	t.Run("an unknown view is a JSON 404", func(t *testing.T) {
		t.Parallel()

		resp := docsCall{target: "/v1/docs/nonsense", accept: browser}.send(t, h)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"),
			"application/json; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		if f := faultOf(t, resp); f.Code != httpapi.CodeNotFound {
			t.Errorf("code = %q, want %q", f.Code, httpapi.CodeNotFound)
		}
	})
}

// TestDocsAssetsServeBothEncodings covers the stored-gzip trick: a client that
// accepts gzip gets the embedded bytes untouched, and one that does not gets
// them decompressed rather than a file it cannot read.
func TestDocsAssetsServeBothEncodings(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, map[string]string{"BARQR_AUTH_MODE": "open"})
	gzipMagic := []byte{0x1f, 0x8b}

	tests := []struct {
		name   string
		target string
		mime   string
		prefix string
	}{
		{"swagger css", "/v1/docs/swagger-ui.css", "text/css; charset=utf-8", ".swagger-ui"},
		{"swagger bundle", "/v1/docs/swagger-ui.js", "text/javascript; charset=utf-8", "/*"},
		{"swagger preset", "/v1/docs/swagger-preset.js", "text/javascript; charset=utf-8", "/*"},
		{"redoc bundle", "/v1/docs/redoc.js", "text/javascript; charset=utf-8", "/*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("gzip", func(t *testing.T) {
				resp := docsCall{target: tt.target, encoding: "gzip, deflate, br"}.send(t, h)
				body := readAll(t, resp)

				if got, want := resp.StatusCode, http.StatusOK; got != want {
					t.Fatalf("status = %d, want %d", got, want)
				}
				if got := resp.Header.Get("Content-Type"); got != tt.mime {
					t.Errorf("Content-Type = %q, want %q", got, tt.mime)
				}
				if got, want := resp.Header.Get("Content-Encoding"), "gzip"; got != want {
					t.Errorf("Content-Encoding = %q, want %q", got, want)
				}
				if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
					t.Error("no Vary: Accept-Encoding; a cache would serve gzip to a " +
						"client that cannot read it")
				}
				if !bytes.HasPrefix(body, gzipMagic) {
					t.Error("the body is not gzip despite Content-Encoding: gzip")
				}
			})

			t.Run("identity", func(t *testing.T) {
				resp := docsCall{target: tt.target}.send(t, h)
				body := readAll(t, resp)

				if got, want := resp.StatusCode, http.StatusOK; got != want {
					t.Fatalf("status = %d, want %d", got, want)
				}
				if got := resp.Header.Get("Content-Encoding"); got != "" {
					t.Errorf("Content-Encoding = %q, want none", got)
				}
				if bytes.HasPrefix(body, gzipMagic) {
					t.Fatal("a client that did not offer gzip was sent gzip bytes")
				}
				if !bytes.HasPrefix(body, []byte(tt.prefix)) {
					t.Errorf("body starts with %q, want it to start with %q",
						firstBytes(body, 32), tt.prefix)
				}
			})
		})
	}
}

// TestDocsAssetsAreBehindTheKey: the bundles are only reachable through
// handleDocs, so an instance with auth on must not hand them out anonymously.
func TestDocsAssetsAreBehindTheKey(t *testing.T) {
	t.Parallel()

	h := docsHandler(t, nil)

	resp := docsCall{target: "/v1/docs/swagger-ui.css"}.send(t, h)
	_ = readAll(t, resp)
	if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d without a key", got, want)
	}

	with := docsCall{target: "/v1/docs/swagger-ui.css", cookie: testKey}.send(t, h)
	_ = readAll(t, with)
	if got, want := with.StatusCode, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d with the cookie the page was served with", got, want)
	}
}

// firstBytes renders a prefix of a body for a failure message.
func firstBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

// TestDocsPagesAreNeverSharedCached: every docs page is reached with a key in
// play, so a shared cache holding one would hand it to the next visitor.
func TestDocsPagesAreNeverSharedCached(t *testing.T) {
	t.Parallel()

	for _, page := range docsPages(t) {
		t.Run(page.name, func(t *testing.T) {
			t.Parallel()

			if got := page.resp.Header.Get("Content-Security-Policy"); got == "" {
				t.Error("no Content-Security-Policy")
			} else if !strings.Contains(got, "default-src 'none'") {
				t.Errorf("Content-Security-Policy = %q, want it to start from a deny-all", got)
			}
			if got, want := page.resp.Header.Get("X-Content-Type-Options"), "nosniff"; got != want {
				t.Errorf("X-Content-Type-Options = %q, want %q", got, want)
			}
			if got, want := page.resp.Header.Get("Cache-Control"), "private, no-store"; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
			for _, forbidden := range []string{"public", "s-maxage", "max-age"} {
				if strings.Contains(page.resp.Header.Get("Cache-Control"), forbidden) {
					t.Errorf("Cache-Control contains %q; a shared cache would keep this page",
						forbidden)
				}
			}
		})
	}
}

// TestDocsPlaceholdersAreSubstituted is the guard a new page trips: a page that
// is not run through the substitution ships a literal {{STYLE}} and renders
// unstyled, or worse, ships an empty capability payload.
func TestDocsPlaceholdersAreSubstituted(t *testing.T) {
	t.Parallel()

	for _, page := range docsPages(t) {
		t.Run(page.name, func(t *testing.T) {
			t.Parallel()

			for _, placeholder := range []string{"{{STYLE}}", "{{DATA}}", "{{VERSION}}"} {
				if bytes.Contains(page.body, []byte(placeholder)) {
					t.Errorf("%s survived into the served page", placeholder)
				}
			}
			// Swagger UI and ReDoc are deliberately stock: the shared theme is
			// not injected into them, because its element-level rules leaked
			// into their DOM and flattened their own colour language. The
			// pages barqr owns must still carry it.
			stock := page.name == "swagger" || page.name == "redoc"

			for _, token := range []string{"--md-primary", "--md-on-surface", "--md-surface"} {
				has := bytes.Contains(page.body, []byte(token))
				switch {
				case stock && has:
					t.Errorf("%s should be stock, but the barqr theme token %s "+
						"was injected into it", page.name, token)
				case !stock && !has:
					t.Errorf("the theme token %s is missing; the page will render unstyled",
						token)
				}
			}
		})
	}
}

// TestDocsPayloadIsValidJSON: the pages build themselves from the injected
// payload, so a payload that does not parse is a blank page. The escaping of
// "<" is what makes a registered name containing markup safe here.
func TestDocsPayloadIsValidJSON(t *testing.T) {
	t.Parallel()

	block := regexp.MustCompile(`(?s)<script[^>]+type="application/json"[^>]*>(.*?)</script>`)
	h := docsHandler(t, map[string]string{"BARQR_AUTH_MODE": "open"})

	for _, target := range []string{"/", "/v1/docs"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			body := readAll(t, docsCall{target: target, accept: browser}.send(t, h))
			m := block.FindSubmatch(body)
			if m == nil {
				t.Fatalf("%s embeds no application/json payload block", target)
			}
			if bytes.Contains(m[1], []byte("</script")) {
				t.Error("the payload can close its own script block")
			}

			var payload struct {
				Version struct {
					Version string `json:"version"`
				} `json:"version"`
				Counts struct {
					Symbologies int `json:"symbologies"`
					Formats     int `json:"formats"`
				} `json:"counts"`
				Endpoints []struct {
					Route string `json:"route"`
				} `json:"endpoints"`
			}
			if err := json.Unmarshal(m[1], &payload); err != nil {
				t.Fatalf("the injected payload is not valid JSON: %v", err)
			}
			if payload.Counts.Symbologies <= 0 {
				t.Errorf("counts.symbologies = %d, want a build with at least one "+
					"working symbology", payload.Counts.Symbologies)
			}
			if payload.Counts.Formats <= 0 {
				t.Errorf("counts.formats = %d, want at least one output format",
					payload.Counts.Formats)
			}
			if payload.Version.Version == "" {
				t.Error("the payload carries no version")
			}
			if len(payload.Endpoints) == 0 {
				t.Error("the payload lists no endpoints; it is not built from the router")
			}
		})
	}
}

// servedPage is one rendered docs response, captured once and shared by the
// header and placeholder tables.
type servedPage struct {
	name string
	resp *http.Response
	body []byte
}

// docsPages fetches every HTML surface the docs UI can produce, including the
// 401 unlock page, which is served on a different path through the handler and
// is exactly the one a new placeholder is likeliest to be forgotten on.
func docsPages(t *testing.T) []servedPage {
	t.Helper()

	open := docsHandler(t, map[string]string{"BARQR_AUTH_MODE": "open"})
	locked := docsHandler(t, nil)

	calls := []struct {
		name string
		h    http.Handler
		call docsCall
	}{
		{"landing", open, docsCall{target: "/", accept: browser}},
		{"dashboard", open, docsCall{target: "/v1/docs", accept: browser}},
		{"swagger", open, docsCall{target: "/v1/docs/swagger", accept: browser}},
		{"redoc", open, docsCall{target: "/v1/docs/redoc", accept: browser}},
		{"unlock", locked, docsCall{target: "/v1/docs", accept: browser}},
	}

	pages := make([]servedPage, 0, len(calls))
	for _, c := range calls {
		resp := c.call.send(t, c.h)
		body := readAll(t, resp)
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("%s: Content-Type = %q, want HTML", c.name, got)
		}
		pages = append(pages, servedPage{name: c.name, resp: resp, body: body})
	}
	return pages
}
