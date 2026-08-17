package builder

import "fmt"

// Raw is the registry name of the unvalidated passthrough builder.
const Raw = "raw"

func init() { Register(rawBuilder{}) }

// RawPayload carries bytes that are already in their final form.
type RawPayload struct {
	Data string `json:"data"`
}

// rawBuilder encodes exactly what it is given.
//
// It exists as the escape hatch for a format this package does not model yet,
// and it validates nothing beyond "not empty" on purpose: the caller has taken
// responsibility for the string. It is also the only builder whose Parse
// accepts every input, which makes it useless for classifying a scanned code
// and is why any caller that tries builders in turn must try raw last.
type rawBuilder struct{}

func (rawBuilder) Name() string { return Raw }

func (rawBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "data",
			Type:        TypeString,
			Description: "the exact string to encode; no validation or escaping is applied",
			Required:    true,
			Example:     "any bytes you like",
		},
	}
}

func (b rawBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}
	// Not strReq: this builder validates nothing, and a payload of spaces is a
	// choice the caller is entitled to make. Only "no data at all" is refused,
	// because no symbology can encode an empty string.
	data, err := str(m, "data")
	if err != nil {
		return "", err
	}
	if data == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "data")
	}
	return data, nil
}

func (rawBuilder) Parse(raw string) (any, bool) {
	if raw == "" {
		return nil, false
	}
	return map[string]any{"data": raw}, true
}
