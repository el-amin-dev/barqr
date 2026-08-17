package builder

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// toMap normalises a payload into the single shape every builder works with.
//
// Three forms arrive in practice: a map[string]any decoded from a JSON body,
// the map[string][]string of a parsed query string, and one of this package's
// payload structs. Normalising here means a builder never type-switches on its
// input and every field lookup shares one set of coercion rules.
func toMap(payload any) (map[string]any, error) {
	switch p := payload.(type) {
	case nil:
		return nil, fmt.Errorf("%w: payload is empty", ErrInvalidPayload)
	case map[string]any:
		return p, nil
	case map[string]string:
		out := make(map[string]any, len(p))
		for k, v := range p {
			out[k] = v
		}
		return out, nil
	case map[string][]string:
		// url.Values. A repeated parameter is a caller mistake rather than a
		// list: no field in this package is multi-valued, so the first
		// non-empty value wins and the rest are ignored deliberately.
		out := make(map[string]any, len(p))
		for k, vs := range p {
			out[k] = ""
			for _, v := range vs {
				if v != "" {
					out[k] = v
					break
				}
			}
		}
		return out, nil
	}

	return structToMap(payload)
}

// structToMap converts one of the exported payload structs into the map form,
// keyed by json tag. Zero-valued fields are skipped so that an unset optional
// field is indistinguishable from an absent map key — otherwise every struct
// payload would carry a dozen empty strings into validation.
func structToMap(payload any) (map[string]any, error) {
	v := reflect.ValueOf(payload)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, fmt.Errorf("%w: payload is empty", ErrInvalidPayload)
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: expected an object of fields, got a %s",
			ErrInvalidPayload, plainKind(v.Kind()))
	}

	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if v.Field(i).IsZero() {
			continue
		}
		out[name] = v.Field(i).Interface()
	}
	return out, nil
}

// plainKind names a Go kind in words a caller can act on. Error messages must
// never leak internal type names, so this maps everything onto a handful of
// neutral words.
func plainKind(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Invalid:
		return "null"
	default:
		if k >= reflect.Int && k <= reflect.Float64 {
			return "number"
		}
		return "value"
	}
}

// checkFields rejects any key the builder does not know, naming the offender
// and suggesting the closest declared field. Silently ignoring an unknown key
// is the worst option available: a caller who misspells "sbuject" would get a
// code with no subject and no way to find out why.
func checkFields(m map[string]any, fs []Field) error {
	known := make(map[string]bool, len(fs))
	for _, f := range fs {
		known[f.Name] = true
	}

	unknown := make([]string, 0, len(m))
	for k := range m {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// Sorted so the message is deterministic whatever order the map ranges in.
	sort.Strings(unknown)

	key := unknown[0]
	if best, ok := closestField(key, fs); ok {
		return fmt.Errorf("%w: unknown field %q, did you mean %q", ErrInvalidPayload, key, best)
	}
	return fmt.Errorf("%w: unknown field %q, expected one of %s",
		ErrInvalidPayload, key, strings.Join(fieldNames(fs), ", "))
}

// fieldNames lists declared field names in declaration order, which is the
// order the API docs present them in.
func fieldNames(fs []Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

// closestField finds the declared field nearest to key by edit distance.
//
// The distance must also be smaller than the key itself, otherwise a one-letter
// key would "match" every one-letter field and the suggestion would be noise.
func closestField(key string, fs []Field) (string, bool) {
	const maxDistance = 3

	best, bestDist := "", maxDistance+1
	for _, f := range fs {
		d := levenshtein(strings.ToLower(key), f.Name)
		if d < bestDist {
			best, bestDist = f.Name, d
		}
	}
	if bestDist > maxDistance || bestDist >= len(key) {
		return "", false
	}
	return best, true
}

// levenshtein is the classic edit distance, two rows rather than a full matrix
// because only the previous row is ever read.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}

	prev := make([]int, len(ar)+1)
	curr := make([]int, len(ar)+1)
	for i := range prev {
		prev[i] = i
	}

	for j := 1; j <= len(br); j++ {
		curr[0] = j
		for i := 1; i <= len(ar); i++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[i] = min(curr[i-1]+1, prev[i]+1, prev[i-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(ar)]
}

// str reads an optional string field. An absent key and an empty value are the
// same thing, because a query string cannot express the difference.
func str(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", nil
	}

	switch t := v.(type) {
	case string:
		return t, nil
	case json.Number:
		return t.String(), nil
	}

	// A JSON number decoded into any is a float64; accepting it here means an
	// ID sent unquoted still works instead of failing on a technicality.
	rv := reflect.ValueOf(v)
	switch {
	case rv.CanInt():
		return strconv.FormatInt(rv.Int(), 10), nil
	case rv.CanUint():
		return strconv.FormatUint(rv.Uint(), 10), nil
	case rv.CanFloat():
		return formatNumber(rv.Float()), nil
	}
	return "", fmt.Errorf("%w: field %q: expected text, got a %s",
		ErrInvalidPayload, key, plainKind(rv.Kind()))
}

// strReq reads a required string field.
func strReq(m map[string]any, key string) (string, error) {
	s, err := str(m, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, key)
	}
	return s, nil
}

// boolean reads an optional boolean field, defaulting to false.
//
// Query parameters arrive as text, so the usual spellings are accepted; a
// value that is neither is an error rather than a silent false, because
// "hidden=ture" must not quietly publish a hidden network as visible.
func boolean(m map[string]any, key string) (bool, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, nil
	}

	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "":
			return false, nil
		case "1", "t", "true", "yes", "y", "on":
			return true, nil
		case "0", "f", "false", "no", "n", "off":
			return false, nil
		}
	default:
		rv := reflect.ValueOf(v)
		if rv.CanFloat() && (rv.Float() == 0 || rv.Float() == 1) {
			return rv.Float() == 1, nil
		}
		if rv.CanInt() && (rv.Int() == 0 || rv.Int() == 1) {
			return rv.Int() == 1, nil
		}
	}
	return false, fmt.Errorf("%w: field %q: expected true or false", ErrInvalidPayload, key)
}

