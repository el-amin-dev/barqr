package fetch

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// publicAddr is a documentation address (RFC 5737 TEST-NET-3). Nothing in the
// guards treats it specially, which is the point: it stands in for a real
// public address while never being routable from a test machine.
const publicAddr = "203.0.113.7"

// testHost is the name the httptest certificate is issued for.
const testHost = "example.com"

// harness is a fetch pointed at a local TLS server while every guard believes
// it is talking to a public host: the stub resolver answers with a public
// address, and the stub dialler records what the fetcher asked to connect to
// before quietly connecting to the test server instead.
//
// Recording the dial target is what makes the pin testable — an
// implementation that handed the hostname to the dialler would record
// "example.com:443" here.
type harness struct {
	opts Options

	lookups atomic.Int64
	dials   atomic.Int64
	target  atomic.Value // string: the address the fetcher chose to dial
}

// newHarness wires a fetch to ts, resolving testHost to answers.
func newHarness(t *testing.T, ts *httptest.Server, answers ...string) *harness {
	t.Helper()

	addrs := make([]netip.Addr, 0, len(answers))
	for _, a := range answers {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			t.Fatalf("netip.ParseAddr(%q) returned error: %v", a, err)
		}
		addrs = append(addrs, addr)
	}

	h := &harness{}
	h.opts = Options{
		Allowlist: []string{testHost},
		Timeout:   3 * time.Second,
		MaxBytes:  1 << 20,
		resolve: func(context.Context, string) ([]netip.Addr, error) {
			n := h.lookups.Add(1)
			// A rebinding attempt: every answer after the first is the
			// loopback. A fetcher that resolves twice ends up here.
			if n > 1 {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return addrs, nil
		},
	}

	if ts != nil {
		h.opts.tlsConfig = tlsConfigOf(t, ts)
		h.opts.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			h.dials.Add(1)
			h.target.Store(addr)
			var d net.Dialer
			return d.DialContext(ctx, network, ts.Listener.Addr().String())
		}
	}
	return h
}

// dialled reports the address the fetcher asked the dialler for.
func (h *harness) dialled() string {
	s, _ := h.target.Load().(string)
	return s
}

// tlsConfigOf lends a test the roots httptest signed its certificate with, so
// the fetch performs a real, verified TLS handshake.
func tlsConfigOf(t *testing.T, ts *httptest.Server) *tls.Config {
	t.Helper()

	tr, ok := ts.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest client transport is %T, want *http.Transport", ts.Client().Transport)
	}
	return tr.TLSClientConfig.Clone()
}

// tlsServer starts an HTTPS test server that is closed with the test.
func tlsServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// pngBytes is a valid image of the requested size, the happy-path body.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: 0x30, G: 0x60, B: 0x90, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() returned error: %v", err)
	}
	return buf.Bytes()
}

// serveImage answers every request with the same PNG.
func serveImage(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}
}

func TestFetchReturnsTheImageAndItsSniffedType(t *testing.T) {
	t.Parallel()

	want := pngBytes(t, 8, 8)
	ts := tlsServer(t, serveImage(want))
	h := newHarness(t, ts, publicAddr)

	got, mediaType, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if err != nil {
		t.Fatalf("Fetch() returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch() returned %d bytes, want the %d served", len(got), len(want))
	}
	if mediaType != "image/png" {
		t.Errorf("media type = %q, want image/png", mediaType)
	}
}

// TestFetchDialsTheAddressItChecked is the DNS-rebinding guard: the fetcher
// must connect to the address it vetted, not re-resolve the name. The stub
// resolver hands back the loopback on any second lookup, so a fetcher that
// re-resolves both dials the wrong address and asks for a name here.
func TestFetchDialsTheAddressItChecked(t *testing.T) {
	t.Parallel()

	ts := tlsServer(t, serveImage(pngBytes(t, 4, 4)))
	h := newHarness(t, ts, publicAddr)

	if _, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts); err != nil {
		t.Fatalf("Fetch() returned error: %v", err)
	}

	if got, want := h.dialled(), publicAddr+":443"; got != want {
		t.Errorf("dialled %q, want %q — the checked address must be dialled, not the name", got, want)
	}
	if got := h.lookups.Load(); got != 1 {
		t.Errorf("resolver called %d times, want exactly 1: a second lookup reopens the rebinding window", got)
	}
	if got := h.dials.Load(); got != 1 {
		t.Errorf("dialled %d times, want 1", got)
	}
}

