// Package fetch retrieves a remote image on behalf of a request, under the
// server-side request forgery guards docs/SECURITY.md Layer 4 promises.
//
// style.logo may name a URL, and a service that dereferences a URL chosen by
// an untrusted caller is a request-forgery primitive: the caller picks the
// destination, and the server brings a routing table, a place inside the
// network perimeter, and often an identity the caller does not have. The
// guards are therefore deny-by-default and layered:
//
//   - https only, so no caller can reach file:, gopher:, or a plaintext port;
//   - an exact host allowlist, empty by default, so nothing is reachable at
//     all until an operator names it;
//   - resolution and address vetting done here, in front of the dialler, and
//     a dial to the vetted address rather than to the name, so DNS cannot
//     change between the check and the connection;
//   - no redirects, because following one means re-running every check above
//     on the new target and refusing is far easier to get right;
//   - caps on time and on bytes, the byte cap checked twice, because a host
//     that answers slowly or endlessly is a denial-of-service amplifier.
//
// Errors from this package never carry the underlying network error. A dial
// failure names the address it dialled, and that address is precisely what
// these guards exist to keep from the caller.
package fetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// Sentinel errors. The HTTP layer maps these onto stable error codes, so a
// reworded message can never change what a client sees.
var (
	// ErrDisabled means the options carry no time or byte budget, which is
	// what an unconfigured fetcher looks like.
	ErrDisabled = errors.New("remote fetching is not configured")
	// ErrNotAllowed means the host is not on the allowlist. An empty
	// allowlist puts every host in this state.
	ErrNotAllowed = errors.New("host is not on the fetch allowlist")
	// ErrPrivateAddress means the host resolved to an address that is not
	// publicly routable — loopback, RFC 1918, link-local, and the rest.
	ErrPrivateAddress = errors.New("host resolves to a non-public address")
	// ErrUnresolved means the host has no address at all.
	ErrUnresolved = errors.New("host could not be resolved")
	// ErrTooLarge means the response is bigger than MaxBytes.
	ErrTooLarge = errors.New("remote image exceeds the size cap")
	// ErrBadStatus means the host answered with a non-2xx status, refused a
	// redirect, or could not be reached at all.
	ErrBadStatus = errors.New("remote image could not be retrieved")
	// ErrBadScheme means the URL is not an https URL.
	ErrBadScheme = errors.New("url scheme is not https")
	// ErrTimeout means the fetch did not finish inside Timeout.
	ErrTimeout = errors.New("remote image fetch timed out")
	// ErrNotImage means the body is not an image, whatever the host claimed
	// in its Content-Type.
	ErrNotImage = errors.New("remote resource is not an image")
)

// schemeHTTPS is the only scheme a fetch may use.
const schemeHTTPS = "https"

// userAgent identifies barqr to the host being fetched from. It carries no
// version: the string reaches third parties, and a version is a free hint
// about what to try against this deployment.
const userAgent = "barqr"

// Options bounds a fetch.
//
// The zero value fetches nothing, which is the intended default: an empty
// allowlist means no host is reachable, and a zero timeout or byte cap means
// the feature was never configured.
type Options struct {
	// Allowlist is the set of hosts that may be fetched from. Matching is
	// exact on the whole host, case-insensitive, and ignores the port.
	Allowlist []string
	// Timeout bounds the entire operation: resolution, connection, and body.
	Timeout time.Duration
	// MaxBytes caps the response body.
	MaxBytes int64

	// The hooks below exist so the guards can be tested for real rather than
	// read for plausibility — a loopback test server is unreachable through
	// the address check by design. They are unexported, so no caller outside
	// this package can configure a guard away.

	// resolve replaces DNS resolution.
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// dial replaces the pinned dial. It is handed the address the fetcher
	// decided to connect to, which is what makes the pin observable.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// tlsConfig supplies the roots a test server's certificate is signed by.
	tlsConfig *tls.Config
}

// Enabled reports whether o could fetch anything at all, so that a caller can
// tell "switched off" from "switched on and misconfigured" and say so.
func Enabled(o Options) bool {
	if o.Timeout <= 0 || o.MaxBytes <= 0 {
		return false
	}
	for _, entry := range o.Allowlist {
		if normaliseHost(entry) != "" {
			return true
		}
	}
	return false
}

