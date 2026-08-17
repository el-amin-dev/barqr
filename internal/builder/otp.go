package builder

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// OTP is the registry name of the one-time-password builder.
const OTP = "otp"

func init() { Register(otpBuilder{}) }

// OTPPayload carries an authenticator enrolment.
//
// The secret is the whole credential: anyone who reads it can generate codes
// for the account forever. It must never reach a log, a metric label, or an
// error message, which is why this type has a String method.
type OTPPayload struct {
	Type      string  `json:"type"`
	Issuer    string  `json:"issuer"`
	Account   string  `json:"account"`
	Secret    string  `json:"secret"`
	Algorithm string  `json:"algorithm"`
	Digits    float64 `json:"digits"`
	Period    float64 `json:"period"`
	Counter   float64 `json:"counter"`
}

// String renders the payload with the secret redacted.
func (p OTPPayload) String() string {
	return fmt.Sprintf("OTPPayload{Type:%q Issuer:%q Account:%q Algorithm:%q "+
		"Digits:%g Period:%g Counter:%g Secret:%s}",
		p.Type, p.Issuer, p.Account, p.Algorithm, p.Digits, p.Period, p.Counter, redacted)
}

// otpBase32 is RFC 4648 base32 without the extended hex alphabet, which is
// what every authenticator app expects. Padding is tolerated on input and
// stripped, because the Key Uri Format says padding should be omitted and half
// the apps in circulation choke on it.
var otpBase32 = regexp.MustCompile(`^[A-Z2-7]+=*$`)

// One-time-password defaults and bounds from the Key Uri Format.
const (
	otpMinDigits = 6
	otpMaxDigits = 8
	otpMaxPeriod = 3600
	// otpMaxCounter is 2^53, the largest integer a float64 payload can carry
	// without losing precision. RFC 4226 allows a 64-bit counter; anything
	// above this bound could not be rendered back exactly.
	otpMaxCounter = 1 << 53
)

// otpBuilder emits an otpauth:// enrolment URI.
//
// The format is Google's Key Uri Format, the de facto standard every
// authenticator implements. Two details bite: the label is "issuer:account"
// where the colon is a separator and both halves are percent-encoded
// separately, and the issuer belongs in both the label and the query, because
// older apps read only one of the two.
type otpBuilder struct{}

func (otpBuilder) Name() string { return OTP }

func (otpBuilder) Fields() []Field {
	return []Field{
		{
			Name: "type", Type: TypeString,
			Description: "totp for time-based, hotp for counter-based", Example: "totp",
		},
		{
			Name: "issuer", Type: TypeString,
			Description: "the service the account belongs to", Example: "Example Ltd",
		},
		{
			Name: "account", Type: TypeString, Required: true,
			Description: "the account the codes are for", Example: "ada@example.com",
		},
		{
			Name: "secret", Type: TypeString, Required: true,
			Description: "the shared secret in base32; treated as a secret and never logged",
			Example:     "JBSWY3DPEHPK3PXP",
		},
		{
			Name: "algorithm", Type: TypeString,
			Description: "SHA1, SHA256 or SHA512", Example: "SHA1",
		},
		{
			Name: "digits", Type: TypeNumber,
			Description: "code length, 6 to 8", Example: "6",
		},
		{
			Name: "period", Type: TypeNumber,
			Description: "totp step in seconds", Example: "30",
		},
		{
			Name: "counter", Type: TypeNumber,
			Description: "hotp counter, required for hotp, e.g. 0; rejected for totp",
		},
	}
}

func (b otpBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	v, err := readStrings(m, []string{"type", "issuer", "account", "secret", "algorithm"})
	if err != nil {
		return "", err
	}

	kind := strings.ToLower(v["type"])
	if kind == "" {
		kind = "totp"
	}
	if kind != "totp" && kind != "hotp" {
		return "", fmt.Errorf("%w: type %q: expected totp or hotp", ErrInvalidPayload, v["type"])
	}
	if v["account"] == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "account")
	}
	if strings.Contains(v["issuer"], ":") {
		return "", fmt.Errorf("%w: issuer must not contain a colon, which separates it "+
			"from the account", ErrInvalidPayload)
	}

	secret, err := otpSecret(v["secret"])
	if err != nil {
		return "", err
	}
	algorithm, err := otpAlgorithm(v["algorithm"])
	if err != nil {
		return "", err
	}

	nums := map[string]float64{}
	for _, key := range []string{"digits", "period", "counter"} {
		value, present, numErr := number(m, key)
		if numErr != nil {
			return "", numErr
		}
		if present {
			nums[key] = value
		}
	}
	if err := otpValidate(kind, nums); err != nil {
		return "", err
	}

	label := pctEscape(v["account"])
	if v["issuer"] != "" {
		label = pctEscape(v["issuer"]) + ":" + label
	}

	params := []string{"secret=" + secret}
	if v["issuer"] != "" {
		params = append(params, "issuer="+pctEscape(v["issuer"]))
	}
	if algorithm != "" {
		params = append(params, "algorithm="+algorithm)
	}
	// Fixed order, so the same payload always yields the same URI.
	for _, key := range []string{"digits", "period", "counter"} {
		if value, present := nums[key]; present {
			params = append(params, key+"="+formatNumber(value))
		}
	}

	return "otpauth://" + kind + "/" + label + "?" + strings.Join(params, "&"), nil
}

