package builder

import (
	"net/url"
	"strings"
)

// WhatsApp is the registry name of the WhatsApp builder.
const WhatsApp = "whatsapp"

func init() { Register(whatsappBuilder{}) }

// WhatsAppPayload carries a WhatsApp click-to-chat link.
type WhatsAppPayload struct {
	Phone   string `json:"phone"`
	Message string `json:"message"`
}

// whatsappHost is the click-to-chat host. wa.me is the short form WhatsApp
// documents; api.whatsapp.com/send is the long form it redirects to and is not
// emitted, since a shorter payload means a sparser, more scannable symbol.
const whatsappHost = "wa.me"

// whatsappBuilder emits a wa.me click-to-chat link.
type whatsappBuilder struct{}

func (whatsappBuilder) Name() string { return WhatsApp }

func (whatsappBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "phone",
			Type:        TypeString,
			Description: "the recipient number in full international form, without the +",
			Required:    true,
			Example:     "+44 20 7946 0958",
		},
		{
			Name:        "message",
			Type:        TypeString,
			Description: "pre-filled message text",
			Required:    false,
			Example:     "Hello!",
		},
	}
}

func (b whatsappBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
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
	// wa.me rejects a leading plus: the path segment is bare digits including
	// the country code, and a national number without one simply fails to
	// resolve for the person scanning it.
	number = strings.TrimPrefix(number, "+")

	message, err := str(m, "message")
	if err != nil {
		return "", err
	}

	out := "https://" + whatsappHost + "/" + number
	if message != "" {
		out += "?text=" + pctEscape(message)
	}
	return out, nil
}

func (whatsappBuilder) Parse(raw string) (any, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") ||
		!strings.EqualFold(u.Host, whatsappHost) {
		return nil, false
	}

	// The path must already be exactly what Build would write: bare digits,
	// no separators, and no leading plus, which wa.me does not accept.
	number := strings.TrimPrefix(u.EscapedPath(), "/")
	if !stableString(number, normalisePhone) || strings.ContainsAny(number, "+/") {
		return nil, false
	}

	params, ok := parseURIQuery(u.RawQuery, "text")
	if !ok {
		return nil, false
	}

	out := map[string]any{"phone": number}
	if text, present := params["text"]; present {
		out["message"] = text
	}
	return out, true
}
