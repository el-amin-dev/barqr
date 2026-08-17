package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/fetch"
	"github.com/el-amin-dev/barqr/internal/render"
)

// logoServer builds a Server from environment overrides, logging discarded.
func logoServer(t *testing.T, env ...string) *Server {
	t.Helper()

	cfg, _, err := config.Load(append([]string{"BARQR_AUTH_MODE=open"}, env...))
	if err != nil {
		t.Fatalf("config.Load() returned error: %v", err)
	}
	return New(cfg, slog.New(slog.DiscardHandler))
}

// faultFrom asserts that err is a *Fault and returns it.
func faultFrom(t *testing.T, err error) *Fault {
	t.Helper()

	var f *Fault
	if !errors.As(err, &f) {
		t.Fatalf("error %v is %T, want *Fault", err, err)
	}
	return f
}

func TestResolveLogoPassesDataURIsThrough(t *testing.T) {
	t.Parallel()

	const uri = "data:image/png;base64,iVBORw0KGgo="
	refs := []string{uri, " " + uri + " ", "DATA:image/png;base64,iVBORw0KGgo="}

	// Fetching is off: a data URI must not care either way.
	s := logoServer(t)
	for _, ref := range refs {
		got, err := s.resolveLogo(t.Context(), ref)
		if err != nil {
			t.Fatalf("resolveLogo(%q) returned error: %v", ref, err)
		}
		if got != strings.TrimSpace(ref) {
			t.Errorf("resolveLogo(%q) = %q, want it unchanged", ref, got)
		}
	}
}

func TestResolveLogoIgnoresAnEmptyReference(t *testing.T) {
	t.Parallel()

	s := logoServer(t)
	for _, ref := range []string{"", "   "} {
		got, err := s.resolveLogo(t.Context(), ref)
		if err != nil || got != "" {
			t.Errorf("resolveLogo(%q) = (%q, %v), want (\"\", nil)", ref, got, err)
		}
	}
}

// TestResolveLogoRefusesARemoteLogoWhenFetchingIsOff pins the default posture:
// the capability is absent, and the answer says so rather than ignoring the
// field.
func TestResolveLogoRefusesARemoteLogoWhenFetchingIsOff(t *testing.T) {
	t.Parallel()

	s := logoServer(t, "BARQR_ALLOW_REMOTE_FETCH=false")

	_, err := s.resolveLogo(t.Context(), "https://cdn.example/logo.png")
	f := faultFrom(t, err)

	if f.Status() != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", f.Status(), http.StatusNotImplemented)
	}
	if f.Code != CodeUnsupported {
		t.Errorf("code = %q, want %q", f.Code, CodeUnsupported)
	}
	if f.Field != "style.logo" {
		t.Errorf("field = %q, want style.logo", f.Field)
	}
	if !strings.Contains(f.Hint, "BARQR_ALLOW_REMOTE_FETCH") {
		t.Errorf("hint %q should name the switch that turns the feature on", f.Hint)
	}
}

// TestResolveLogoRefusesWhenEnabledButNothingIsAllowlisted covers the
// misconfiguration an operator is most likely to make, and the reason
// fetch.Enabled exists at all.
func TestResolveLogoRefusesWhenEnabledButNothingIsAllowlisted(t *testing.T) {
	t.Parallel()

	s := logoServer(t, "BARQR_ALLOW_REMOTE_FETCH=true", "BARQR_FETCH_ALLOWLIST=")

	_, err := s.resolveLogo(t.Context(), "https://cdn.example/logo.png")
	f := faultFrom(t, err)

	if f.Status() != http.StatusForbidden {
		t.Errorf("status = %d, want %d", f.Status(), http.StatusForbidden)
	}
	if f.Code != CodeFetchNotAllowed {
		t.Errorf("code = %q, want %q", f.Code, CodeFetchNotAllowed)
	}
	if !strings.Contains(f.Hint, "BARQR_FETCH_ALLOWLIST") {
		t.Errorf("hint %q should name the missing setting", f.Hint)
	}
}

