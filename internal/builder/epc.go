package builder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EPC is the registry name of the SEPA credit-transfer builder.
const EPC = "epc"

func init() { Register(epcBuilder{}) }

// EPCPayload carries a SEPA credit transfer request.
type EPCPayload struct {
	BIC         string  `json:"bic"`
	Name        string  `json:"name"`
	IBAN        string  `json:"iban"`
	Amount      float64 `json:"amount"`
	Purpose     string  `json:"purpose"`
	Reference   string  `json:"reference"`
	Remittance  string  `json:"remittance"`
	Information string  `json:"information"`
}

// EPC069-12 field limits, in characters, and the payload ceiling in bytes.
const (
	epcMaxName        = 70
	epcMaxPurpose     = 4
	epcMaxReference   = 35
	epcMaxRemittance  = 140
	epcMaxInformation = 70
	epcMaxAmount      = 999999999.99
	epcMinAmount      = 0.01
	epcMaxBytes       = 331
	epcLines          = 12
)

var (
	// epcIBAN is the IBAN shape from ISO 13616: two country letters, two check
	// digits, then up to thirty alphanumerics.
	epcIBAN = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)
	// epcBIC is the ISO 9362 business identifier, eight or eleven characters.
	epcBIC = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
)

// epcBuilder emits an EPC QR code, the "GiroCode" a European banking app
// scans to pre-fill a SEPA credit transfer.
//
// The format is twelve positional lines separated by LF — not CRLF. The
// specification is explicit about this, and a CRLF payload is rejected by
// several banking apps outright, so the vCard-style separator must not be
// reused here.
//
// Trailing empty lines are omitted, which the specification permits and which
// buys back modules in a symbol that is often already dense.
type epcBuilder struct{}

func (epcBuilder) Name() string { return EPC }

func (epcBuilder) Fields() []Field {
	return []Field{
		{
			Name: "bic", Type: TypeString,
			Description: "beneficiary bank identifier; optional inside the EEA",
			Example:     "BANKGB2L",
		},
		{
			Name: "name", Type: TypeString, Required: true,
			Description: "beneficiary name, at most 70 characters", Example: "Example Ltd",
		},
		{
			Name: "iban", Type: TypeString, Required: true,
			Description: "beneficiary account", Example: "DE89370400440532013000",
		},
		{
			Name: "amount", Type: TypeNumber,
			Description: "euro amount, 0.01 to 999999999.99", Example: "12.5",
		},
		{
			Name: "purpose", Type: TypeString,
			Description: "four-letter SEPA purpose code", Example: "GDDS",
		},
		{
			Name: "reference", Type: TypeString,
			Description: "structured creditor reference, at most 35 characters",
			Example:     "RF18539007547034",
		},
		{
			Name: "remittance", Type: TypeString,
			Description: "unstructured remittance text, at most 140 characters, " +
				"e.g. Invoice 2026-114; mutually exclusive with reference",
		},
		{
			Name: "information", Type: TypeString,
			Description: "note shown to the payer, at most 70 characters",
			Example:     "Thank you",
		},
	}
}

func (b epcBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	v, err := readStrings(m, []string{
		"bic", "name", "iban", "purpose", "reference", "remittance", "information",
	})
	if err != nil {
		return "", err
	}

	// Both are normalised before validation because a caller pastes an IBAN in
	// the four-character groups a bank statement prints it in.
	v["iban"] = strings.ToUpper(strings.ReplaceAll(v["iban"], " ", ""))
	v["bic"] = strings.ToUpper(strings.ReplaceAll(v["bic"], " ", ""))

	amount, hasAmount, err := number(m, "amount")
	if err != nil {
		return "", err
	}
	if err := epcValidate(v, amount, hasAmount); err != nil {
		return "", err
	}

	amountLine := ""
	if hasAmount {
		// Two decimals exactly: the field is a currency amount, and a bank
		// parsing "EUR12.5" cannot tell 12.50 from a truncated 12.5x.
		amountLine = "EUR" + strconv.FormatFloat(amount, 'f', 2, 64)
	}
	iban, bic := v["iban"], v["bic"]

	// The twelve lines are positional: an empty field still occupies its line,
	// and a field written on the wrong one is a payment to the wrong account.
	lines := []string{
		"BCD",            // service tag
		"002",            // version; 002 is what makes the BIC optional
		"1",              // character set 1 is UTF-8
		"SCT",            // identification: SEPA credit transfer
		bic,              // beneficiary bank
		v["name"],        // beneficiary
		iban,             // beneficiary account
		amountLine,       // EUR-prefixed amount
		v["purpose"],     // purpose code
		v["reference"],   // structured creditor reference
		v["remittance"],  // or unstructured remittance text
		v["information"], // note to the payer
	}

	out := strings.Join(trimTrailingEmpty(lines), "\n")
	if len(out) > epcMaxBytes {
		return "", fmt.Errorf("%w: payload is %d bytes, the maximum is %d",
			ErrInvalidPayload, len(out), epcMaxBytes)
	}
	return out, nil
}