// TestFetchKeepsTheURLPort proves the pin replaces the host and nothing else.
func TestFetchKeepsTheURLPort(t *testing.T) {
	t.Parallel()

	ts := tlsServer(t, serveImage(pngBytes(t, 4, 4)))
	h := newHarness(t, ts, publicAddr)

	if _, _, err := Fetch(t.Context(), "https://"+testHost+":8443/logo.png", h.opts); err != nil {
		t.Fatalf("Fetch() returned error: %v", err)
	}
	if got, want := h.dialled(), publicAddr+":8443"; got != want {
		t.Errorf("dialled %q, want %q", got, want)
	}
}

func TestFetchRejectsNonHTTPSSchemes(t *testing.T) {
	t.Parallel()

	urls := []string{
		"http://" + testHost + "/logo.png",
		"file:///etc/passwd",
		"gopher://" + testHost + ":70/1",
		"ftp://" + testHost + "/logo.png",
		"HTTP://" + testHost + "/logo.png",
		"//" + testHost + "/logo.png",
		"logo.png",
		"data:image/png;base64,AAAA",
		"https://%zz/logo.png",
	}

	for _, raw := range urls {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, nil, publicAddr)
			_, _, err := Fetch(t.Context(), raw, h.opts)
			if !errors.Is(err, ErrBadScheme) {
				t.Fatalf("Fetch(%q) error = %v, want ErrBadScheme", raw, err)
			}
			if h.lookups.Load() != 0 {
				t.Error("the scheme check must refuse before anything is resolved")
			}
		})
	}
}

// TestFetchEmptyAllowlistFetchesNothing pins the rule that empty means
// nothing: the opposite reading turns the default configuration into an open
// proxy.
func TestFetchEmptyAllowlistFetchesNothing(t *testing.T) {
	t.Parallel()

	for _, list := range [][]string{nil, {}, {""}, {"   "}} {
		h := newHarness(t, nil, publicAddr)
		h.opts.Allowlist = list

		_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Fetch() with allowlist %q error = %v, want ErrNotAllowed", list, err)
		}
		if h.lookups.Load() != 0 {
			t.Error("the allowlist check must refuse before anything is resolved")
		}
	}
}

func TestFetchAllowlistMatchesTheWholeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowlist []string
		url       string
		want      bool
	}{
		{"exact", []string{"cdn.example"}, "https://cdn.example/a.png", true},
		{"host case ignored", []string{"cdn.example"}, "https://CDN.Example/a.png", true},
		{"allowlist case ignored", []string{"CDN.EXAMPLE"}, "https://cdn.example/a.png", true},
		{"entry padding ignored", []string{" cdn.example "}, "https://cdn.example/a.png", true},
		{"port ignored", []string{"cdn.example"}, "https://cdn.example:8443/a.png", true},
		{"root label ignored", []string{"cdn.example"}, "https://cdn.example./a.png", true},
		{"userinfo is not the host", []string{"cdn.example"},
			"https://cdn.example@evil.test/a.png", false},
		{"suffix is not a match", []string{"cdn.example"}, "https://evil-cdn.example/a.png", false},
		{"subdomain is not a match", []string{"cdn.example"}, "https://a.cdn.example/a.png", false},
		{"parent is not a match", []string{"a.cdn.example"}, "https://cdn.example/a.png", false},
		{"prefix is not a match", []string{"cdn.example"}, "https://cdn.example.evil.test/a.png", false},
		{"second entry matches", []string{"a.example", "cdn.example"},
			"https://cdn.example/a.png", true},
		{"no host at all", []string{"cdn.example"}, "https:///a.png", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, nil, publicAddr)
			h.opts.Allowlist = tt.allowlist
			// Resolution fails, so an allowed host stops at the next guard and
			// a refused one never reaches it. Either way nothing is dialled.
			h.opts.resolve = func(context.Context, string) ([]netip.Addr, error) {
				return nil, errors.New("no such host")
			}

			_, _, err := Fetch(t.Context(), tt.url, h.opts)
			allowed := !errors.Is(err, ErrNotAllowed)
			if allowed != tt.want {
				t.Errorf("Fetch(%q) allowed = %t (err %v), want %t", tt.url, allowed, err, tt.want)
			}
		})
	}
}

func TestFetchRejectsNonPublicResolvedAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
	}{
		{"loopback v4", "127.0.0.1"},
		{"loopback v4 in another octet", "127.99.12.5"},
		{"loopback v6", "::1"},
		{"unspecified v4", "0.0.0.0"},
		{"unspecified v6", "::"},
		{"rfc1918 ten", "10.1.2.3"},
		{"rfc1918 172", "172.16.9.9"},
		{"rfc1918 192.168", "192.168.1.1"},
		{"link-local, the cloud metadata endpoint", "169.254.169.254"},
		{"link-local v6", "fe80::1"},
		{"unique local v6", "fd00::1"},
		{"multicast v4", "224.0.0.1"},
		{"multicast v6", "ff02::1"},
		{"carrier-grade nat", "100.64.0.1"},
		{"benchmarking", "198.18.0.1"},
		{"reserved", "240.0.0.1"},
		{"broadcast", "255.255.255.255"},
		{"protocol assignments", "192.0.0.1"},
		{"nat64", "64:ff9b::7f00:1"},
		{"6to4", "2002::1"},
		{"v4-mapped loopback", "::ffff:127.0.0.1"},
		{"v4-mapped rfc1918", "::ffff:10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, nil, tt.addr)
			_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
			if !errors.Is(err, ErrPrivateAddress) {
				t.Fatalf("Fetch() with %s error = %v, want ErrPrivateAddress", tt.addr, err)
			}
		})
	}
}

// TestFetchRejectsAMixedAnswer covers the split-horizon trick: one good
// address to pass the check, one bad one to be picked up by whatever resolves
// next.
func TestFetchRejectsAMixedAnswer(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, publicAddr, "10.0.0.5")
	if _, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("Fetch() error = %v, want ErrPrivateAddress", err)
	}
}

// TestFetchChecksAddressLiterals covers a URL that skips DNS entirely.
func TestFetchChecksAddressLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		host    string
		wantErr error
	}{
		{"loopback literal", "127.0.0.1", ErrPrivateAddress},
		{"metadata literal", "169.254.169.254", ErrPrivateAddress},
		{"v6 loopback literal", "[::1]", ErrPrivateAddress},
		{"v4-mapped literal", "[::ffff:127.0.0.1]", ErrPrivateAddress},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := strings.Trim(tt.host, "[]")
			h := newHarness(t, nil, publicAddr)
			h.opts.Allowlist = []string{host}

			_, _, err := Fetch(t.Context(), "https://"+tt.host+"/logo.png", h.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Fetch() error = %v, want %v", err, tt.wantErr)
			}
			if h.lookups.Load() != 0 {
				t.Error("an address literal must not be resolved")
			}
		})
	}
}

// TestFetchAcceptsAPublicAddressLiteral is the other half: a literal that
// passes the check is dialled as given.
func TestFetchAcceptsAPublicAddressLiteral(t *testing.T) {
	t.Parallel()

	ts := tlsServer(t, serveImage(pngBytes(t, 4, 4)))
	h := newHarness(t, ts, publicAddr)
	h.opts.Allowlist = []string{"93.184.216.34"}
	// The certificate is issued for the name, not this address, so the
	// handshake is expected to fail; the dial target is what is under test.
	_, _, _ = Fetch(t.Context(), "https://93.184.216.34/logo.png", h.opts)

	if got, want := h.dialled(), "93.184.216.34:443"; got != want {
		t.Errorf("dialled %q, want %q", got, want)
	}
	if h.lookups.Load() != 0 {
		t.Error("an address literal must not be resolved")
	}
}

// TestFetchRejectsAnInvalidResolvedAddress covers a resolver answering with
// something that is not an address at all.
func TestFetchRejectsAnInvalidResolvedAddress(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, publicAddr)
	h.opts.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{{}}, nil
	}
	if _, _, err := Fetch(t.Context(), "https://"+testHost+"/x.png", h.opts); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("Fetch() error = %v, want ErrPrivateAddress", err)
	}
}

// TestFetchResolvesWithTheRealResolver exercises the production resolution
// path — no stub — against a name every machine resolves from its hosts file.
// It resolves to the loopback, so the address check is what refuses it.
func TestFetchResolvesWithTheRealResolver(t *testing.T) {
	t.Parallel()

	opts := Options{Allowlist: []string{"localhost"}, Timeout: 2 * time.Second, MaxBytes: 1 << 20}
	_, _, err := Fetch(t.Context(), "https://localhost/logo.png", opts)
	if !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("Fetch() error = %v, want ErrPrivateAddress", err)
	}
}