func TestResolveLogoRefusesAHostThatIsNotAllowlisted(t *testing.T) {
	t.Parallel()

	s := logoServer(t,
		"BARQR_ALLOW_REMOTE_FETCH=true",
		"BARQR_FETCH_ALLOWLIST=cdn.example")

	// A suffix of an allowlisted host, which is a name an attacker can own.
	_, err := s.resolveLogo(t.Context(), "https://evil-cdn.example/logo.png")
	f := faultFrom(t, err)

	if f.Status() != http.StatusForbidden || f.Code != CodeFetchNotAllowed {
		t.Fatalf("status/code = %d/%s, want 403/%s", f.Status(), f.Code, CodeFetchNotAllowed)
	}
	if !strings.Contains(f.Expected, "cdn.example") {
		t.Errorf("expected %q should name the allowlist", f.Expected)
	}
	if f.Got != "evil-cdn.example" {
		t.Errorf("got = %q, want the host the caller asked for", f.Got)
	}
}

// TestResolveLogoBlocksALoopbackTarget runs the whole wiring against a real
// TLS server on the loopback: the fetcher refuses to connect, and the refusal
// arrives as a 403 rather than as a rendered code.
func TestResolveLogoBlocksALoopbackTarget(t *testing.T) {
	t.Parallel()

	var hits int
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	host := ts.Listener.Addr().String() // 127.0.0.1:port
	s := logoServer(t,
		"BARQR_ALLOW_REMOTE_FETCH=true",
		"BARQR_FETCH_ALLOWLIST=127.0.0.1",
		"BARQR_FETCH_TIMEOUT=2s")

	_, err := s.resolveLogo(t.Context(), "https://"+host+"/logo.png")
	f := faultFrom(t, err)

	if f.Status() != http.StatusForbidden || f.Code != CodeFetchBlocked {
		t.Fatalf("status/code = %d/%s, want 403/%s", f.Status(), f.Code, CodeFetchBlocked)
	}
	if hits != 0 {
		t.Errorf("the blocked host was reached %d times, want 0", hits)
	}
}

func TestLogoFetchFaultMapsEverySentinel(t *testing.T) {
	t.Parallel()

	opts := fetch.Options{Allowlist: []string{"cdn.example"}, MaxBytes: 2 << 20}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not allowed", fetch.ErrNotAllowed, http.StatusForbidden, CodeFetchNotAllowed},
		{"private address", fetch.ErrPrivateAddress, http.StatusForbidden, CodeFetchBlocked},
		{"too large", fetch.ErrTooLarge, http.StatusRequestEntityTooLarge, CodeBodyTooLarge},
		{"timeout", fetch.ErrTimeout, http.StatusGatewayTimeout, CodeTimeout},
		{"bad scheme", fetch.ErrBadScheme, http.StatusBadRequest, CodeInvalidValue},
		{"not an image", fetch.ErrNotImage, http.StatusBadRequest, CodeInvalidValue},
		{"unresolved", fetch.ErrUnresolved, http.StatusBadRequest, CodeInvalidValue},
		{"bad status", fetch.ErrBadStatus, http.StatusBadRequest, CodeInvalidValue},
		{"disabled", fetch.ErrDisabled, http.StatusBadRequest, CodeInvalidValue},
		{"unknown", errors.New("something new"), http.StatusBadRequest, CodeInvalidValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Wrapped, as the fetcher returns them.
			err := fmt.Errorf("fetch: %w", tt.err)
			f := logoFetchFault(err, opts, "https://cdn.example/logo.png")

			if f.Status() != tt.wantStatus {
				t.Errorf("status = %d, want %d", f.Status(), tt.wantStatus)
			}
			if f.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", f.Code, tt.wantCode)
			}
			if f.Field != "style.logo" {
				t.Errorf("field = %q, want style.logo", f.Field)
			}
			if f.Message == "" {
				t.Error("message is empty")
			}
			// 504 is the one 5xx allowed here: the upstream host ran out of
			// time, which is genuinely not the caller's fault. Anything else
			// in the 5xx range would be barqr blaming itself for a URL a
			// caller chose.
			if f.Status() >= http.StatusInternalServerError && f.Status() != http.StatusGatewayTimeout {
				t.Errorf("status = %d: a caller's URL must not produce a 5xx of ours",
					f.Status())
			}
		})
	}
}