func (epcBuilder) Parse(raw string) (any, bool) {
	lines := strings.Split(raw, "\n")
	if len(lines) < 7 || len(lines) > epcLines || len(raw) > epcMaxBytes {
		return nil, false
	}
	for len(lines) < epcLines {
		lines = append(lines, "")
	}
	if lines[0] != "BCD" || lines[1] != "002" || lines[2] != "1" || lines[3] != "SCT" {
		return nil, false
	}

	v := map[string]string{
		"bic": lines[4], "name": lines[5], "iban": lines[6], "purpose": lines[8],
		"reference": lines[9], "remittance": lines[10], "information": lines[11],
	}

	amount, hasAmount := 0.0, lines[7] != ""
	if hasAmount {
		text, isEuro := strings.CutPrefix(lines[7], "EUR")
		if !isEuro {
			return nil, false
		}
		parsed, err := strconv.ParseFloat(text, 64)
		// Reformatting must reproduce the line exactly, so that a rebuild of
		// the parsed payload is byte-identical rather than merely equivalent.
		if err != nil || strconv.FormatFloat(parsed, 'f', 2, 64) != text {
			return nil, false
		}
		amount = parsed
	}

	// The same validation Build applies: a record this package would refuse to
	// produce is not one it should claim to understand.
	if err := epcValidate(v, amount, hasAmount); err != nil {
		return nil, false
	}
	for _, value := range v {
		if value != strings.TrimSpace(value) {
			return nil, false
		}
	}

	out := map[string]any{"name": v["name"], "iban": v["iban"]}
	for _, key := range []string{"bic", "purpose", "reference", "remittance", "information"} {
		setIfNotEmpty(out, key, v[key])
	}
	if hasAmount {
		out["amount"] = amount
	}
	return out, true
}

// epcValidate holds every rule the format imposes on the variable fields, so
// that Build and Parse cannot drift apart on what a valid transfer looks like.
func epcValidate(v map[string]string, amount float64, hasAmount bool) error {
	if v["name"] == "" {
		return fmt.Errorf("%w: %q", ErrMissingField, "name")
	}
	if v["iban"] == "" {
		return fmt.Errorf("%w: %q", ErrMissingField, "iban")
	}
	if !epcIBAN.MatchString(v["iban"]) {
		return fmt.Errorf("%w: iban must be two country letters, two check digits "+
			"and up to thirty more characters", ErrInvalidPayload)
	}
	if !ibanCheckDigitsOK(v["iban"]) {
		return fmt.Errorf("%w: iban check digits do not match the account number",
			ErrInvalidPayload)
	}
	if v["bic"] != "" && !epcBIC.MatchString(v["bic"]) {
		return fmt.Errorf("%w: bic %q: expected eight or eleven characters",
			ErrInvalidPayload, v["bic"])
	}

	// The specification defines the transfer as one payment with one purpose,
	// so the structured reference and the free-text remittance are exclusive;
	// sending both leaves the bank to guess which one the payer meant.
	if v["reference"] != "" && v["remittance"] != "" {
		return fmt.Errorf("%w: reference and remittance are mutually exclusive",
			ErrInvalidPayload)
	}

	for _, limit := range []struct {
		key string
		max int
	}{
		{"name", epcMaxName},
		{"purpose", epcMaxPurpose},
		{"reference", epcMaxReference},
		{"remittance", epcMaxRemittance},
		{"information", epcMaxInformation},
	} {
		if n := len([]rune(v[limit.key])); n > limit.max {
			return fmt.Errorf("%w: %s is %d characters, the maximum is %d",
				ErrInvalidPayload, limit.key, n, limit.max)
		}
		// Every field occupies exactly one line, so an embedded break would
		// shift every later field onto the wrong one.
		if strings.ContainsAny(v[limit.key], "\r\n") {
			return fmt.Errorf("%w: %s must be a single line, since the format "+
				"separates fields by line", ErrInvalidPayload, limit.key)
		}
	}

	if hasAmount && (amount < epcMinAmount || amount > epcMaxAmount) {
		return fmt.Errorf("%w: amount %s is outside %.2f to %.2f euro",
			ErrInvalidPayload, formatNumber(amount), epcMinAmount, epcMaxAmount)
	}
	return nil
}

// trimTrailingEmpty drops empty tail elements, which is how the EPC format
// says an unused trailing field may be omitted altogether.
func trimTrailingEmpty(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}

// ibanCheckDigitsOK runs the ISO 7064 mod-97 check.
//
// The first four characters move to the end, each letter becomes its position
// in the alphabet plus nine, and the resulting decimal must be 1 modulo 97.
// The remainder is accumulated digit by digit because the number is far wider
// than any integer type.
func ibanCheckDigitsOK(iban string) bool {
	rearranged := iban[4:] + iban[:4]

	remainder := 0
	for i := range len(rearranged) {
		c := rearranged[i]
		switch {
		case c >= '0' && c <= '9':
			remainder = (remainder*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			value := int(c-'A') + 10
			remainder = (remainder*100 + value) % 97
		default:
			return false
		}
	}
	return remainder == 1
}
