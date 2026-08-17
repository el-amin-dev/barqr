package builder

import (
	"fmt"
	"strings"
)

// VCard is the registry name of the contact-card builder.
const VCard = "vcard"

func init() { Register(vcardBuilder{}) }

// VCardPayload carries a contact card.
type VCardPayload struct {
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Org        string `json:"org"`
	Title      string `json:"title"`
	Phone      string `json:"phone"`
	Mobile     string `json:"mobile"`
	Email      string `json:"email"`
	URL        string `json:"url"`
	Street     string `json:"street"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Note       string `json:"note"`
}

// crlf is the line separator every vCard and iCalendar line ends with. RFC
// 6350 and RFC 5545 both mandate CRLF, and Outlook in particular treats a
// bare LF card as a single unparseable line.
const crlf = "\r\n"

// vcardBuilder emits a vCard 3.0 card.
//
// 3.0 rather than 4.0: phone address books have converged on 3.0 for scanned
// cards, and Android's contact importer still mis-handles 4.0's mandatory
// escaping differences. 3.0 is also what every other QR generator emits, which
// matters more than currency for a format whose only job is to be read.
//
// Lines are not folded at 75 octets. Folding is a transport rule for cards
// travelling through mail systems; inside a QR payload it only adds bytes, and
// unfolding is the single most commonly botched part of a vCard parser.
type vcardBuilder struct{}

func (vcardBuilder) Name() string { return VCard }

func (vcardBuilder) Fields() []Field {
	return []Field{
		{Name: "first_name", Type: TypeString, Description: "given name", Example: "Ada"},
		{Name: "last_name", Type: TypeString, Description: "family name", Example: "Lovelace"},
		{Name: "org", Type: TypeString, Description: "organisation", Example: "Analytical Engines Ltd"},
		{Name: "title", Type: TypeString, Description: "job title", Example: "Chief Engineer"},
		{Name: "phone", Type: TypeString, Description: "work telephone", Example: "+44 20 7946 0958"},
		{Name: "mobile", Type: TypeString, Description: "mobile telephone", Example: "+44 7700 900123"},
		{Name: "email", Type: TypeString, Description: "email address", Example: "ada@example.com"},
		{Name: "url", Type: TypeString, Description: "web address", Example: "https://example.com"},
		{Name: "street", Type: TypeString, Description: "street address", Example: "12 Bishopsgate"},
		{Name: "city", Type: TypeString, Description: "town or city", Example: "London"},
		{Name: "region", Type: TypeString, Description: "county, state or region", Example: "Greater London"},
		{Name: "postal_code", Type: TypeString, Description: "postcode", Example: "EC2N 3AR"},
		{Name: "country", Type: TypeString, Description: "country", Example: "United Kingdom"},
		{Name: "note", Type: TypeString, Description: "free-form note", Example: "Met at the fair"},
	}
}

func (b vcardBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	v, err := readStrings(m, fieldNames(b.Fields()))
	if err != nil {
		return "", err
	}

	// A card with nothing to identify the person by is not a contact, and
	// vCard 3.0 makes FN mandatory, so there has to be something to put in it.
	if v["first_name"] == "" && v["last_name"] == "" && v["org"] == "" {
		return "", fmt.Errorf("%w: one of first_name, last_name or org", ErrMissingField)
	}

	if v["email"] != "" && !emailAddress.MatchString(v["email"]) {
		return "", fmt.Errorf("%w: %q is not an email address of the form name@example.com",
			ErrInvalidPayload, v["email"])
	}
	for _, key := range []string{"phone", "mobile"} {
		if v[key] == "" {
			continue
		}
		number, phoneErr := normalisePhone(v[key])
		if phoneErr != nil {
			return "", fmt.Errorf("%s: %w", key, phoneErr)
		}
		v[key] = number
	}
	if v["url"] != "" {
		link, urlErr := normaliseURL(v["url"], false)
		if urlErr != nil {
			return "", urlErr
		}
		v["url"] = link
	}

	var b2 strings.Builder
	writeLine := func(name, value string) {
		if value == "" {
			return
		}
		b2.WriteString(name + ":" + value + crlf)
	}

	b2.WriteString("BEGIN:VCARD" + crlf)
	b2.WriteString("VERSION:3.0" + crlf)

	// N is structured: family;given;additional;prefix;suffix. Its components
	// are separated by bare semicolons, so any semicolon inside a name has to
	// be escaped or it would silently shift every later component.
	b2.WriteString("N:" + joinComponents(v["last_name"], v["first_name"], "", "", "") + crlf)
	writeLine("FN", escapeTextValue(vcardFullName(v)))
	writeLine("ORG", escapeTextValue(v["org"]))
	writeLine("TITLE", escapeTextValue(v["title"]))
	writeLine("TEL;TYPE=WORK,VOICE", escapeTextValue(v["phone"]))
	writeLine("TEL;TYPE=CELL,VOICE", escapeTextValue(v["mobile"]))
	writeLine("EMAIL;TYPE=INTERNET", escapeTextValue(v["email"]))
	writeLine("URL", escapeTextValue(v["url"]))
	if v["street"]+v["city"]+v["region"]+v["postal_code"]+v["country"] != "" {
		// ADR is po-box;extended;street;locality;region;postcode;country.
		b2.WriteString("ADR;TYPE=WORK:" + joinComponents("", "",
			v["street"], v["city"], v["region"], v["postal_code"], v["country"]) + crlf)
	}
	writeLine("NOTE", escapeTextValue(v["note"]))
	b2.WriteString("END:VCARD" + crlf)

	return b2.String(), nil
}

func (vcardBuilder) Parse(raw string) (any, bool) {
	lines, ok := splitContentLines(raw, "VCARD")
	if !ok {
		return nil, false
	}

	out := map[string]any{}
	version := ""
	for _, line := range lines {
		name, params, value, lineOK := splitContentLine(line)
		if !lineOK {
			return nil, false
		}
		switch name {
		case "VERSION":
			version = value
		case "FN":
			// Derived from the name fields on build; recovering it would make
			// the parsed payload rebuild to a different card.
		case "N":
			parts := splitComponents(value)
			if len(parts) != 5 {
				return nil, false
			}
			setIfNotEmpty(out, "last_name", parts[0])
			setIfNotEmpty(out, "first_name", parts[1])
		case "ORG":
			setIfNotEmpty(out, "org", unescapeTextValue(value))
		case "TITLE":
			setIfNotEmpty(out, "title", unescapeTextValue(value))
		case "TEL":
			key := "phone"
			if strings.Contains(strings.ToUpper(params), "CELL") {
				key = "mobile"
			}
			number := unescapeTextValue(value)
			if !stableString(number, normalisePhone) {
				return nil, false
			}
			out[key] = number
		case "EMAIL":
			addr := unescapeTextValue(value)
			if !stableString(addr, checkEmail) {
				return nil, false
			}
			out["email"] = addr
		case "URL":
			link := unescapeTextValue(value)
			if !stableString(link, strictURL) {
				return nil, false
			}
			out["url"] = link
		case "ADR":
			parts := splitComponents(value)
			if len(parts) != 7 {
				return nil, false
			}
			for i, key := range []string{"street", "city", "region", "postal_code", "country"} {
				setIfNotEmpty(out, key, parts[i+2])
			}
		case "NOTE":
			setIfNotEmpty(out, "note", unescapeTextValue(value))
		default:
			// An unmodelled property would be lost on rebuild, so the card is
			// reported as foreign rather than silently truncated.
			return nil, false
		}
	}

	// The same rule Build applies: without a name or an organisation there is
	// nothing to put in the mandatory FN property.
	_, hasFirst := out["first_name"]
	_, hasLast := out["last_name"]
	_, hasOrg := out["org"]
	if version != "3.0" || !(hasFirst || hasLast || hasOrg) {
		return nil, false
	}
	if !trimmedValues(out) || !noRawCarriageReturn(out) {
		return nil, false
	}
	return out, true
}

// vcardFullName builds the FN property, which vCard 3.0 requires. It falls
// back to the organisation so a company card is still valid.
func vcardFullName(v map[string]string) string {
	name := strings.TrimSpace(v["first_name"] + " " + v["last_name"])
	if name == "" {
		return v["org"]
	}
	return name
}

// readStrings pulls every named key out of the payload as a trimmed string, so
// a builder with a dozen text fields does not repeat the same six lines a
// dozen times.
func readStrings(m map[string]any, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		s, err := str(m, k)
		if err != nil {
			return nil, err
		}
		out[k] = strings.TrimSpace(s)
	}
	return out, nil
}

// setIfNotEmpty records a parsed value only when it carries something, so that
// the parsed payload matches the payload that produced the string rather than
// carrying a spray of empty keys.
func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// escapeTextValue escapes a TEXT value per RFC 6350 §3.4, which RFC 5545
// §3.3.11 repeats verbatim for iCalendar. Backslash, comma, semicolon and
// newline are the whole set; a colon is explicitly not escaped, because the
// property name is separated at the first colon and everything after it is
// value.
func escapeTextValue(s string) string {
	// Any spelling of a line break becomes the literal two characters \n, so
	// the encoded card stays one physical line per property.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapeTextValue reverses escapeTextValue. An unrecognised escape yields
// the escaped character itself, which is what RFC 6350 tells a parser to do
// with an escape it does not know.
func unescapeTextValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	escaped := false
	for _, r := range s {
		if !escaped {
			if r == '\\' {
				escaped = true
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteRune(r)
		}
		escaped = false
	}
	if escaped {
		// A trailing lone backslash is malformed; keeping it is friendlier
		// than dropping the value.
		b.WriteByte('\\')
	}
	return b.String()
}

// noRawCarriageReturn reports whether every parsed value is free of a bare CR.
//
// escapeTextValue folds every spelling of a line break into a literal "\n", so
// a value carrying a real carriage return cannot have come from Build and
// would rebuild as something different.
func noRawCarriageReturn(m map[string]any) bool {
	for _, v := range m {
		if s, ok := v.(string); ok && strings.ContainsRune(s, '\r') {
			return false
		}
	}
	return true
}

// joinComponents assembles a structured property value, escaping each
// component so that its own semicolons cannot be mistaken for separators.
func joinComponents(parts ...string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = escapeTextValue(p)
	}
	return strings.Join(out, ";")
}

// splitComponents splits a structured value on unescaped semicolons and
// unescapes each component.
func splitComponents(value string) []string {
	var out []string
	var cur strings.Builder

	escaped := false
	for _, r := range value {
		switch {
		case escaped:
			cur.WriteRune('\\')
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == ';':
			out = append(out, unescapeTextValue(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if escaped {
		cur.WriteByte('\\')
	}
	return append(out, unescapeTextValue(cur.String()))
}

// splitContentLines validates the BEGIN/END envelope of a vCard or iCalendar
// object and returns the lines between them.
func splitContentLines(raw, object string) ([]string, bool) {
	trimmed := strings.TrimSuffix(raw, crlf)
	if !strings.Contains(trimmed, crlf) {
		return nil, false
	}
	lines := strings.Split(trimmed, crlf)
	if len(lines) < 3 {
		return nil, false
	}
	if !strings.EqualFold(lines[0], "BEGIN:"+object) ||
		!strings.EqualFold(lines[len(lines)-1], "END:"+object) {
		return nil, false
	}
	return lines[1 : len(lines)-1], true
}

// splitContentLine breaks "NAME;PARAMS:value" apart. The name is upper-cased
// because RFC 6350 property names are case-insensitive and generators differ.
func splitContentLine(line string) (name, params, value string, ok bool) {
	head, value, found := strings.Cut(line, ":")
	if !found || head == "" {
		return "", "", "", false
	}
	name, params, _ = strings.Cut(head, ";")
	return strings.ToUpper(name), params, value, true
}