// TestLogoFetchFaultNeverEchoesTheFetchError is the leak guard: the fetcher's
// messages name the address that was dialled, and none of that may reach the
// client.
func TestLogoFetchFaultNeverEchoesTheFetchError(t *testing.T) {
	t.Parallel()

	const secret = "10.1.2.3"
	opts := fetch.Options{Allowlist: []string{"cdn.example"}, MaxBytes: 1 << 20}

	for _, base := range []error{fetch.ErrPrivateAddress, fetch.ErrBadStatus, fetch.ErrTimeout} {
		err := fmt.Errorf("%w: %s is not publicly routable, and admin.internal answered 200 OK",
			base, secret)
		f := logoFetchFault(err, opts, "https://cdn.example:8443/logo.png?token=hunter2")

		for name, field := range map[string]string{
			"message": f.Message, "hint": f.Hint,
			"expected": f.Expected, "got": f.Got,
		} {
			if strings.Contains(field, secret) {
				t.Errorf("%s %q leaks the resolved address", name, field)
			}
			if strings.Contains(field, "admin.internal") {
				t.Errorf("%s %q leaks what the remote host answered", name, field)
			}
			if strings.Contains(field, "hunter2") {
				t.Errorf("%s %q echoes the caller's query string back", name, field)
			}
		}
		if f.Got != "cdn.example" {
			t.Errorf("got = %q, want the bare host", f.Got)
		}
	}
}

// TestImageDataURIIsDecodableByTheRenderer closes the loop on a successful
// fetch: whatever the fetcher returns has to survive the renderer's own
// decode, or a remote logo would be fetched under every guard and then thrown
// away with an unhelpful error.
func TestImageDataURIIsDecodableByTheRenderer(t *testing.T) {
	t.Parallel()

	img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	img.Set(1, 1, color.NRGBA{R: 0xFF, A: 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() returned error: %v", err)
	}

	uri := imageDataURI("image/png", buf.Bytes())
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Fatalf("imageDataURI() = %.32q..., want a base64 data URI", uri)
	}

	decoded, err := render.ParseLogo(uri)
	if err != nil {
		t.Fatalf("render.ParseLogo() returned error: %v", err)
	}
	if got := decoded.Bounds().Dx(); got != 3 {
		t.Errorf("decoded width = %d, want 3", got)
	}
}

func TestAllowlistSummaryNamesAnEmptyAllowlist(t *testing.T) {
	t.Parallel()

	if got := allowlistSummary(fetch.Options{}); !strings.Contains(got, "none") {
		t.Errorf("allowlistSummary() = %q, want it to say nothing is configured", got)
	}
	if got := allowlistSummary(fetch.Options{Allowlist: []string{"a.example", "b.example"}}); got !=
		"a.example, b.example" {
		t.Errorf("allowlistSummary() = %q, want both hosts", got)
	}
}

func TestHostOfDropsEverythingButTheHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  string
		want string
	}{
		{"https://cdn.example/logo.png", "cdn.example"},
		{"https://cdn.example:8443/logo.png?a=b", "cdn.example"},
		{"https://user:pass@cdn.example/logo.png", "cdn.example"},
		{"https://[::1]:8443/logo.png", "::1"},
		{"not a url at all", ""},
		{"https://%zz/logo.png", ""},
	}

	for _, tt := range tests {
		if got := hostOf(tt.ref); got != tt.want {
			t.Errorf("hostOf(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// inlineLogo is a real one-pixel PNG as a data URI.
//
// It has to be a decodable image rather than a truncated header: the pipeline
// now decodes the resolved logo and puts it on the style, so anything that is
// not an image is refused — which is the point.
func inlineLogo(t *testing.T) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the test logo: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestStyleResolvesTheLogoReference covers the pipeline's one call into this
// file: a style carrying a remote logo is refused before anything is rendered,
// and an inline one is decoded onto the style.
func TestStyleResolvesTheLogoReference(t *testing.T) {
	t.Parallel()

	s := logoServer(t)
	caps := encoder.Capabilities{}

	req := Request{}
	req.Style.Logo = "https://cdn.example/logo.png"
	_, err := s.style(t.Context(), req, caps)
	f := faultFrom(t, err)
	if f.Code != CodeUnsupported || f.Status() != http.StatusNotImplemented {
		t.Fatalf("style() fault = %d/%s, want 501/%s", f.Status(), f.Code, CodeUnsupported)
	}

	req.Style.Logo = inlineLogo(t)
	if _, err := s.style(t.Context(), req, caps); err != nil {
		t.Fatalf("style() with an inline logo returned error: %v", err)
	}
}
