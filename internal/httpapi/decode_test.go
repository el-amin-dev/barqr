package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeQuery decodes a GET with the given query string.
func decodeQuery(t *testing.T, query string) (Request, error) {
	t.Helper()
	return Decode(httptest.NewRequest(http.MethodGet, "/v1/qr?"+query, nil), "qr")
}

// decodeJSON decodes a POST with a JSON body.
func decodeJSON(t *testing.T, body string) (Request, error) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/v1/qr", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return Decode(r, "qr")
}

func TestDecodeQueryDotNotation(t *testing.T) {
	t.Parallel()

	req, err := decodeQuery(t,
		"data=hello&style.module=dot&style.fg=%23111&output.format=svg&output.scale=8"+
			"&encode.ecc=H&encode.quiet_zone=6&meta.attachment=true&payload.ssid=Lobby")
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}

	if got, want := req.Data, "hello"; got != want {
		t.Errorf("Data = %q, want %q", got, want)
	}
	if got, want := req.Style.Module, "dot"; got != want {
		t.Errorf("Style.Module = %q, want %q", got, want)
	}
	if got, want := req.Style.FG, "#111"; got != want {
		t.Errorf("Style.FG = %q, want %q", got, want)
	}
	if got, want := req.Output.Format, "svg"; got != want {
		t.Errorf("Output.Format = %q, want %q", got, want)
	}
	if got, want := req.Output.Scale, 8; got != want {
		t.Errorf("Output.Scale = %d, want %d", got, want)
	}
	if got, want := req.Encode.ECC, "H"; got != want {
		t.Errorf("Encode.ECC = %q, want %q", got, want)
	}
	if req.Encode.QuietZone == nil || *req.Encode.QuietZone != 6 {
		t.Errorf("Encode.QuietZone = %v, want 6", req.Encode.QuietZone)
	}
	if !req.Meta.Attachment {
		t.Error("Meta.Attachment = false, want true")
	}
	if got, want := req.Payload["ssid"], "Lobby"; got != want {
		t.Errorf("Payload[ssid] = %v, want %q", got, want)
	}
	if got, want := req.Symbology, "qr"; got != want {
		t.Errorf("Symbology = %q, want the endpoint default %q", got, want)
	}
}

// TestDecodeTransportsAgree is the invariant the whole request layer exists
// for: a query string, a JSON body, and a multipart form describing the same
// request must produce the identical struct.
func TestDecodeTransportsAgree(t *testing.T) {
	t.Parallel()

	fromQuery, err := decodeQuery(t,
		"data=hi&style.module=dot&output.format=png&output.scale=12&encode.ecc=Q")
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	fromJSON, err := decodeJSON(t, `{
		"data": "hi",
		"style":  {"module": "dot"},
		"output": {"format": "png", "scale": 12},
		"encode": {"ecc": "Q"}
	}`)
	if err != nil {
		t.Fatalf("json: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"data": "hi", "style.module": "dot",
		"output.format": "png", "output.scale": "12", "encode.ecc": "Q",
	} {
		if writeErr := mw.WriteField(k, v); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if closeErr := mw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/qr", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	fromForm, err := Decode(r, "qr")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}

	a, _ := json.Marshal(fromQuery)
	b, _ := json.Marshal(fromJSON)
	c, _ := json.Marshal(fromForm)

	if !bytes.Equal(a, b) {
		t.Errorf("query and JSON disagree:\n query = %s\n  json = %s", a, b)
	}
	if !bytes.Equal(a, c) {
		t.Errorf("query and multipart disagree:\n    query = %s\nmultipart = %s", a, c)
	}
}

func TestDecodeBodyOverridesQuery(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/v1/qr?output.format=png&output.scale=4",
		strings.NewReader(`{"output":{"format":"svg"}}`))
	r.Header.Set("Content-Type", "application/json")

	req, err := Decode(r, "qr")
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	// The body is the more specific statement of intent, so it wins...
	if got, want := req.Output.Format, "svg"; got != want {
		t.Errorf("Output.Format = %q, want %q", got, want)
	}
	// ...but only for the fields it actually sets.
	if got, want := req.Output.Scale, 4; got != want {
		t.Errorf("Output.Scale = %d, want %d from the query", got, want)
	}
}

func TestDecodeUnknownFieldSuggests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		decode    func(*testing.T) (Request, error)
		wantField string
		wantHint  string
	}{
		{
			name:      "query typo",
			decode:    func(t *testing.T) (Request, error) { return decodeQuery(t, "output.formt=png") },
			wantField: "output.formt",
			wantHint:  "output.format",
		},
		{
			name:      "json typo",
			decode:    func(t *testing.T) (Request, error) { return decodeJSON(t, `{"styl":{}}`) },
			wantField: "styl",
			wantHint:  "style",
		},
		{
			name:      "nested json typo",
			decode:    func(t *testing.T) (Request, error) { return decodeJSON(t, `{"style":{"modul":"dot"}}`) },
			wantField: "modul",
			wantHint:  "module",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.decode(t)
			if err == nil {
				t.Fatal("Decode() succeeded, want an unknown-field error")
			}
			f := asFault(err)
			if f.Code != CodeUnknownField {
				t.Fatalf("code = %q, want %q (%v)", f.Code, CodeUnknownField, err)
			}
			if !strings.Contains(f.Field, tt.wantField) && !strings.Contains(f.Message, tt.wantField) {
				t.Errorf("error does not name %q: %+v", tt.wantField, f)
			}
			if !strings.Contains(f.Hint, tt.wantHint) {
				t.Errorf("hint = %q, want it to suggest %q", f.Hint, tt.wantHint)
			}
		})
	}
}