// Fetch retrieves a remote image under the SSRF guards.
//
// It returns the body and the media type sniffed from that body — never the
// type the host declared. The caller gets bytes it can decode or an error it
// can map; it never gets the address that was dialled.
func Fetch(ctx context.Context, rawURL string, o Options) ([]byte, string, error) {
	if o.Timeout <= 0 || o.MaxBytes <= 0 {
		return nil, "", fmt.Errorf("%w: a timeout and a byte cap are both required", ErrDisabled)
	}

	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, "", fmt.Errorf("%w: the url could not be parsed", ErrBadScheme)
	}
	if !strings.EqualFold(u.Scheme, schemeHTTPS) {
		return nil, "", fmt.Errorf("%w: got %q, want https", ErrBadScheme, u.Scheme)
	}

	host := normaliseHost(u.Hostname())
	if !o.allows(host) {
		return nil, "", fmt.Errorf("%w: %q is not on the allowlist", ErrNotAllowed, host)
	}

	// One deadline over the whole operation. Resolution, handshake, and body
	// read all draw on the same budget, because a host that stalls in any one
	// of the three holds the same request slot for the same length of time.
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	pinned, err := o.pin(ctx, host)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: the url is not requestable", ErrBadScheme)
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", userAgent)

	client, closeIdle := o.client(pinned)
	defer closeIdle()

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		return nil, "", statusError(resp.StatusCode, resp.Status)
	}

	// The declared length is checked first so an oversized body costs one
	// header read rather than MaxBytes of transfer.
	if resp.ContentLength > o.MaxBytes {
		return nil, "", fmt.Errorf("%w: content-length %d exceeds the %d byte cap",
			ErrTooLarge, resp.ContentLength, o.MaxBytes)
	}

	// Then read one byte past the cap, because Content-Length is the host's
	// claim: it may be absent, chunked, or simply a lie. Reading the spare
	// byte is what distinguishes "exactly at the cap" from "truncated".
	body, err := io.ReadAll(io.LimitReader(resp.Body, o.MaxBytes+1))
	if err != nil {
		return nil, "", transportError(err)
	}
	if int64(len(body)) > o.MaxBytes {
		return nil, "", fmt.Errorf("%w: body exceeds the %d byte cap", ErrTooLarge, o.MaxBytes)
	}

	// Sniff rather than trust. Content-Type is another claim by the same
	// untrusted host, and the point of this check is that a caller must not
	// be able to make barqr relay arbitrary bytes by labelling them.
	mediaType := http.DetectContentType(body)
	if !strings.HasPrefix(mediaType, "image/") {
		return nil, "", fmt.Errorf("%w: the body sniffs as %s, want image/*",
			ErrNotImage, mediaType)
	}

	return body, mediaType, nil
}

// allows reports whether host is on the allowlist.
//
// The comparison is exact on the whole host. Suffix matching is the classic
// allowlist bug: "cdn.example" written as a suffix rule also admits
// "evil-cdn.example", a name the attacker registers in an afternoon.
func (o Options) allows(host string) bool {
	if host == "" {
		return false
	}
	for _, entry := range o.Allowlist {
		if e := normaliseHost(entry); e != "" && e == host {
			return true
		}
	}
	return false
}

// normaliseHost lowercases a host and drops the root label.
//
// "cdn.example." is the same name as "cdn.example" with the root written out;
// refusing it would be a false negative that teaches nobody anything, and it
// costs no safety because the address check runs either way.
func normaliseHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// pin resolves host and returns the one address the fetch will dial.
//
// This is the guard that matters most. Resolving here, checking here, and
// then dialling the checked address closes the DNS-rebinding window: an
// implementation that checks a name and then hands the same name to the
// dialler invites a second lookup, and the second answer is the attacker's.
func (o Options) pin(ctx context.Context, host string) (netip.Addr, error) {
	// A URL may carry an address literal, in which case there is nothing to
	// resolve — but there is still everything to check.
	if lit, err := netip.ParseAddr(host); err == nil {
		if err := checkAddr(lit); err != nil {
			return netip.Addr{}, err
		}
		return lit.Unmap(), nil
	}

	addrs, err := o.lookup(ctx, host)
	if err != nil {
		if isTimeout(err) {
			return netip.Addr{}, fmt.Errorf("%w: %q did not resolve in time", ErrTimeout, host)
		}
		return netip.Addr{}, fmt.Errorf("%w: %q has no address", ErrUnresolved, host)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("%w: %q has no address", ErrUnresolved, host)
	}

	// Every answer is checked, not only the one that will be dialled. A name
	// that answers with a public address and a private one is misconfigured
	// or hostile, and there is no reading of a mixed answer under which the
	// private half is safe to have been offered.
	for _, a := range addrs {
		if err := checkAddr(a); err != nil {
			return netip.Addr{}, err
		}
	}
	return addrs[0].Unmap(), nil
}

