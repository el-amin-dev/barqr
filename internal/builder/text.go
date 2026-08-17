package builder

import (
	"fmt"
	"strings"
)

// Text is the registry name of the plain-text builder.
const Text = "text"

func init() { Register(textBuilder{}) }

// TextPayload carries free-form text.
type TextPayload struct {
	Text string `json:"text"`
}

// textBuilder passes text through untouched.
//
// Build is trivial; Parse is the interesting half. A scanner hands back one
// string and the caller wants to know what it is, so every builder's Parse is
// tried in turn. If text accepted everything it would shadow the sixteen
// structured builders, so it instead refuses any string that opens with a
// prefix another builder claims. It is the fallback, not the catch-all — that
// role belongs to raw.
type textBuilder struct{}

// structuredPrefixes are the opening tokens of the other builders' formats.
// A string starting with one of these is a structured payload that happens to
// be malformed, not free text, and saying so is more useful than pretending it
// is prose.
var structuredPrefixes = []string{
	"http://", "https://", "mailto:", "tel:", "smsto:", "sms:",
	"wifi:", "mecard:", "mebkm:", "begin:vcard", "begin:vcalendar", "begin:vevent",
	"geo:", "otpauth://", "bitcoin:", "ethereum:", "litecoin:", "intent://", "bcd\n",
}

func (textBuilder) Name() string { return Text }

func (textBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "text",
			Type:        TypeString,
			Description: "the text to encode, verbatim",
			Required:    true,
			Example:     "Hello from barqr",
		},
	}
}

func (b textBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}
	// Not strReq: a payload of nothing but spaces is odd, but it is text, and
	// this builder's contract is to encode what it is given.
	text, err := str(m, "text")
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "text")
	}
	return text, nil
}

func (textBuilder) Parse(raw string) (any, bool) {
	if raw == "" {
		return nil, false
	}
	lower := strings.ToLower(raw)
	for _, p := range structuredPrefixes {
		if strings.HasPrefix(lower, p) {
			return nil, false
		}
	}
	return map[string]any{"text": raw}, true
}