// TestFetchDialsForRealWhenNothingIsInjected exercises the production
// transport: a real dial to an address that answers nothing. Whether that ends
// in a timeout or an unreachable network depends on where the test runs, and
// either is a refusal — what matters is that it is never a success.
func TestFetchDialsForRealWhenNothingIsInjected(t *testing.T) {
	t.Parallel()

	opts := Options{
		Allowlist: []string{publicAddr},
		Timeout:   250 * time.Millisecond,
		MaxBytes:  1 << 20,
	}
	body, _, err := Fetch(t.Context(), "https://"+publicAddr+"/logo.png", opts)
	if err == nil {
		t.Fatalf("Fetch() returned %d bytes and no error from an address that answers nothing",
			len(body))
	}
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrBadStatus) {
		t.Fatalf("Fetch() error = %v, want ErrTimeout or ErrBadStatus", err)
	}
}

func TestFetchRejectsAnUnresolvableHost(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, publicAddr)
	h.opts.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("no such host")
	}
	if _, _, err := Fetch(t.Context(), "https://"+testHost+"/x.png", h.opts); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Fetch() error = %v, want ErrUnresolved", err)
	}

	h = newHarness(t, nil, publicAddr)
	h.opts.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }
	if _, _, err := Fetch(t.Context(), "https://"+testHost+"/x.png", h.opts); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Fetch() with an empty answer error = %v, want ErrUnresolved", err)
	}
}

// TestFetchRefusesRedirects asserts both halves of the rule: the 3xx is an
// error, and the target of the redirect is never requested.
func TestFetchRefusesRedirects(t *testing.T) {
	t.Parallel()

	var followed atomic.Int64
	ts := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			followed.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes(t, 2, 2))
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	})

	h := newHarness(t, ts, publicAddr)
	_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if !errors.Is(err, ErrBadStatus) {
		t.Fatalf("Fetch() error = %v, want ErrBadStatus", err)
	}
	if !strings.Contains(err.Error(), "302") {
		t.Errorf("error %q should name the status", err)
	}
	if got := followed.Load(); got != 0 {
		t.Errorf("the redirect target was requested %d times, want 0", got)
	}
}

func TestFetchRejectsNon2xxStatuses(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusBadRequest, http.StatusForbidden,
		http.StatusNotFound, http.StatusInternalServerError, http.StatusTeapot} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()

			ts := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			})
			h := newHarness(t, ts, publicAddr)

			_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
			if !errors.Is(err, ErrBadStatus) {
				t.Fatalf("Fetch() error = %v, want ErrBadStatus", err)
			}
			if !strings.Contains(err.Error(), http.StatusText(code)) {
				t.Errorf("error %q should name the status", err)
			}
		})
	}
}

// TestFetchRejectsADeclaredOversizeBody covers the cheap check: an honest
// Content-Length over the cap is refused before the body is read.
func TestFetchRejectsADeclaredOversizeBody(t *testing.T) {
	t.Parallel()

	body := pngBytes(t, 32, 32)
	ts := tlsServer(t, serveImage(body))
	h := newHarness(t, ts, publicAddr)
	h.opts.MaxBytes = int64(len(body)) - 1

	_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch() error = %v, want ErrTooLarge", err)
	}
	if !strings.Contains(err.Error(), "content-length") {
		t.Errorf("error %q should name the declared length", err)
	}
}

// TestFetchRejectsAnUndeclaredOversizeBody covers the other half: a chunked
// response declares no length at all, so only the limited read catches it.
func TestFetchRejectsAnUndeclaredOversizeBody(t *testing.T) {
	t.Parallel()

	body := pngBytes(t, 64, 64)
	ts := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// Flushing before the body forces chunked encoding, which is what
		// removes Content-Length from the response.
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write(body)
	})

	h := newHarness(t, ts, publicAddr)
	h.opts.MaxBytes = 16

	_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch() error = %v, want ErrTooLarge", err)
	}
	if strings.Contains(err.Error(), "content-length") {
		t.Errorf("error %q claims a declared length the response never carried", err)
	}
}

