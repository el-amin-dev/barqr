package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/el-amin-dev/barqr/internal/fetch"
)

// Error codes for the remote-fetch guards. Both are refusals of policy rather
// than of syntax — the request is well formed and barqr will not make it — so
// neither reuses a code that means "you typed it wrong".
const (
	// CodeFetchNotAllowed means the host is not on BARQR_FETCH_ALLOWLIST, or
	// remote fetching is on but nothing was ever allowlisted.
	CodeFetchNotAllowed = "FETCH_NOT_ALLOWED"
	// CodeFetchBlocked means the host resolved to an address barqr will not
	// connect to.
	CodeFetchBlocked = "FETCH_BLOCKED"
)

// dataURIPrefix is what an inline image starts with.
const dataURIPrefix = "data:"

// resolveLogo turns a style.logo reference into a data: URI.
//
// A data: URI is returned untouched. A remote reference is fetched under the
// guards in internal/fetch and returned as a data: URI too, so that everything
// downstream sees exactly one representation of "an image" whether it arrived
// inline, as an upload, or from a host across the internet.
//
// The error is always a *Fault: a caller has to be able to tell "that host is
// not allowlisted" from "that host is unreachable" from "remote fetching is
// off here", and each of those is a different status.
func (s *Server) resolveLogo(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return "", nil
	case strings.HasPrefix(strings.ToLower(ref), dataURIPrefix):
		return ref, nil
	case !s.cfg.AllowRemoteFetch:
		return "", remoteLogoDisabledFault()
	}

	opts := s.fetchOptions()

	// Enabled separates "off" from "on and misconfigured". Without this the
	// operator who sets BARQR_ALLOW_REMOTE_FETCH=true and forgets the
	// allowlist gets the same refusal as one who never enabled it, and spends
	// the afternoon looking in the wrong place.
	if !fetch.Enabled(opts) {
		f := newFault(http.StatusForbidden, CodeFetchNotAllowed,
			"remote logo fetching is enabled but no host is allowed to be fetched from")
		f.Field = "style.logo"
		f.Expected = "a data: URI"
		f.Hint = "ask the operator to set BARQR_FETCH_ALLOWLIST, " +
			"BARQR_FETCH_TIMEOUT and BARQR_FETCH_MAX_BYTES"
		return "", f
	}

	data, mediaType, err := fetch.Fetch(ctx, ref, opts)
	if err != nil {
		// The detail — which address was refused, which status came back —
		// goes to the log, where an operator can correlate it by request id.
		// It never goes to the client: the resolved address is exactly what
		// an SSRF probe is fishing for, and a refusal that names it answers
		// the question anyway.
		s.log.WarnContext(ctx, "remote logo refused",
			slog.String("detail", err.Error()),
			slog.String("request_id", requestIDFrom(ctx)))
		return "", logoFetchFault(err, opts, ref)
	}

	return imageDataURI(mediaType, data), nil
}