func (otpBuilder) Parse(raw string) (any, bool) {
	u, err := url.Parse(raw)
	// A fragment carries nothing this format defines and would be dropped on
	// a rebuild, so a URI with one is not this builder's form.
	if err != nil || !strings.EqualFold(u.Scheme, "otpauth") || u.Fragment != "" {
		return nil, false
	}
	kind := strings.ToLower(u.Host)
	if kind != "totp" && kind != "hotp" {
		return nil, false
	}

	// The label is split before it is unescaped: an account containing a colon
	// carries it as %3A, and unescaping first would split the label at the
	// account's own colon instead of at the issuer separator.
	label := strings.TrimPrefix(u.EscapedPath(), "/")
	if label == "" {
		return nil, false
	}
	escapedIssuer, escapedAccount, hasIssuer := strings.Cut(label, ":")
	if !hasIssuer {
		escapedIssuer, escapedAccount = "", label
	}
	issuer, err := url.PathUnescape(escapedIssuer)
	if err != nil {
		return nil, false
	}
	account, err := url.PathUnescape(escapedAccount)
	if err != nil || account == "" {
		return nil, false
	}

	params, ok := parseURIQuery(u.RawQuery,
		"secret", "issuer", "algorithm", "digits", "period", "counter")
	if !ok {
		return nil, false
	}
	// The secret must already be in the normal form Build emits — upper case,
	// no display grouping, no padding — or the rebuilt URI would differ from
	// the one that was parsed.
	secret := params["secret"]
	if !stableString(secret, otpSecret) {
		return nil, false
	}

	out := map[string]any{"type": kind, "account": account, "secret": secret}
	if issuer != "" {
		// The label and the query must agree; a URI where they disagree is
		// ambiguous and no rebuild of it could be faithful.
		if q, present := params["issuer"]; present && q != issuer {
			return nil, false
		}
		out["issuer"] = issuer
	} else if params["issuer"] != "" {
		return nil, false
	}

	if a := params["algorithm"]; a != "" {
		normalised, algErr := otpAlgorithm(a)
		if algErr != nil {
			return nil, false
		}
		out["algorithm"] = normalised
	}
	nums := map[string]float64{}
	for _, key := range []string{"digits", "period", "counter"} {
		text, present := params[key]
		if !present {
			continue
		}
		value, ok2, numErr := number(map[string]any{key: text}, key)
		// Reformatting must reproduce the parameter exactly, otherwise a
		// rebuild would write "06" as "6".
		if numErr != nil || !ok2 || formatNumber(value) != text {
			return nil, false
		}
		nums[key] = value
		out[key] = value
	}

	// The same rules Build applies: a URI this package would refuse to produce
	// is not one it should claim to understand.
	if otpValidate(kind, nums) != nil || !trimmedValues(out) {
		return nil, false
	}
	return out, true
}

// otpValidate holds the numeric rules and the totp/hotp split, so that Build
// and Parse cannot disagree about what a valid enrolment looks like.
func otpValidate(kind string, nums map[string]float64) error {
	if digits, present := nums["digits"]; present &&
		(digits != math.Trunc(digits) || digits < otpMinDigits || digits > otpMaxDigits) {
		return fmt.Errorf("%w: digits %s: expected a whole number from %d to %d",
			ErrInvalidPayload, formatNumber(digits), otpMinDigits, otpMaxDigits)
	}
	period, hasPeriod := nums["period"]
	if hasPeriod && (period != math.Trunc(period) || period < 1 || period > otpMaxPeriod) {
		return fmt.Errorf("%w: period %s: expected whole seconds from 1 to %d",
			ErrInvalidPayload, formatNumber(period), otpMaxPeriod)
	}
	counter, hasCounter := nums["counter"]
	if hasCounter && (counter != math.Trunc(counter) || counter < 0 || counter > otpMaxCounter) {
		return fmt.Errorf("%w: counter %s: expected a whole number from 0 to %s",
			ErrInvalidPayload, formatNumber(counter), formatNumber(otpMaxCounter))
	}

	// The counter and the time step are the two alternative moving factors;
	// each belongs to exactly one of the two algorithms.
	switch kind {
	case "hotp":
		if !hasCounter {
			return fmt.Errorf("%w: %q is required for hotp", ErrMissingField, "counter")
		}
		if hasPeriod {
			return fmt.Errorf("%w: period applies to totp, not hotp", ErrInvalidPayload)
		}
	case "totp":
		if hasCounter {
			return fmt.Errorf("%w: counter applies to hotp, not totp", ErrInvalidPayload)
		}
	}
	return nil
}

// otpSecret validates and normalises the shared secret. The error never quotes
// the secret; naming the offending character class is enough to fix a typo and
// safe to write down.
func otpSecret(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "secret")
	}
	// Authenticator secrets are routinely displayed in groups of four, so the
	// grouping is stripped rather than rejected.
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(raw))
	s = strings.TrimRight(s, "=")
	if !otpBase32.MatchString(s) {
		return "", fmt.Errorf("%w: secret must be base32, using A-Z and 2-7", ErrInvalidPayload)
	}
	return s, nil
}

// otpAlgorithm normalises the HMAC algorithm name. An empty value is left
// empty so the parameter is omitted and the app applies its own default of
// SHA1, which is what the Key Uri Format specifies.
func otpAlgorithm(raw string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "SHA1":
		return "SHA1", nil
	case "SHA256":
		return "SHA256", nil
	case "SHA512":
		return "SHA512", nil
	default:
		return "", fmt.Errorf("%w: algorithm %q: expected SHA1, SHA256 or SHA512",
			ErrInvalidPayload, raw)
	}
}