func TestDecodeInvalidValue(t *testing.T) {
	t.Parallel()

	_, err := decodeQuery(t, "output.scale=enormous")
	if err == nil {
		t.Fatal("Decode() succeeded, want an invalid-value error")
	}

	f := asFault(err)
	if got, want := f.Code, CodeInvalidValue; got != want {
		t.Fatalf("code = %q, want %q", got, want)
	}
	if got, want := f.Field, "output.scale"; got != want {
		t.Errorf("field = %q, want %q", got, want)
	}
	if got, want := f.Got, "enormous"; got != want {
		t.Errorf("got = %q, want the offending value %q", got, want)
	}
	if f.Expected == "" {
		t.Error("expected is empty; the caller cannot tell what was wanted")
	}
}

func TestDecodeAutoInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query   string
		wantSet bool
		wantVal int
		wantErr bool
	}{
		{query: "encode.mask=3", wantSet: true, wantVal: 3},
		{query: "encode.mask=0", wantSet: true, wantVal: 0},
		{query: "encode.mask=auto"},
		{query: "encode.mask=AUTO"},
		{query: "encode.mask=", wantSet: false},
		{query: "encode.mask=maybe", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			req, err := decodeQuery(t, tt.query)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Decode() succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() returned error: %v", err)
			}
			if got := req.Encode.Mask.Set; got != tt.wantSet {
				t.Errorf("Mask.Set = %v, want %v", got, tt.wantSet)
			}
			if tt.wantSet && req.Encode.Mask.Value != tt.wantVal {
				t.Errorf("Mask.Value = %d, want %d", req.Encode.Mask.Value, tt.wantVal)
			}
		})
	}
}