// imageDataURI wraps fetched bytes in the representation the rest of the
// pipeline understands.
//
// The media type is the one sniffed from the body, never the one the remote
// host declared, so the URI cannot claim to be an image the bytes are not.
func imageDataURI(mediaType string, data []byte) string {
	return dataURIPrefix + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// fetchOptions is the fetcher's view of the configuration.
func (s *Server) fetchOptions() fetch.Options {
	return fetch.Options{
		Allowlist: s.cfg.FetchAllowlist,
		Timeout:   s.cfg.FetchTimeout,
		MaxBytes:  s.cfg.FetchMaxBytes,
	}
}

// remoteLogoDisabledFault is the answer when BARQR_ALLOW_REMOTE_FETCH is off.
//
// It is a 501 rather than a 400 because the request is perfectly valid and
// another deployment of the same version would serve it: the capability is
// absent here, which is what "not implemented" means on the wire.
func remoteLogoDisabledFault() *Fault {
	f := newFault(http.StatusNotImplemented, CodeUnsupported,
		"style.logo must be a data: URI in this build")
	f.Field = "style.logo"
	f.Got = "a remote reference"
	f.Expected = "data:image/png;base64,..."
	f.Hint = "inline the image as a data URI, or upload it as a multipart " +
		"field named style.logo; remote fetching is off (BARQR_ALLOW_REMOTE_FETCH=false)"
	return f
}

// logoFetchFault maps a fetch sentinel onto the wire shape.
//
// No branch interpolates the fetch error itself. Those messages name the
// address that was dialled and the status the host returned, and both are
// answers to questions a caller pointing barqr at an internal network is
// asking. The caller gets the rule that refused them; the log gets the rest.
func logoFetchFault(err error, opts fetch.Options, ref string) *Fault {
	var f *Fault

	switch {
	case errors.Is(err, fetch.ErrNotAllowed):
		f = newFault(http.StatusForbidden, CodeFetchNotAllowed,
			"style.logo names a host barqr is not allowed to fetch from")
		f.Expected = "a host on the fetch allowlist: " + allowlistSummary(opts)
		f.Hint = "use a data: URI, or ask the operator to add the host to " +
			"BARQR_FETCH_ALLOWLIST"

	case errors.Is(err, fetch.ErrPrivateAddress):
		f = newFault(http.StatusForbidden, CodeFetchBlocked,
			"the host in style.logo resolves to a private address")
		f.Expected = "a host on the public internet"
		f.Hint = "barqr never connects to loopback, private, link-local or " +
			"multicast addresses on behalf of a request"

	case errors.Is(err, fetch.ErrTooLarge):
		f = newFault(http.StatusRequestEntityTooLarge, CodeBodyTooLarge,
			"the image at style.logo is larger than the %d byte fetch cap", opts.MaxBytes)
		f.Hint = "serve a smaller image, or ask the operator to raise " +
			"BARQR_FETCH_MAX_BYTES"

	case errors.Is(err, fetch.ErrTimeout):
		f = newFault(http.StatusGatewayTimeout, CodeTimeout,
			"fetching style.logo took longer than %s", opts.Timeout)
		f.Hint = "serve the image from somewhere faster, or ask the operator " +
			"to raise BARQR_FETCH_TIMEOUT"

	case errors.Is(err, fetch.ErrBadScheme):
		f = newFault(http.StatusBadRequest, CodeInvalidValue,
			"style.logo must be an https: URL or a data: URI")
		f.Expected = "https://host/path or data:image/png;base64,..."

	case errors.Is(err, fetch.ErrNotImage):
		f = newFault(http.StatusBadRequest, CodeInvalidValue,
			"the resource at style.logo is not an image")
		f.Expected = "an image/* body, judged by its content and not its Content-Type"

	case errors.Is(err, fetch.ErrUnresolved):
		f = newFault(http.StatusBadRequest, CodeInvalidValue,
			"the host in style.logo could not be resolved")

	default:
		// ErrBadStatus and ErrDisabled land here, along with anything a later
		// version of the fetcher adds: an unmapped sentinel must still produce
		// a refusal a caller can act on, never a 500.
		f = newFault(http.StatusBadRequest, CodeInvalidValue,
			"the image at style.logo could not be fetched")
		f.Hint = "check that the URL serves the image over https without a redirect"
	}

	f.Field = "style.logo"
	if host := hostOf(ref); host != "" {
		// Echoing the host is safe — the caller wrote it — and it is the one
		// piece of context that makes a refusal legible in a batch of items.
		f.Got = host
	}
	return f
}

// allowlistSummary names the allowed hosts for an error message.
func allowlistSummary(opts fetch.Options) string {
	if len(opts.Allowlist) == 0 {
		return "(none configured)"
	}
	return strings.Join(opts.Allowlist, ", ")
}

// hostOf extracts the host from a reference, dropping any userinfo, port,
// path, or query so that a credential in the URL cannot be echoed back.
func hostOf(ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return u.Hostname()
}
