package builder

import (
	"fmt"
	"strings"
)

// WiFi is the registry name of the Wi-Fi credential builder.
const WiFi = "wifi"

func init() { Register(wifiBuilder{}) }

// WiFiPayload carries a Wi-Fi network credential.
//
// The password is a secret. It must never reach a log, a metric label, or an
// error message, which is why this type has a String method: a stray %v on the
// struct prints the redaction rather than the key to the network.
type WiFiPayload struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
	Hidden   bool   `json:"hidden"`
}

// String renders the payload with the password redacted.
func (p WiFiPayload) String() string {
	return fmt.Sprintf("WiFiPayload{SSID:%q Auth:%q Hidden:%t Password:%s}",
		p.SSID, p.Auth, p.Hidden, redacted)
}

// redacted is what a secret prints as.
const redacted = "[redacted]"

// Wi-Fi authentication types, as spelled in the payload.
const (
	authNone   = "nopass"
	authWEP    = "WEP"
	authWPA    = "WPA"
	authWPAEAP = "WPA2-EAP"
)

// SSID and passphrase bounds from IEEE 802.11. The SSID is 32 octets; a WPA
// passphrase is 8 to 63 printable characters, and a shorter one is a typo
// rather than a network anybody can join.
const (
	ssidMaxBytes  = 32
	wpaMinPass    = 8
	wpaMaxPass    = 63
	wifiSpecials  = `\;,:"`
	eapMinPassLen = 1
)

// wifiBuilder emits the WIFI: credential record.
//
// The format is ZXing's, not a standard: "WIFI:T:WPA;S:ssid;P:pass;H:true;;".
// Two of its rules are easy to miss and both are implemented here. Backslash
// escapes the five characters that would otherwise end a field, and a value
// made entirely of hex digits must be wrapped in double quotes, because
// Android otherwise reads it as a raw hex PSK and joins the wrong network — or
// no network at all.
type wifiBuilder struct{}

func (wifiBuilder) Name() string { return WiFi }

func (wifiBuilder) Fields() []Field {
	return []Field{
		{
			Name: "ssid", Type: TypeString, Required: true,
			Description: "network name, at most 32 bytes", Example: "Cafe Guest",
		},
		{
			Name: "password", Type: TypeString,
			Description: "passphrase; omit for an open network", Example: "correct horse",
		},
		{
			Name: "auth", Type: TypeString,
			Description: "nopass, WEP, WPA or WPA2-EAP; defaults to WPA when a password is given",
			Example:     "WPA",
		},
		{
			Name: "hidden", Type: TypeBool,
			Description: "the network does not broadcast its SSID", Example: "false",
		},
	}
}

func (b wifiBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	ssid, err := strReq(m, "ssid")
	if err != nil {
		return "", err
	}
	if len(ssid) > ssidMaxBytes {
		return "", fmt.Errorf("%w: ssid is %d bytes, the maximum is %d",
			ErrInvalidPayload, len(ssid), ssidMaxBytes)
	}

	// Read without trimming: a passphrase may legitimately begin or end with a
	// space, and silently trimming one would produce a code that never joins.
	password, err := str(m, "password")
	if err != nil {
		return "", err
	}
	hidden, err := boolean(m, "hidden")
	if err != nil {
		return "", err
	}
	rawAuth, err := str(m, "auth")
	if err != nil {
		return "", err
	}
	auth, err := wifiAuth(rawAuth, password)
	if err != nil {
		return "", err
	}
	if err := wifiPasswordOK(auth, password); err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString("WIFI:T:" + auth + ";")
	out.WriteString("S:" + wifiEscape(ssid) + ";")
	if auth != authNone {
		out.WriteString("P:" + wifiEscape(password) + ";")
	}
	if hidden {
		out.WriteString("H:true;")
	}
	out.WriteString(";")
	return out.String(), nil
}