// TestAutoIntJSONRoundTrip covers the number-or-"auto" form in both directions.
func TestAutoIntJSONRoundTrip(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{"encode":{"mask":5}}`, `{"encode":{"mask":"auto"}}`,
		`{"encode":{"mask":null}}`} {
		req, err := decodeJSON(t, body)
		if err != nil {
			t.Fatalf("Decode(%s) returned error: %v", body, err)
		}

		out, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var round Request
		if err := json.Unmarshal(out, &round); err != nil {
			t.Fatalf("re-decoding %s: %v", out, err)
		}
		if round.Encode.Mask != req.Encode.Mask {
			t.Errorf("Mask did not round-trip: %+v -> %+v", req.Encode.Mask, round.Encode.Mask)
		}
	}

	if got, want := Auto.String(), "auto"; got != want {
		t.Errorf("Auto.String() = %q, want %q", got, want)
	}
	if !Auto.IsZero() {
		t.Error("Auto.IsZero() = false")
	}
}

func TestDecodeBodyRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{"malformed json", "application/json", `{"data":`, http.StatusBadRequest},
		{"two json values", "application/json", `{"data":"a"}{"data":"b"}`, http.StatusBadRequest},
		{"wrong type for a field", "application/json", `{"data":123}`, http.StatusBadRequest},
		{"unsupported media type", "application/xml", `<xml/>`, http.StatusUnsupportedMediaType},
		{"unparseable content type", "application/;;;", `{}`, http.StatusUnsupportedMediaType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/v1/qr", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)

			_, err := Decode(r, "qr")
			if err == nil {
				t.Fatal("Decode() succeeded, want an error")
			}
			if got := asFault(err).Status(); got != tt.wantStatus {
				t.Errorf("status = %d, want %d (%v)", got, tt.wantStatus, err)
			}
		})
	}
}

// TestDecodeBareBodyIsData covers `curl --data-binary @file` with no
// Content-Type, which is what a shell user reaches for.
func TestDecodeBareBodyIsData(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/v1/qr", strings.NewReader("https://example.com"))
	r.Header.Del("Content-Type")

	req, err := Decode(r, "qr")
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if got, want := req.Data, "https://example.com"; got != want {
		t.Errorf("Data = %q, want %q", got, want)
	}
}

func TestDecodeEmptyJSONBodyIsLegal(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/v1/qr?data=hi", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/json")

	req, err := Decode(r, "qr")
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if got, want := req.Data, "hi"; got != want {
		t.Errorf("Data = %q, want the query value %q", got, want)
	}
}

// TestDecodeMultipartFileBecomesDataURI covers an uploaded logo overriding the
// string field of the same name.
func TestDecodeMultipartFileBecomesDataURI(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("data", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("style.logo", "ignored-because-a-file-wins"); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("style.logo", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	// A one-pixel PNG is enough: the decoder only needs the bytes.
	if _, writeErr := part.Write([]byte("\x89PNG\r\n\x1a\n-not-a-real-png")); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := mw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/qr", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	req, err := Decode(r, "qr")
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if !strings.HasPrefix(req.Style.Logo, "data:") {
		t.Fatalf("Style.Logo = %q, want a data URI", req.Style.Logo)
	}
	if !strings.Contains(req.Style.Logo, ";base64,") {
		t.Errorf("Style.Logo = %q, want base64 encoding", req.Style.Logo)
	}
}

// TestPathIndexCoversEverySection guards the reflection walk: if a section is
// ever added to Request without a JSON tag, the query transport would silently
// lose it while JSON kept working.
func TestPathIndexCoversEverySection(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"type", "data", "symbology",
		"encode.ecc", "encode.version", "encode.mask", "encode.quiet_zone",
		"style.module", "style.eye", "style.eye_ball", "style.fg", "style.bg",
		"style.eye_fg", "style.bar_height", "style.hri", "style.logo",
		"style.logo_scale", "style.excavate", "style.caption", "style.frame",
		"output.format", "output.scale", "output.size", "output.unit",
		"output.dpi", "output.quality",
		"meta.filename", "meta.attachment",
	} {
		if _, ok := pathIndex[key]; !ok {
			t.Errorf("dot-notation key %q is not reachable from the query string", key)
		}
	}
	// The payload map must NOT be a settable leaf: it is the open-ended
	// namespace handled separately.
	if _, ok := pathIndex["payload"]; ok {
		t.Error("payload is indexed as a leaf; it must be handled as a namespace")
	}
}

func TestClosestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantHit bool
	}{
		{"output.formt", "output.format", true},
		{"stlye.module", "style.module", true},
		{"dat", "data", true},
		{"completely-unrelated-nonsense", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, ok := closest(tt.in, knownPaths())
			if ok != tt.wantHit {
				t.Fatalf("closest(%q) ok = %v, want %v (got %q)", tt.in, ok, tt.wantHit, got)
			}
			if tt.wantHit && got != tt.want {
				t.Errorf("closest(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
	}

	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseBool(t *testing.T) {
	t.Parallel()

	// A bare flag means true: that is what `?meta.attachment` written by hand
	// implies, and rejecting it would be pedantic.
	for _, in := range []string{"", "on", "yes", "true", "1"} {
		v, err := parseBool(in)
		if err != nil || !v {
			t.Errorf("parseBool(%q) = %v, %v; want true, nil", in, v, err)
		}
	}
	for _, in := range []string{"off", "no", "false", "0"} {
		v, err := parseBool(in)
		if err != nil || v {
			t.Errorf("parseBool(%q) = %v, %v; want false, nil", in, v, err)
		}
	}
	if _, err := parseBool("perhaps"); err == nil {
		t.Error("parseBool(perhaps) succeeded")
	}
}

// FuzzDecodeQuery drives the request decoder with arbitrary query strings.
//
// The decoder is the first thing an untrusted request touches, and it uses
// reflection to write into a struct. The property under test is simply that it
// never panics: every malformed input must come back as an error.
func FuzzDecodeQuery(f *testing.F) {
	for _, seed := range []string{
		"data=hi",
		"style.module=dot&output.format=svg",
		"encode.mask=auto&encode.version=3",
		"payload.ssid=x&payload.password=y",
		"output.scale=999999999999999999999",
		"meta.attachment",
		"=&&=&data",
		"style.fg=%ZZ",
		strings.Repeat("a.b.c=1&", 50),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		r := httptest.NewRequest(http.MethodGet, "/v1/qr", nil)
		r.URL.RawQuery = query

		req, err := Decode(r, "qr")
		if err != nil {
			// Every error must map onto the wire shape with a real code, or a
			// client sees an empty error object.
			if f := asFault(err); f.Code == "" || f.Message == "" {
				t.Fatalf("error %v mapped to an incomplete fault %+v", err, f)
			}
			return
		}
		// A successful decode must be serialisable, since /v1/validate echoes
		// parts of it back.
		if _, err := json.Marshal(req); err != nil {
			t.Fatalf("decoded request does not marshal: %v", err)
		}
	})
}

// FuzzDecodeJSON drives the JSON transport with arbitrary bodies.
func FuzzDecodeJSON(f *testing.F) {
	for _, seed := range []string{
		`{"data":"hi"}`,
		`{"encode":{"mask":"auto"}}`,
		`{"style":{"fg":"#fff"}}`,
		`{"payload":{"ssid":"x"}}`,
		`{`, `[]`, `null`, `0`, `""`,
		`{"data":"` + strings.Repeat("x", 200) + `"}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		r := httptest.NewRequest(http.MethodPost, "/v1/qr", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		if _, err := Decode(r, "qr"); err != nil {
			if f := asFault(err); f.Code == "" || f.Message == "" {
				t.Fatalf("error %v mapped to an incomplete fault %+v", err, f)
			}
		}
	})
}
