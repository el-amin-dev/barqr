package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// tinyPNG is a real image as a data URI, for style.logo.
func tinyPNG(t *testing.T) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the test logo: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestEveryStyleOptionChangesTheOutput is a regression guard for a whole class
// of bug rather than one instance of it.
//
// `style.frame`, `style.caption`, `style.logo`, `style.excavate` and
// `style.gradient` were all declared on the request, decoded from the query
// string, validated, and then silently dropped on the floor: the pipeline
// never copied them onto the render style. Every unit test passed, because
// each layer was correct in isolation — the wiring between them was missing,
// so the entire style engine was unreachable over HTTP.
//
// The property here is simple and hard to fake: setting an option must change
// the bytes that come back. A future option that is decoded but never applied
// fails this immediately.
func TestEveryStyleOptionChangesTheOutput(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	render := func(t *testing.T, extra url.Values) []byte {
		t.Helper()

		q := url.Values{"data": {"https://barqr.dev"}, "output.format": {"svg"}}
		for k, v := range extra {
			q[k] = v
		}

		resp := do(t, h, http.MethodGet, "/v1/qr?"+q.Encode())
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d for %v: %s", resp.StatusCode, extra, body)
		}
		return body
	}

	plain := render(t, nil)

	tests := []struct {
		name string
		opts url.Values
	}{
		{"module shape", url.Values{"style.module": {"dot"}}},
		{"eye shape", url.Values{"style.eye": {"circle"}}},
		{"eye ball shape", url.Values{"style.eye_ball": {"circle"}}},
		{"foreground colour", url.Values{"style.fg": {"#123456"}}},
		{"background colour", url.Values{"style.bg": {"#fedcba"}}},
		{"eye colour", url.Values{"style.eye_fg": {"#ff0000"}}},
		{"quiet zone", url.Values{"encode.quiet_zone": {"8"}}},
		{"gradient", url.Values{"style.gradient": {"linear(45deg,#000,#00f)"}}},
		{"frame", url.Values{"style.frame": {"border"}}},
		{"frame width", url.Values{"style.frame": {"border"}, "style.frame_width": {"3"}}},
		{"frame colour", url.Values{"style.frame": {"border"}, "style.frame_color": {"#112233"}}},
		{"caption", url.Values{"style.caption": {"SCAN ME"}}},
		{"caption colour", url.Values{
			"style.caption": {"SCAN ME"}, "style.caption_color": {"#445566"}}},
		{"error correction", url.Values{"encode.ecc": {"H"}}},
		{"scale", url.Values{"output.scale": {"20"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := render(t, tt.opts); bytes.Equal(got, plain) {
				t.Errorf("%v produced output identical to the default; the option is "+
					"decoded but never reaches the renderer", tt.opts)
			}
		})
	}
}

// TestLogoOptionsReachTheRenderer covers the logo separately: it needs a real
// image, and excavation changes the module grid rather than only the drawing,
// which is worth asserting on its own.
func TestLogoOptionsReachTheRenderer(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()
	logo := tinyPNG(t)

	get := func(t *testing.T, q url.Values) []byte {
		t.Helper()

		resp := do(t, h, http.MethodGet, "/v1/qr?"+q.Encode())
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.StatusCode, body)
		}
		return body
	}

	base := url.Values{
		"data": {"https://barqr.dev"}, "output.format": {"svg"}, "encode.ecc": {"H"},
	}

	plain := get(t, base)

	withLogo := url.Values{}
	for k, v := range base {
		withLogo[k] = v
	}
	withLogo["style.logo"] = []string{logo}
	logoed := get(t, withLogo)

	if bytes.Equal(plain, logoed) {
		t.Fatal("style.logo produced identical output; the logo never reached the renderer")
	}
	if !bytes.Contains(logoed, []byte("<image")) {
		t.Error("the SVG carries no <image> element for the logo")
	}

	// Excavation clears modules under the logo, so it must differ from the
	// same logo drawn over live data.
	excavated := url.Values{}
	for k, v := range withLogo {
		excavated[k] = v
	}
	excavated["style.excavate"] = []string{"true"}

	if bytes.Equal(logoed, get(t, excavated)) {
		t.Error("style.excavate changed nothing; excavation never reached the renderer")
	}
}

// TestStyleRejectionsNameTheField checks that a bad value is refused against
// the field the caller actually set, which is what makes the error actionable.
func TestStyleRejectionsNameTheField(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name      string
		query     string
		wantField string
	}{
		{"bad gradient", "style.gradient=sideways(#000)", "style.gradient"},
		{"bad frame kind", "style.frame=hexagon", "style.frame"},
		{"bad frame colour", "style.frame=border&style.frame_color=zzz", "style.frame_color"},
		{"bad caption colour", "style.caption=x&style.caption_color=zzz", "style.caption_color"},
		// The semicolon is percent-encoded: a bare one ends the query as far as
		// net/url is concerned, which is its own rejection, tested below.
		{"logo that is not an image",
			"style.logo=data:image/png%3Bbase64,bm90YW5pbWFnZQ%3D%3D", "style.logo"},
		{"remote logo while fetching is off", "style.logo=https://cdn.example/l.png", "style.logo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := do(t, h, http.MethodGet, "/v1/qr?data=hi&"+tt.query)
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("status = 200 for %q, want a rejection", tt.query)
			}

			f := faultOf(t, resp)
			if !strings.HasPrefix(f.Field, "style.") {
				t.Errorf("field = %q, want the style field that was set", f.Field)
			}
			if f.Field != tt.wantField {
				t.Errorf("field = %q, want %q", f.Field, tt.wantField)
			}
			if f.Message == "" {
				t.Error("the rejection carries no message")
			}
		})
	}
}

