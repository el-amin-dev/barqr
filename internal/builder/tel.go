package builder

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nyaruka/phonenumbers"
)

// Tel is the registry name of the telephone builder.
const Tel = "tel"

func init() { Register(telBuilder{}) }

// TelPayload carries a telephone number.
type TelPayload struct {
	Phone       string `json:"phone"`
	PhoneRegion string `json:"phone_region"`
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
		phoneRegionField(),
	}
}

func (b telBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	raw, err := strReq(m, "phone")
	if err != nil {
		return "", err
	}
	region, err := str(m, "phone_region")
	if err != nil {
		return "", err
	}
	number, err := normalisePhoneRegion(raw, region)
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
	// phone_region is consumed on build and deliberately not recovered. By the
	// time a number reaches the string it is already in the form Build emits,
	// so a region would be ignored on rebuild anyway, and reporting one would
	// claim knowledge the string does not carry.
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

// supportedRegions is the set of region codes phonenumbers actually knows,
// computed once. A region is validated against this rather than against a
// two-letter shape check, so a plausible-looking typo is caught rather than
// silently producing a number for nowhere.
var supportedRegions = sync.OnceValue(phonenumbers.GetSupportedRegions)

// normalisePhoneRegion reduces a number to E.164 when a region says which
// country a national number belongs to.
//
// The rules, in order, and each one is load-bearing:
//
//  1. A region is validated whether or not it ends up being used. A typo that
//     were silently ignored would be an option accepted and then discarded.
//  2. A number already in international form is returned untouched and the
//     region is not consulted. The caller has already said which country this
//     is; re-parsing it against a different region is how a correct number
//     becomes a wrong one.
//  3. Otherwise the number is parsed and validated for that region and
//     returned in E.164. An unparseable or invalid number is an error, never a
//     pass-through: emitting a number no dialler can reach is the failure this
//     exists to remove.
//  4. With no region at all the behaviour is exactly normalisePhone's. A
//     national number stays national — see ADR-017 for why that is not treated
//     as a mistake.
func normalisePhoneRegion(raw, region string) (string, error) {
	region = strings.ToUpper(strings.TrimSpace(region))
	if region != "" && !supportedRegions()[region] {
		return "", fmt.Errorf(
			"%w: region %q is not an ISO 3166-1 alpha-2 country code",
			ErrInvalidPayload, region)
	}

	number, err := normalisePhone(raw)
	if err != nil {
		return "", err
	}
	if region == "" || strings.HasPrefix(number, "+") {
		return number, nil
	}

	parsed, err := phonenumbers.Parse(number, region)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not a number for region %s: %w",
			ErrInvalidPayload, raw, region, err)
	}
	if !phonenumbers.IsValidNumber(parsed) {
		return "", fmt.Errorf("%w: %q is not a valid number for region %s",
			ErrInvalidPayload, raw, region)
	}

	// Back through normalisePhone so the digit bounds stay the single gate and
	// there is one exit from this function's contract.
	return normalisePhone(phonenumbers.Format(parsed, phonenumbers.E164))
}

// phoneRegionField is the region declaration the phone-carrying builders
// share, so the name, the wording and the example cannot drift between them.
//
// It is called phone_region rather than region because vcard already has a
// region — the postal one, "Greater London". A caller who wrote region there
// meaning the dialling country would get no error at all: it is a real field,
// so the value would be accepted and silently filed into the address.
func phoneRegionField() Field {
	return Field{
		Name: "phone_region",
		Type: TypeString,
		Description: "ISO 3166-1 alpha-2 country code, used only when the " +
			"number is not already in +international form",
		Example: "GB",
	}
}
