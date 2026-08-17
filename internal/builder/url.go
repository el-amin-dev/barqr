package builder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// URL is the registry name of the URL builder.
const URL = "url"

func init() { Register(urlBuilder{}) }

// URLPayload carries a link.
type URLPayload struct {
	URL      string `json:"url"`
	AllowAny bool   `json:"allow_any"`
}

// urlSchemePrefix matches an RFC 3986 scheme at the start of a string. It is
// how "example.com" is told apart from "https://example.com", which decides
// whether a scheme has to be supplied.
var urlSchemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// urlSafeSchemes are the schemes a scanned code may open without surprising
// the person holding the phone. Anything else needs allow_any, because a code
// that silently opens a "javascript:" or an app's private scheme is a way to
// weaponise a printed sticker.
var urlSafeSchemes = map[string]bool{
	"http": true, "https": true, "mailto": true, "tel": true,
}

// urlBuilder normalises and validates a link.
type urlBuilder struct{}

func (urlBuilder) Name() string { return URL }

func (urlBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "url",
			Type:        TypeString,
			Description: "the link; https:// is added when no scheme is given",
			Required:    true,
			Example:     "https://example.com/menu",
		},
		{
			Name:        "allow_any",
			Type:        TypeBool,
			Description: "accept schemes beyond http, https, mailto and tel",
			Required:    false,
			Example:     "false",
		},
	}
}

func (b urlBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	raw, err := strReq(m, "url")
	if err != nil {
		return "", err
	}
	allowAny, err := boolean(m, "allow_any")
	if err != nil {
		return "", err
	}
	return normaliseURL(raw, allowAny)
}

func (urlBuilder) Parse(raw string) (any, bool) {
	normalised, err := strictURL(raw)
	if err != nil {
		return nil, false
	}
	return map[string]any{"url": normalised}, true
}

// strictURL normalises a link that must already carry its own scheme.
//
// Parsing insists on the scheme even though Build supplies a missing one,
// because otherwise every bare word in the world would parse as a hostname and
// the URL builder would claim strings that belong to the text builder.
func strictURL(raw string) (string, error) {
	if !urlSchemePrefix.MatchString(raw) {
		return "", fmt.Errorf("%w: url %q has no scheme", ErrInvalidPayload, raw)
	}
	return normaliseURL(raw, false)
}

// stableString reports whether a value is already in the normal form a
// builder would produce for it.
//
// Parse uses it on every field that Build normalises. Accepting a value that
// normalises to something else would mean the parsed payload rebuilds to a
// different string, which breaks the one invariant every builder promises.
func stableString(value string, normalise func(string) (string, error)) bool {
	got, err := normalise(value)
	return err == nil && got == value
}

// normaliseURL applies the one normalisation both Build and Parse need, so
// that parsing a built string yields a payload that rebuilds byte-identically.
//
// It adds a missing scheme, lower-cases scheme and host (the only two
// case-insensitive parts of a URL) and leaves path, query and fragment exactly
// as given, since those are case-sensitive and re-encoding them would corrupt
// a signed link.
func normaliseURL(raw string, allowAny bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "url")
	}
	if i := strings.IndexFunc(s, isURLUnsafe); i >= 0 {
		return "", fmt.Errorf("%w: url contains a space or control character at position %d",
			ErrInvalidPayload, i)
	}
	if !urlSchemePrefix.MatchString(s) {
		s = "https://" + s
	}

	u, err := url.Parse(s)
	if err != nil {
		// net/url's message quotes the input and names no internals, but it is
		// wordy; the scheme and host are what a caller can actually fix.
		return "", fmt.Errorf("%w: url is not a valid link: %q", ErrInvalidPayload, raw)
	}

	scheme := strings.ToLower(u.Scheme)
	if !allowAny && !urlSafeSchemes[scheme] {
		return "", fmt.Errorf("%w: scheme %q is not one of http, https, mailto, tel; "+
			"set allow_any to encode it anyway", ErrInvalidPayload, scheme)
	}
	u.Scheme = scheme

	if scheme == "http" || scheme == "https" {
		if u.Host == "" {
			return "", fmt.Errorf("%w: %s url has no host", ErrInvalidPayload, scheme)
		}
		u.Host = strings.ToLower(u.Host)
	} else if u.Opaque == "" && u.Host == "" && u.Path == "" {
		return "", fmt.Errorf("%w: %s url has no body", ErrInvalidPayload, scheme)
	}

	return u.String(), nil
}

// isURLUnsafe reports characters that cannot appear literally in a URL. They
// are rejected rather than escaped: a space in a pasted link is nearly always
// two links or a typo, and escaping it would hide the mistake behind a %20.
func isURLUnsafe(r rune) bool {
	return r <= ' ' || r == 0x7f
}
