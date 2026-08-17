// Package builder turns a structured payload into the raw string that gets
// encoded into a code, and parses that string back.
//
// A Builder is the layer above the encoder: the encoder knows how to draw
// modules for a string, the builder knows that a Wi-Fi credential is spelled
// "WIFI:T:WPA;S:home;P:hunter2;;" and that the semicolon inside an SSID has to
// be escaped. Builders are registered by name in an init function and looked
// up through the registry, never through a switch in a caller, so adding a
// payload type is one new file plus one Register call.
//
// Two invariants hold for every builder in this package:
//
//   - Build is a pure function of its payload. No clock, no randomness, no
//     network. The same payload always produces byte-identical output, which
//     is what makes caching and the round-trip test possible.
//   - Parse(Build(p)) recovers a payload that rebuilds to the same string.
//     Parse is deliberately strict: it reports ok=false for anything that is
//     not of its own form, so a caller can try every builder in turn to
//     classify a scanned code.
//
// Payloads arrive as any, in practice a map[string]any decoded from a JSON
// body or from query parameters, or one of the exported payload structs in
// this package. Both forms go through toMap, so a builder only ever sees a
// map, and unknown keys are rejected with a suggestion rather than silently
// ignored — a typo in "sbuject" must not produce a code missing its subject.
package builder

import "errors"

// Sentinel errors. The HTTP layer maps these onto stable error codes, so a
// caller can switch on the code rather than on message text.
var (
	// ErrUnknownType means no builder is registered under that name.
	ErrUnknownType = errors.New("unknown payload type")
	// ErrInvalidPayload means the payload is structurally wrong for this
	// builder: an unknown field, a value of the wrong type, or a value that
	// violates the format's rules.
	ErrInvalidPayload = errors.New("payload invalid for this type")
	// ErrMissingField means a field the format requires was absent or empty.
	ErrMissingField = errors.New("required field missing")
)

// Field describes one accepted payload field. The slice a builder returns from
// Fields is what /v1/build serves as documentation and what validation uses to
// reject unknown keys, so the two can never drift apart.
type Field struct {
	// Name is the payload key, snake_case to match both JSON bodies and
	// query parameters.
	Name string `json:"name"`
	// Type is the accepted value type: string, bool, or number. Query
	// parameters arrive as strings and are coerced, so "bool" also accepts
	// "true"/"false".
	Type string `json:"type"`
	// Description is one line of prose for the API docs.
	Description string `json:"description"`
	// Required reports whether Build fails without this field.
	Required bool `json:"required"`
	// Example is a value that is valid on its own and valid alongside every
	// other field's Example. It is empty for a field that conflicts with
	// another field's example, which is why the docs carry the sample in the
	// description instead.
	Example string `json:"example,omitempty"`
}

// Field value types, used in Field.Type.
const (
	// TypeString is a text field.
	TypeString = "string"
	// TypeBool is a boolean field; strings such as "true" are coerced.
	TypeBool = "bool"
	// TypeNumber is a numeric field; numeric strings are coerced.
	TypeNumber = "number"
)

// Builder converts a structured payload to and from the raw string encoded
// into a code.
//
// Implementations must be safe for concurrent use: one instance is shared
// across every request and holds no per-build state.
type Builder interface {
	// Name is the registry key.
	Name() string
	// Build renders the payload as the raw string to encode.
	Build(payload any) (string, error)
	// Parse recovers a payload from a raw string. ok is false when the string
	// is not of this builder's form.
	Parse(raw string) (payload any, ok bool)
	// Fields describes the accepted payload fields, for /v1/build docs and
	// validation.
	Fields() []Field
}