// TestFetchAcceptsABodyExactlyAtTheCap guards the off-by-one in the limited
// read: the spare byte exists to detect an overrun, not to reject the cap.
func TestFetchAcceptsABodyExactlyAtTheCap(t *testing.T) {
	t.Parallel()

	body := pngBytes(t, 6, 6)
	ts := tlsServer(t, serveImage(body))
	h := newHarness(t, ts, publicAddr)
	h.opts.MaxBytes = int64(len(body))

	got, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if err != nil {
		t.Fatalf("Fetch() returned error: %v", err)
	}
	if len(got) != len(body) {
		t.Errorf("got %d bytes, want %d", len(got), len(body))
	}
}

func TestFetchRejectsANonImageBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{"html labelled as png", "<!DOCTYPE html><html><body>hello</body></html>"},
		{"plain text", "not an image, whatever the header says"},
		{"empty body", ""},
		{"svg, which no raster decoder here accepts", `<svg xmlns="http://www.w3.org/2000/svg"/>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
				// The header lies on purpose: the sniff must win.
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte(tt.body))
			})
			h := newHarness(t, ts, publicAddr)

			_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
			if !errors.Is(err, ErrNotImage) {
				t.Fatalf("Fetch() error = %v, want ErrNotImage", err)
			}
		})
	}
}

func TestFetchTimesOut(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	ts := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})

	h := newHarness(t, ts, publicAddr)
	h.opts.Timeout = 100 * time.Millisecond

	_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Fetch() error = %v, want ErrTimeout", err)
	}
}

// TestFetchHonoursACancelledContext proves the caller's context bounds the
// fetch as well as the configured timeout.
func TestFetchHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	ts := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	h := newHarness(t, ts, publicAddr)
	if _, _, err := Fetch(ctx, "https://"+testHost+"/logo.png", h.opts); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Fetch() error = %v, want ErrTimeout", err)
	}
}

// TestFetchReportsAnUnreachableHost covers a dial that fails outright: the
// error must be a sentinel and must not carry the address that was dialled.
func TestFetchReportsAnUnreachableHost(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, publicAddr)
	h.opts.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	h.opts.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: errors.New("connect: connection refused"),
		}
	}

	_, _, err := Fetch(t.Context(), "https://"+testHost+"/logo.png", h.opts)
	if !errors.Is(err, ErrBadStatus) {
		t.Fatalf("Fetch() error = %v, want ErrBadStatus", err)
	}
	if strings.Contains(err.Error(), publicAddr) {
		t.Errorf("error %q leaks the address that was dialled", err)
	}
}

func TestFetchRefusesWithoutBudgets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts Options
	}{
		{"no timeout", Options{Allowlist: []string{testHost}, MaxBytes: 1 << 20}},
		{"no byte cap", Options{Allowlist: []string{testHost}, Timeout: time.Second}},
		{"negative byte cap", Options{Allowlist: []string{testHost}, Timeout: time.Second, MaxBytes: -1}},
		{"nothing at all", Options{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := Fetch(t.Context(), "https://"+testHost+"/x.png", tt.opts); !errors.Is(err, ErrDisabled) {
				t.Fatalf("Fetch() error = %v, want ErrDisabled", err)
			}
		})
	}
}

func TestEnabled(t *testing.T) {
	t.Parallel()

	full := Options{Allowlist: []string{"cdn.example"}, Timeout: time.Second, MaxBytes: 1 << 20}

	tests := []struct {
		name string
		opts Options
		want bool
	}{
		{"fully configured", full, true},
		{"zero value", Options{}, false},
		{"no allowlist", Options{Timeout: time.Second, MaxBytes: 1 << 20}, false},
		{"blank allowlist entries only",
			Options{Allowlist: []string{"", "  "}, Timeout: time.Second, MaxBytes: 1 << 20}, false},
		{"no timeout", Options{Allowlist: []string{"cdn.example"}, MaxBytes: 1 << 20}, false},
		{"no byte cap", Options{Allowlist: []string{"cdn.example"}, Timeout: time.Second}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Enabled(tt.opts); got != tt.want {
				t.Errorf("Enabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestErrorsAreDistinct guards the sentinel set: mapping onto HTTP statuses
// only works while each error is its own value.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{ErrDisabled, ErrNotAllowed, ErrPrivateAddress, ErrUnresolved,
		ErrTooLarge, ErrBadStatus, ErrBadScheme, ErrTimeout, ErrNotImage}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v and %v are the same error", a, b)
			}
		}
	}
}
