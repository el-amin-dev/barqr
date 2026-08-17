package builder

import (
	"strings"
)

// SMS is the registry name of the text-message builder.
const SMS = "sms"

func init() { Register(smsBuilder{}) }

// SMSPayload carries a pre-addressed text message.
type SMSPayload struct {
	Phone   string `json:"phone"`
	Message string `json:"message"`
}

// smsBuilder emits the SMSTO: form.
//
// Why not the RFC 5724 "sms:" URI? Because the wild disagrees about it. The
// RFC puts the body in a semicolon parameter, Android historically read
// "sms:number?body=", iOS read "sms:number&body=" for years, and several
// versions of both ignore a body altogether. SMSTO: is not a standard at all —
// it comes from ZXing's Barcode Contents document — but every scanner app of
// consequence implements exactly one interpretation of it, which is the
// property that actually matters for a printed code.
//
// SMSTO defines no escaping, so the number is validated down to digits and the
// message is taken as everything after the second colon. A colon in the
// message is therefore safe; one in the number is not, and is rejected.
type smsBuilder struct{}

func (smsBuilder) Name() string { return SMS }

func (smsBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "phone",
			Type:        TypeString,
			Description: "the recipient number",
			Required:    true,
			Example:     "+44 20 7946 0958",
		},
		{
			Name:        "message",
			Type:        TypeString,
			Description: "pre-filled message text",
			Required:    false,
			Example:     "I am outside",
		},
	}
}

func (b smsBuilder) Build(payload any) (string, error) {
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
	message, err := str(m, "message")
	if err != nil {
		return "", err
	}

	// The trailing separator is dropped when there is no message, so that the
	// same payload always yields the same string on a round trip.
	if message == "" {
		return "SMSTO:" + number, nil
	}
	return "SMSTO:" + number + ":" + message, nil
}

func (smsBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "SMSTO:")
	if !ok {
		return nil, false
	}

	numberPart, message, hasMessage := strings.Cut(rest, ":")
	number, err := normalisePhone(numberPart)
	if err != nil || number != numberPart {
		return nil, false
	}
	if hasMessage && message == "" {
		// "SMSTO:123:" is ambiguous with the empty-message form this builder
		// never emits; treating it as foreign keeps the round trip exact.
		return nil, false
	}

	out := map[string]any{"phone": number}
	if hasMessage {
		out["message"] = message
	}
	return out, true
}