// lookup resolves host, through the stub resolver when a test injected one.
func (o Options) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if o.resolve != nil {
		return o.resolve(ctx, host)
	}
	var r net.Resolver
	return r.LookupNetIP(ctx, "ip", host)
}

// blockedPrefixes are ranges netip's own classifiers do not cover and that no
// public image host lives in. Each is a route to somewhere a caller has no
// business reaching through barqr.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // RFC 6598 carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // RFC 6890 protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // RFC 2544 benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // RFC 1112 reserved, broadcast included
	netip.MustParsePrefix("64:ff9b::/96"),  // RFC 6052 NAT64: an IPv4 address in disguise
	netip.MustParsePrefix("2002::/16"),     // RFC 3056 6to4: likewise
}

// checkAddr rejects any address that is not publicly routable.
func checkAddr(a netip.Addr) error {
	// An IPv4-mapped IPv6 address is an IPv4 address wearing a hat:
	// ::ffff:127.0.0.1 dials the loopback, yet IsLoopback on the 16-byte form
	// answers false. Unmapping first is what makes every classifier below
	// see the address that will actually be dialled.
	a = a.Unmap()

	if !a.IsValid() {
		return fmt.Errorf("%w: the resolved address is not an ip address", ErrPrivateAddress)
	}

	switch {
	case a.IsUnspecified(), a.IsLoopback(), a.IsPrivate(),
		a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(),
		a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: %s is not publicly routable", ErrPrivateAddress, a)
	}

	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return fmt.Errorf("%w: %s is in the reserved range %s", ErrPrivateAddress, a, p)
		}
	}
	return nil
}

// client builds the one-shot client for a fetch and a function that releases
// its connections.
func (o Options) client(pinned netip.Addr) (*http.Client, func()) {
	tlsCfg := o.tlsConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	tr := &http.Transport{
		// No proxy, ever — not even from the environment. A proxy is handed
		// the hostname and dialled in place of the address just vetted,
		// which voids the pin outright.
		Proxy:                 nil,
		DialContext:           o.dialPinned(pinned),
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   o.Timeout,
		ResponseHeaderTimeout: o.Timeout,
		// One request per connection. Pooling would keep a socket to an
		// address that has to be re-vetted before the next fetch anyway.
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: tr,
		Timeout:   o.Timeout,
		// Refuse every redirect. Following one means re-running the scheme
		// check, the allowlist check, and the address check against a target
		// the first host chose — and a 302 to 169.254.169.254 is the oldest
		// trick in this file. ErrUseLastResponse hands the 3xx back instead,
		// which the status check below turns into an error.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, tr.CloseIdleConnections
}

// dialPinned returns a dialler that ignores the name in the address and
// connects to the address pin already checked, keeping the port the URL asked
// for.
func (o Options) dialPinned(pinned netip.Addr) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: the url has no usable port", ErrBadScheme)
		}
		target := net.JoinHostPort(pinned.String(), port)

		if o.dial != nil {
			return o.dial(ctx, network, target)
		}
		d := net.Dialer{Timeout: o.Timeout}
		return d.DialContext(ctx, network, target)
	}
}

// statusError describes a non-2xx answer.
func statusError(code int, status string) error {
	if code >= http.StatusMultipleChoices && code < http.StatusBadRequest {
		return fmt.Errorf("%w: the host answered %s and redirects are not followed",
			ErrBadStatus, status)
	}
	return fmt.Errorf("%w: the host answered %s", ErrBadStatus, status)
}

// transportError maps a transport failure onto a sentinel.
//
// The original error is deliberately dropped rather than wrapped: its text
// names the address that was dialled, and that address is the one thing this
// package must not hand back up the stack.
func transportError(err error) error {
	if isTimeout(err) {
		return fmt.Errorf("%w: no response within the fetch timeout", ErrTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: the request ended before the fetch finished", ErrTimeout)
	}
	return fmt.Errorf("%w: the host could not be reached", ErrBadStatus)
}

// isTimeout reports whether err is a deadline of any of the several shapes
// the net and http packages produce for one.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
