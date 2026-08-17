package builder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Email is the registry name of the mailto builder.
const Email = "email"

func init() { Register(emailBuilder{}) }

// EmailPayload carries a pre-addressed message.
type EmailPayload struct {
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// emailAddress is the address form this builder accepts.
//
// It is narrower than RFC 5322 on purpose. The exotic-but-legal local-part
// characters (&, ?, /, =, #, quoted strings) are all URI-reserved, so a mailto
// carrying them has to percent-encode them, and mail clients disagree on
// whether they decode the local part before or after splitting the query.
// Rejecting them is honest; producing a link that opens the wrong compose
// window is not.
var emailAddress = regexp.MustCompile(
	`^[A-Za-z0-9._+\-]+@[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)

// emailBuilder emits an RFC 6068 mailto: URI.
type emailBuilder struct{}

func (emailBuilder) Name() string { return Email }

func (emailBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "email",
			Type:        TypeString,
			Description: "the recipient address",
			Required:    true,
			Example:     "hello@example.com",
		},
		{
			Name:        "subject",
			Type:        TypeString,
			Description: "pre-filled subject line",
			Required:    false,
			Example:     "Table for two",
		},
		{
			Name:        "body",
			Type:        TypeString,
			Description: "pre-filled message body",
			Required:    false,
			Example:     "Friday at 8pm, please.",
		},
	}
}

func (b emailBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	addr, err := strReq(m, "email")
	if err != nil {
		return "", err
	}
	addr, err = checkEmail(strings.TrimSpace(addr))
	if err != nil {
		return "", err
	}

	subject, err := str(m, "subject")
	if err != nil {
		return "", err
	}
	body, err := str(m, "body")
	if err != nil {
		return "", err
	}

	// The validated address contains nothing that is reserved in a URI, so it
	// goes in literally; percent-encoding the "@" is legal but confuses enough
	// clients to be worth avoiding.
	out := "mailto:" + addr
	var params []string
	if subject != "" {
		params = append(params, "subject="+pctEscape(subject))
	}
	if body != "" {
		params = append(params, "body="+pctEscape(body))
	}
	if len(params) > 0 {
		out += "?" + strings.Join(params, "&")
	}
	return out, nil
}

func (emailBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "mailto:")
	if !ok {
		return nil, false
	}

	addrPart, query, _ := strings.Cut(rest, "?")
	addr, err := url.PathUnescape(addrPart)
	if err != nil || !stableString(addr, checkEmail) {
		return nil, false
	}

	params, ok := parseURIQuery(query, "subject", "body")
	if !ok {
		return nil, false
	}

	out := map[string]any{"email": addr}
	for k, v := range params {
		out[k] = v
	}
	return out, true
}

// checkEmail validates an address and returns it unchanged, so that it can be
// passed to stableString alongside the normalising checks.
func checkEmail(addr string) (string, error) {
	if !emailAddress.MatchString(addr) {
		return "", fmt.Errorf("%w: %q is not an email address of the form name@example.com",
			ErrInvalidPayload, addr)
	}
	return addr, nil
}

// parseURIQuery splits a URI query into the named parameters, reporting false
// for a malformed pair or a parameter outside the allowed set.
//
// url.ParseQuery is not used because it decodes "+" as a space, a rule that
// belongs to HTML form bodies rather than to RFC 3986 URIs. A mailto body of
// "1+1=2" would come back as "1 1=2". PathUnescape decodes percent escapes and
// leaves the plus alone, which is what these URIs mean.
func parseURIQuery(query string, allowed ...string) (map[string]string, bool) {
	out := make(map[string]string, len(allowed))
	if query == "" {
		return out, true
	}

	permitted := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		permitted[a] = true
	}

	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			return nil, false
		}
		rawKey, rawVal, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			return nil, false
		}
		key, err := url.PathUnescape(rawKey)
		if err != nil || !permitted[strings.ToLower(key)] {
			return nil, false
		}
		val, err := url.PathUnescape(rawVal)
		// An empty parameter carries nothing and is never emitted, so treating
		// it as absent would rebuild a different URI.
		if err != nil || val == "" {
			return nil, false
		}
		if _, dup := out[strings.ToLower(key)]; dup {
			return nil, false
		}
		out[strings.ToLower(key)] = val
	}
	return out, true
}