// number reads an optional numeric field. present is false when the key is
// absent or empty, which lets a caller tell "no altitude" from "altitude 0".
func number(m map[string]any, key string) (value float64, present bool, err error) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false, nil
	}

	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false, nil
		}
		f, convErr := strconv.ParseFloat(s, 64)
		if convErr != nil {
			return 0, false, fmt.Errorf("%w: field %q: expected a number, got %q",
				ErrInvalidPayload, key, t)
		}
		v = f
	case json.Number:
		f, convErr := t.Float64()
		if convErr != nil {
			return 0, false, fmt.Errorf("%w: field %q: expected a number, got %q",
				ErrInvalidPayload, key, t.String())
		}
		v = f
	}

	rv := reflect.ValueOf(v)
	switch {
	case rv.CanFloat():
		value = rv.Float()
	case rv.CanInt():
		value = float64(rv.Int())
	case rv.CanUint():
		value = float64(rv.Uint())
	default:
		return 0, false, fmt.Errorf("%w: field %q: expected a number, got a %s",
			ErrInvalidPayload, key, plainKind(rv.Kind()))
	}

	// NaN and the infinities have no textual form any scanner understands.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false, fmt.Errorf("%w: field %q: expected a finite number", ErrInvalidPayload, key)
	}
	return value, true, nil
}

// trimmedValues reports whether every string in a parsed payload is already
// free of surrounding whitespace.
//
// Builders that trim their text fields on the way in have to check this on the
// way out: a value of " Ada " would rebuild as "Ada", which is a different
// payload from the one Parse claimed to have recovered.
func trimmedValues(m map[string]any) bool {
	for _, v := range m {
		if s, ok := v.(string); ok && s != strings.TrimSpace(s) {
			return false
		}
	}
	return true
}

// formatNumber renders a float in the shortest form that parses back to the
// same value, so that a build/parse round trip is byte-stable.
func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// pctEscape percent-encodes everything outside RFC 3986's unreserved set.
//
// url.QueryEscape is deliberately not used: it renders a space as "+", which is
// correct only for application/x-www-form-urlencoded bodies. Mail clients and
// wallet apps read these URIs as plain RFC 3986, where "+" is a literal plus,
// so a subject of "a + b" would arrive mangled. Escaping conservatively also
// removes any chance of a payload value smuggling in a "&" or "#".
func pctEscape(s string) string {
	const upperhex = "0123456789ABCDEF"

	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if unreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

// unreserved reports whether a byte is in RFC 3986's unreserved set, which is
// the only set safe to leave alone in every URI component.
func unreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '.', c == '_', c == '~':
		return true
	default:
		return false
	}
}
