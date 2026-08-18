package builder

import (
	"fmt"
	"strings"
)

// MeCard is the registry name of the MECARD contact builder.
const MeCard = "mecard"

func init() { Register(mecardBuilder{}) }

// MeCardPayload carries a compact contact card.
type MeCardPayload struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Phone       string `json:"phone"`
	PhoneRegion string `json:"phone_region"`
	Email       string `json:"email"`
	URL         string `json:"url"`
	Address     string `json:"address"`
	Note        string `json:"note"`
}

// mecardBuilder emits NTT DoCoMo's MECARD.
//
// MECARD exists because a vCard is verbose and a verbose payload is a denser,
// harder-to-scan symbol; a MECARD of the same contact is routinely half the
// size. It carries less — no job title, no structured address — which is the
// trade being made.
//
// Its escaping rules come from the DoCoMo specification: a backslash escapes
// backslash, semicolon, colon and comma, and nothing else. Newlines have no
// escape, and are left as-is because nothing in the grammar splits on them.
type mecardBuilder struct{}

func (mecardBuilder) Name() string { return MeCard }

func (mecardBuilder) Fields() []Field {
	return []Field{
		{Name: "last_name", Type: TypeString, Description: "family name", Example: "Lovelace"},
		{Name: "first_name", Type: TypeString, Description: "given name", Example: "Ada"},
		{Name: "phone", Type: TypeString, Description: "telephone", Example: "+44 20 7946 0958"},
		phoneRegionField(),
		{Name: "email", Type: TypeString, Description: "email address", Example: "ada@example.com"},
		{Name: "url", Type: TypeString, Description: "web address", Example: "https://example.com"},
		{
			Name: "address", Type: TypeString,
			Description: "postal address on one line", Example: "12 Bishopsgate, London",
		},
		{Name: "note", Type: TypeString, Description: "free-form note", Example: "Met at the fair"},
	}
}

func (b mecardBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	v, err := readStrings(m, fieldNames(b.Fields()))
	if err != nil {
		return "", err
	}
	if v["first_name"] == "" && v["last_name"] == "" {
		return "", fmt.Errorf("%w: one of first_name or last_name", ErrMissingField)
	}
	if v["email"] != "" && !emailAddress.MatchString(v["email"]) {
		return "", fmt.Errorf("%w: %q is not an email address of the form name@example.com",
			ErrInvalidPayload, v["email"])
	}
	if v["phone"] != "" {
		number, phoneErr := normalisePhoneRegion(v["phone"], v["phone_region"])
		if phoneErr != nil {
			return "", fmt.Errorf("phone: %w", phoneErr)
		}
		v["phone"] = number
	} else if v["phone_region"] != "" {
		// A region with nothing to apply it to is a mistake worth naming.
		// Accepting it silently is how an option comes to mean nothing.
		return "", fmt.Errorf("%w: phone_region is set but no phone number was given",
			ErrInvalidPayload)
	}
	if v["url"] != "" {
		link, urlErr := normaliseURL(v["url"], false)
		if urlErr != nil {
			return "", urlErr
		}
		v["url"] = link
	}

	var out strings.Builder
	out.WriteString("MECARD:")
	// N is "family,given"; the comma is a separator, so a comma inside either
	// component must be escaped or the name splits in the wrong place.
	out.WriteString("N:" + docomoEscape(v["last_name"]) + "," + docomoEscape(v["first_name"]) + ";")

	for _, f := range []struct{ key, tag string }{
		{"phone", "TEL"},
		{"email", "EMAIL"},
		{"url", "URL"},
		{"address", "ADR"},
		{"note", "NOTE"},
	} {
		if v[f.key] != "" {
			out.WriteString(f.tag + ":" + docomoEscape(v[f.key]) + ";")
		}
	}
	// The record terminator is a second semicolon after the last field.
	out.WriteString(";")

	return out.String(), nil
}

func (mecardBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "MECARD:")
	if !ok || !strings.HasSuffix(rest, ";;") {
		return nil, false
	}
	rest = strings.TrimSuffix(rest, ";")

	out := map[string]any{}
	for _, field := range docomoSplit(rest, ';') {
		if field == "" {
			continue
		}
		rawTag, rawValue, found := docomoCut(field, ':')
		if !found {
			return nil, false
		}
		value := docomoUnescape(rawValue)

		switch strings.ToUpper(docomoUnescape(rawTag)) {
		case "N":
			parts := docomoSplit(rawValue, ',')
			if len(parts) != 2 {
				return nil, false
			}
			setIfNotEmpty(out, "last_name", docomoUnescape(parts[0]))
			setIfNotEmpty(out, "first_name", docomoUnescape(parts[1]))
		case "TEL":
			if !stableString(value, normalisePhone) {
				return nil, false
			}
			out["phone"] = value
		case "EMAIL":
			if !stableString(value, checkEmail) {
				return nil, false
			}
			out["email"] = value
		case "URL":
			if !stableString(value, strictURL) {
				return nil, false
			}
			out["url"] = value
		case "ADR":
			setIfNotEmpty(out, "address", value)
		case "NOTE":
			setIfNotEmpty(out, "note", value)
		default:
			// SOUND, BDAY and friends are not modelled; keeping them would
			// mean rebuilding a different card.
			return nil, false
		}
	}

	// The same rule Build applies: a MECARD without a name is not a contact.
	_, hasFirst := out["first_name"]
	_, hasLast := out["last_name"]
	if (!hasFirst && !hasLast) || !trimmedValues(out) {
		return nil, false
	}
	return out, true
}

// docomoEscape escapes the four characters the MECARD grammar gives meaning
// to. Unlike vCard, the colon is in the set, because MECARD separates a tag
// from its value at the first colon rather than at a fixed position.
func docomoEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', ';', ':', ',':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// docomoUnescape reverses docomoEscape, passing through an unknown escape as
// the character it precedes.
func docomoUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	escaped := false
	for _, r := range s {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
		escaped = false
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String()
}

// docomoSplit splits on unescaped separators, leaving the parts escaped so a
// caller can split them again before unescaping.
func docomoSplit(s string, sep rune) []string {
	var out []string
	var cur strings.Builder

	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune('\\')
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == sep:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if escaped {
		cur.WriteByte('\\')
	}
	return append(out, cur.String())
}

// docomoCut splits at the first unescaped separator, which is how a tag is
// told from a value that may itself contain escaped colons.
func docomoCut(s string, sep rune) (before, after string, found bool) {
	parts := docomoSplit(s, sep)
	if len(parts) < 2 {
		return s, "", false
	}
	return parts[0], strings.Join(parts[1:], string(sep)), true
}