func (wifiBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "WIFI:")
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
		switch strings.ToUpper(docomoUnescape(rawTag)) {
		case "T":
			auth, authErr := wifiAuth(docomoUnescape(rawValue), "")
			if authErr != nil {
				return nil, false
			}
			out["auth"] = auth
		case "S":
			out["ssid"] = wifiUnescape(rawValue)
		case "P":
			setIfNotEmpty(out, "password", wifiUnescape(rawValue))
		case "H":
			hidden, boolErr := boolean(map[string]any{"h": docomoUnescape(rawValue)}, "h")
			if boolErr != nil {
				return nil, false
			}
			out["hidden"] = hidden
		default:
			// The enterprise sub-fields (E, I, A, PH2) are not modelled, so a
			// record carrying them is not this builder's form.
			return nil, false
		}
	}

	ssid, hasSSID := out["ssid"].(string)
	if !hasSSID || ssid == "" || len(ssid) > ssidMaxBytes {
		return nil, false
	}

	// Records in the wild omit T for an open network. The default is filled in
	// here rather than left absent so that the parsed payload states what the
	// record means instead of relying on the same inference happening again.
	password, _ := out["password"].(string)
	if _, hasAuth := out["auth"]; !hasAuth {
		auth, err := wifiAuth("", password)
		if err != nil {
			return nil, false
		}
		out["auth"] = auth
	}
	auth, _ := out["auth"].(string)
	if wifiPasswordOK(auth, password) != nil {
		return nil, false
	}
	return out, true
}

// wifiAuth normalises an authentication type. An empty value follows what the
// rest of the payload implies: a credential with a passphrase is WPA, one
// without is an open network.
func wifiAuth(raw, password string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "":
		if password == "" {
			return authNone, nil
		}
		return authWPA, nil
	case "NOPASS", "NONE", "OPEN":
		return authNone, nil
	case "WEP":
		return authWEP, nil
	// WPA2 and WPA3 are spelled WPA in this format; the record says which
	// handshake family to use, not which revision.
	case "WPA", "WPA2", "WPA3", "WPA/WPA2":
		return authWPA, nil
	case "WPA2-EAP", "WPA2EAP", "EAP":
		return authWPAEAP, nil
	default:
		return "", fmt.Errorf("%w: auth %q: expected nopass, WEP, WPA or WPA2-EAP",
			ErrInvalidPayload, raw)
	}
}

// wifiPasswordOK checks the passphrase against the rules of the chosen
// authentication type. The passphrase itself never appears in an error.
func wifiPasswordOK(auth, password string) error {
	switch auth {
	case authNone:
		if password != "" {
			return fmt.Errorf("%w: auth is nopass but a password was given", ErrInvalidPayload)
		}
	case authWEP:
		// A WEP key is 5 or 13 ASCII characters, or the same key written as 10
		// or 26 hex digits.
		n := len(password)
		hex := isHexString(password)
		if !(n == 5 || n == 13 || (hex && (n == 10 || n == 26))) {
			return fmt.Errorf(
				"%w: a WEP key is 5 or 13 characters, or 10 or 26 hex digits; this one is %d",
				ErrInvalidPayload, n)
		}
	case authWPA:
		if n := len(password); n < wpaMinPass || n > wpaMaxPass {
			return fmt.Errorf("%w: a WPA passphrase is %d to %d characters; this one is %d",
				ErrInvalidPayload, wpaMinPass, wpaMaxPass, n)
		}
	case authWPAEAP:
		// Enterprise credentials are an account password, which no rule bounds
		// below one character.
		if n := len(password); n < eapMinPassLen || n > wpaMaxPass {
			return fmt.Errorf("%w: a WPA2-EAP password is 1 to %d characters; this one is %d",
				ErrInvalidPayload, wpaMaxPass, n)
		}
	}
	return nil
}

// wifiEscape escapes the record's five reserved characters and applies the
// hex-quoting rule.
func wifiEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(wifiSpecials, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}

	// A hex-looking value contains none of the reserved characters, so the
	// quotes can only have come from here and Parse can strip them safely.
	if isHexString(s) {
		return `"` + b.String() + `"`
	}
	return b.String()
}

// wifiUnescape reverses wifiEscape, unwrapping the hex quoting first.
func wifiUnescape(raw string) string {
	if len(raw) >= 2 && strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		return raw[1 : len(raw)-1]
	}
	return docomoUnescape(raw)
}

// isHexString reports whether every character is a hex digit, which is the
// condition ZXing's hex-quoting rule turns on.
func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