// TestSemicolonInQueryIsRejected covers a silent drop that used to be
// invisible.
//
// Since Go 1.17 net/url refuses a bare ";" as a parameter separator, and
// http.Request.Query discards that error and returns whatever it managed to
// parse. A caller pasting a data URI — `?style.logo=data:image/png;base64,…` —
// lost that parameter and every one after it, and got a plain code back with
// no logo and no explanation.
func TestSemicolonInQueryIsRejected(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	resp := do(t, h, http.MethodGet,
		"/v1/qr?data=hi&style.logo=data:image/png;base64,AAAA")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
	}

	f := faultOf(t, resp)
	if !strings.Contains(strings.ToLower(f.Message), "semicolon") {
		t.Errorf("message = %q, want it to explain the semicolon", f.Message)
	}
	if !strings.Contains(f.Hint, "%3B") {
		t.Errorf("hint = %q, want it to suggest percent-encoding", f.Hint)
	}

	// The percent-encoded form must still work.
	ok := do(t, h, http.MethodGet, "/v1/qr?data=hi&style.caption=a%3Bb&output.format=svg")
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("the percent-encoded form was rejected: %s", readAll(t, ok))
	}
	_ = readAll(t, ok)
}

// TestEveryLinearStyleOptionChangesTheOutput is the sibling of the test above,
// for the options that only a linear code has.
//
// It exists separately because the test above renders a QR, and a QR has no
// human-readable line — an hri_* row added there would pass while doing
// nothing. It runs every row against a vector format and a raster one, because
// the two draw text through completely different machinery: the failure this
// guards is an option honoured by the SVG writer and quietly dropped by the
// rasteriser, which is what a face selector on a single-font path would be.
func TestEveryLinearStyleOptionChangesTheOutput(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	render := func(t *testing.T, format string, extra url.Values) []byte {
		t.Helper()

		// The reporter's own payload: letters and digits together, which is
		// the case the human-readable line has to survive.
		q := url.Values{
			"data":          {"JX8QQEMJQ0KR"},
			"output.format": {format},
			"output.scale":  {"8"},
		}
		for k, v := range extra {
			q[k] = v
		}

		resp := do(t, h, http.MethodGet, "/v1/barcode/code39?"+q.Encode())
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d for %v as %s: %s", resp.StatusCode, extra, format, body)
		}
		return body
	}

	tests := []struct {
		name string
		opts url.Values
	}{
		{"human-readable text off", url.Values{"style.hri": {"false"}}},
		{"human-readable text size", url.Values{"style.hri_size": {"5"}}},
		{"human-readable text font", url.Values{"style.hri_font": {"sans"}}},
		{"bar height", url.Values{"style.bar_height": {"40"}}},
	}

	for _, format := range []string{"svg", "png"} {
		for _, tt := range tests {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				plain := render(t, format, nil)
				if got := render(t, format, tt.opts); bytes.Equal(got, plain) {
					t.Errorf("%v produced output identical to the default as %s; "+
						"the option is decoded but never reaches the renderer",
						tt.opts, format)
				}
			})
		}
	}
}

// TestHRIOptionsAreRefusedNotIgnored checks the other half of the contract: a
// value barqr cannot honour has to say so, in the same shape as every other
// validation failure, rather than falling back to a default the caller did not
// ask for and cannot see.
func TestHRIOptionsAreRefusedNotIgnored(t *testing.T) {
	t.Parallel()

	h := serverWith(t, nil).Handler()

	tests := []struct {
		name  string
		query string
		field string
	}{
		{"unknown font", "style.hri_font=comic", "style.hri_font"},
		{"size below the floor", "style.hri_size=0.3", "style.hri_size"},
		{"size above the ceiling", "style.hri_size=99", "style.hri_size"},
	}

	// Every output format must agree. A request that succeeded as SVG and
	// failed as PNG would make the error depend on a field the caller thinks
	// is unrelated.
	for _, format := range []string{"svg", "png", "pdf"} {
		for _, tt := range tests {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				resp := do(t, h, http.MethodGet,
					"/v1/barcode/code39?data=ABC&output.format="+format+"&"+tt.query)
				body := readAll(t, resp)
				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d as %s, want 400: %s", resp.StatusCode, format, body)
				}
				if !bytes.Contains(body, []byte(tt.field)) {
					t.Errorf("error does not name %s: %s", tt.field, body)
				}
			})
		}
	}
}
