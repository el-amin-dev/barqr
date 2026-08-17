package builder

import (
	"fmt"
	"strings"
)

// Tel is the registry name of the telephone builder.
const Tel = "tel"

func init() { Register(telBuilder{}) }

// TelPayload carries a telephone number.
type TelPayload struct {
	Phone string `json:"phone"`
}

// Phone number bounds. E.164 caps a subscriber number at fifteen digits; the
// lower bound admits short codes such as emergency and service numbers.
const (
	phoneMinDigits = 3
	phoneMaxDigits = 15
)

// telBuilder emits an RFC 3966 tel: URI.
type telBuilder struct{}

func (telBuilder) Name() string { return Tel }

func (telBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "phone",
			Type:        TypeString,
			Description: "the number; spaces, dashes, dots and brackets are stripped",
			Required:    true,
			Example:     "+44 20 7946 0958",
		},
	}
}

func (b telBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	raw, err := strReq(m, "phone")
	if err != nil {
		return "", err
	}
	number, err := normalisePhone(raw)
	if err != nil {
		return "", err
	}
	return "tel:" + number, nil
}

func (telBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "tel:")
	if !ok {
		return nil, false
	}
	// A tel: URI may carry parameters such as ";ext=". They are not modelled,
	// and quietly dropping them would round-trip to a different number, so a
	// parameterised URI is simply not this builder's form.
	number, err := normalisePhone(rest)
	if err != nil || number != rest {
		return nil, false
	}
	return map[string]any{"phone": number}, true
}

// normalisePhone reduces a human-typed number to digits with an optional
// leading plus, which is the only form every dialler agrees on.
//
// Separators are stripped rather than rejected because every source of phone
// numbers — a spreadsheet, a CRM, a person — spells them differently, and the
// separators carry no information.
func normalisePhone(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	plus := strings.HasPrefix(s, "+")

	var digits strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')' || r == '/' || r == '\t':
			// Formatting noise.
		case r == '+' && plus && digits.Len() == 0:
			// The leading plus, kept below.
		default:
			return "", fmt.Errorf("%w: phone contains %q, expected digits with an optional leading +",
				ErrInvalidPayload, string(r))
		}
	}

	n := digits.Len()
	if n < phoneMinDigits || n > phoneMaxDigits {
		return "", fmt.Errorf("%w: phone has %d digits, expected between %d and %d",
			ErrInvalidPayload, n, phoneMinDigits, phoneMaxDigits)
	}

	if plus {
		return "+" + digits.String(), nil
	}
	return digits.String(), nil
}

// cutPrefixFold strips a case-insensitive prefix. Scanner output capitalises
// scheme names inconsistently — "TEL:" and "tel:" are both common in the wild
// — and the URI specification says the scheme is case-insensitive anyway.
func cutPrefixFold(s, prefix string) (rest string, ok bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}
