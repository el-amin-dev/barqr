package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Sentinel errors for the request layer.
var (
	// errUnknownField means a request named a field that does not exist.
	errUnknownField = errors.New("unknown field")
	// errInvalidValue means a field exists but its value could not be parsed.
	errInvalidValue = errors.New("invalid value")
)

// payloadPrefix is the one dot-notation namespace that is open-ended: a
// builder defines its own payload keys, so `payload.*` cannot be validated
// against the Request struct and is passed through to the builder instead.
const payloadPrefix = "payload."

// maxMultipartMemory bounds what a multipart parse buffers in RAM before
// spilling to disk. barqr never wants a disk spill — the body limit
// middleware caps the whole request well below this — so it is set above the
// largest body the service accepts.
const maxMultipartMemory = 8 << 20

// pathIndex maps every dot-notation key to the struct field it sets.
//
// It is derived from the Request struct's JSON tags by reflection, so the
// query, multipart, and JSON transports cannot drift apart and a new field
// becomes reachable from all three the moment it is declared. It also gives
// the "did you mean" suggestions on an unknown field for free.
var pathIndex = buildPathIndex()

// pathField is one settable leaf of the Request struct.
type pathField struct {
	index []int
	typ   reflect.Type
}

// buildPathIndex walks Request once at init and records every leaf.
func buildPathIndex() map[string]pathField {
	out := make(map[string]pathField)
	walkPaths(reflect.TypeOf(Request{}), "", nil, out)
	return out
}

// walkPaths recurses into nested structs, joining JSON tag names with dots.
func walkPaths(t reflect.Type, prefix string, index []int, out map[string]pathField) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		key := prefix + name
		idx := append(append([]int{}, index...), i)

		ft := f.Type
		switch {
		// Recurse into plain nested structs. AutoInt is excluded because it
		// has its own string parser and is a leaf as far as callers see.
		case ft.Kind() == reflect.Struct && ft != reflect.TypeOf(AutoInt{}):
			walkPaths(ft, key+".", idx, out)
		// A map is the open-ended payload namespace, handled by applyOne. It
		// must not be indexed as a leaf, or a bare `?payload=x` would look
		// like a settable field and then silently do nothing.
		case ft.Kind() == reflect.Map:
			continue
		default:
			out[key] = pathField{index: idx, typ: ft}
		}
	}
}

// knownPaths lists every name a caller could plausibly have meant, sorted.
//
// It is the settable leaves plus the section prefixes they live under. The
// sections are included because a JSON body reports an unknown field by name
// alone: a caller who wrote "styl" instead of "style" gets a useful suggestion
// only if "style" is a candidate, even though nothing is settable at that key.
func knownPaths() []string {
	seen := make(map[string]bool, len(pathIndex)*2)
	out := make([]string, 0, len(pathIndex)*2)

	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}

	for k := range pathIndex {
		add(k)
		if i := strings.Index(k, "."); i > 0 {
			add(k[:i])
		}
	}
	add("payload")
	add("payload.<field>")

	sort.Strings(out)
	return out
}

// Decode builds a Request from any of the three transports.
//
// The order is deliberate: query parameters first, then the body. A body is
// the more specific statement of intent, so on a POST with both, the body
// wins. For multipart, file parts are applied last so an uploaded logo
// overrides a string field of the same name.
//
// defaultSymbology is what the endpoint implies — "qr" for /v1/qr, the path
// parameter for /v1/barcode/{symbology}.
func Decode(r *http.Request, defaultSymbology string) (Request, error) {
	req := Request{Symbology: defaultSymbology}
	if err := decodeOver(r, &req); err != nil {
		return Request{}, err
	}
	return req, nil
}

// decodeOver applies a request onto an existing Request, overriding only the
// fields it actually sets. It is what lets a preset supply the baseline and
// the request override it, using one decoder rather than two.
func decodeOver(r *http.Request, req *Request) error {
	if err := applyValues(req, r.URL.Query()); err != nil {
		return err
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Body == nil {
		return nil
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return newFault(http.StatusUnsupportedMediaType, CodeBadRequest,
			"unparseable Content-Type header")
	}

	switch mediaType {
	case "application/json":
		if err := applyJSON(req, r.Body); err != nil {
			return err
		}
	case "multipart/form-data":
		if err := applyMultipart(req, r); err != nil {
			return err
		}
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			return newFault(http.StatusBadRequest, CodeBadRequest,
				"malformed form body")
		}
		if err := applyValues(req, r.PostForm); err != nil {
			return err
		}
	case "":
		// A POST with a body but no Content-Type: treat it as raw data
		// rather than guessing, which is what a curl --data-binary does.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return newFault(http.StatusBadRequest, CodeBadRequest, "could not read body")
		}
		if len(body) > 0 {
			req.Data = string(body)
		}
	default:
		return newFault(http.StatusUnsupportedMediaType, CodeBadRequest,
			"unsupported Content-Type %q; use application/json, multipart/form-data, "+
				"or application/x-www-form-urlencoded", mediaType)
	}

	return nil
}

// readLimited reads a request body up to a cap, distinguishing an oversized
// body from a truncated one by asking for a single byte more than allowed.
func readLimited(r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, newFault(http.StatusBadRequest, CodeBadRequest, "could not read body")
	}
	if int64(len(body)) > limit {
		return nil, newFault(http.StatusRequestEntityTooLarge, CodeBodyTooLarge,
			"body exceeds the %d-byte limit", limit)
	}
	return body, nil
}

// applyJSON decodes a JSON body over the request, rejecting unknown fields
// with a suggestion rather than ignoring them: a silently dropped
// `output.formt` produces the wrong image and no clue why.
func applyJSON(req *Request, body io.Reader) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(req); err != nil {
		if field, ok := unknownJSONField(err); ok {
			return unknownFieldError(field)
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return newFault(http.StatusBadRequest, CodeInvalidValue,
				"field %s expects %s", typeErr.Field, typeErr.Type)
		}
		if errors.Is(err, io.EOF) {
			return nil // an empty body is legal; the query may carry everything
		}
		return newFault(http.StatusBadRequest, CodeBadRequest, "malformed JSON body")
	}

	// Reject trailing content so `{"data":"a"}{"data":"b"}` is an error
	// rather than silently using the first object.
	if dec.More() {
		return newFault(http.StatusBadRequest, CodeBadRequest,
			"body contains more than one JSON value")
	}
	return nil
}

// strictJSON decodes exactly one JSON value into v, rejecting unknown fields
// and trailing content. It is the shared strictness policy for every JSON body
// barqr accepts, so no endpoint can quietly relax it.
func strictJSON(body io.Reader, v any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("body contains more than one JSON value")
	}
	return nil
}

// unknownJSONField extracts the field name from encoding/json's unknown-field
// error, which is reported only as text.
func unknownJSONField(err error) (string, bool) {
	const prefix = `json: unknown field `
	msg := err.Error()
	i := strings.Index(msg, prefix)
	if i < 0 {
		return "", false
	}
	return strings.Trim(msg[i+len(prefix):], `"`), true
}

// applyMultipart reads form fields and then file parts, so an uploaded file
// overrides a string field with the same name.
func applyMultipart(req *Request, r *http.Request) error {
	// The body is already capped by withBodyLimit's MaxBytesReader, so the
	// parse cannot buffer more than that however many parts are declared.
	// #nosec G120 -- bounded upstream: withBodyLimit wraps the body in a MaxBytesReader at BARQR_MAX_BODY
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		return newFault(http.StatusBadRequest, CodeBadRequest, "malformed multipart body")
	}
	if r.MultipartForm == nil {
		return nil
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	if err := applyValues(req, r.MultipartForm.Value); err != nil {
		return err
	}

	for name, headers := range r.MultipartForm.File {
		if len(headers) == 0 {
			continue
		}
		data, err := readPart(headers[0])
		if err != nil {
			return err
		}
		// A file part becomes a data URI so that every downstream consumer
		// sees one representation of "an image", whether it arrived inline
		// or as an upload.
		if err := applyValues(req, url.Values{name: {data}}); err != nil {
			return err
		}
	}
	return nil
}

// maxPartBytes caps a single uploaded part. The body-limit middleware already
// bounds the whole request; this is the second line of defence for a multipart
// body that declares many parts.
const maxPartBytes = 4 << 20

// readPart reads one uploaded file and returns it as a data URI, so that
// everything downstream sees a single representation of "an image" whether it
// arrived inline in JSON or as an upload.
func readPart(fh *multipart.FileHeader) (string, error) {
	f, err := fh.Open()
	if err != nil {
		return "", newFault(http.StatusBadRequest, CodeBadRequest, "could not read uploaded file")
	}
	defer func() { _ = f.Close() }()

	// LimitReader with one spare byte so an oversized part is detected rather
	// than silently truncated into a corrupt image.
	data, err := io.ReadAll(io.LimitReader(f, maxPartBytes+1))
	if err != nil {
		return "", newFault(http.StatusBadRequest, CodeBadRequest, "could not read uploaded file")
	}
	if len(data) > maxPartBytes {
		return "", newFault(http.StatusRequestEntityTooLarge, CodeBodyTooLarge,
			"uploaded file exceeds %d bytes", maxPartBytes)
	}

	ct := fh.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(fh.Filename))
	}
	if ct == "" {
		ct = http.DetectContentType(data)
	}

	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// applyValues sets every key of a flat map onto the request.
func applyValues(req *Request, values map[string][]string) error {
	// Sort for deterministic error reporting: with two bad keys, the same
	// request must always name the same one.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rv := reflect.ValueOf(req).Elem()
	for _, key := range keys {
		vals := values[key]
		if len(vals) == 0 {
			continue
		}
		// Last value wins for a repeated key, matching how a browser and
		// net/url both behave.
		if err := applyOne(req, rv, key, vals[len(vals)-1]); err != nil {
			return err
		}
	}
	return nil
}

// applyOne sets a single dot-notation key.
func applyOne(req *Request, rv reflect.Value, key, raw string) error {
	if rest, ok := strings.CutPrefix(key, payloadPrefix); ok {
		if rest == "" {
			return unknownFieldError(key)
		}
		if req.Payload == nil {
			req.Payload = make(map[string]any)
		}
		req.Payload[rest] = raw
		return nil
	}

	field, ok := pathIndex[key]
	if !ok {
		return unknownFieldError(key)
	}
	return setField(rv.FieldByIndex(field.index), field.typ, key, raw)
}

// setField parses raw according to the field's type and assigns it.
func setField(target reflect.Value, typ reflect.Type, key, raw string) error {
	switch typ {
	case reflect.TypeOf(AutoInt{}):
		var a AutoInt
		if err := a.parse(raw); err != nil {
			return valueError(key, raw, "an integer or \"auto\"")
		}
		target.Set(reflect.ValueOf(a))
		return nil

	case reflect.TypeOf((*int)(nil)):
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return valueError(key, raw, "an integer")
		}
		target.Set(reflect.ValueOf(&n))
		return nil

	case reflect.TypeOf((*bool)(nil)):
		b, err := parseBool(raw)
		if err != nil {
			return valueError(key, raw, "a boolean")
		}
		target.Set(reflect.ValueOf(&b))
		return nil
	}

	switch typ.Kind() {
	case reflect.String:
		target.SetString(raw)
		return nil

	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return valueError(key, raw, "an integer")
		}
		target.SetInt(n)
		return nil

	case reflect.Float64:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return valueError(key, raw, "a number")
		}
		target.SetFloat(f)
		return nil

	case reflect.Bool:
		b, err := parseBool(raw)
		if err != nil {
			return valueError(key, raw, "a boolean")
		}
		target.SetBool(b)
		return nil

	case reflect.Map:
		// The payload map is handled by applyOne; any other map is a bug.
		return unknownFieldError(key)

	default:
		return unknownFieldError(key)
	}
}

// parseBool accepts the usual booleans plus a bare flag, so `?meta.attachment`
// with no value means true — which is what a URL written by hand implies.
func parseBool(raw string) (bool, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || s == "on" || s == "yes" {
		return true, nil
	}
	if s == "off" || s == "no" {
		return false, nil
	}
	return strconv.ParseBool(s)
}

// unknownFieldError reports an unknown key with the closest known key as a
// suggestion, which turns a typo from a debugging session into a one-line fix.
func unknownFieldError(key string) error {
	f := newFault(http.StatusBadRequest, CodeUnknownField, "unknown field %q", key)
	f.Field = key
	if best, ok := closest(key, knownPaths()); ok {
		f.Hint = fmt.Sprintf("did you mean %q?", best)
	} else {
		f.Hint = "see /v1/openapi.json for the accepted fields"
	}
	return f
}

// valueError reports an unparseable value, echoing what arrived.
func valueError(key, raw, expected string) error {
	f := newFault(http.StatusBadRequest, CodeInvalidValue, "field %s could not be parsed", key)
	f.Field = key
	f.Expected = expected
	f.Got = raw
	return f
}

// closest returns the nearest candidate within a small edit distance, so a
// suggestion is only offered when it is likely to be right.
//
// Each candidate is measured twice: against the whole dotted path and against
// its last segment. A JSON body reports an unknown field by its leaf name —
// encoding/json says "modul", not "style.modul" — and comparing a leaf against
// full paths would never land inside the distance limit. The suggestion is
// always the full path, which is what the caller needs to type.
func closest(key string, candidates []string) (string, bool) {
	key = strings.ToLower(key)

	best, bestDist := "", 1<<30
	for _, c := range candidates {
		lower := strings.ToLower(c)
		d := levenshtein(key, lower)
		if leaf := lower[strings.LastIndex(lower, ".")+1:]; leaf != lower {
			d = min(d, levenshtein(key, leaf))
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}

	// A third of the key's length, floored at 2: close enough to be a typo,
	// far enough to avoid suggesting "output.dpi" for "nonsense".
	limit := max(len(key)/3, 2)
	return best, bestDist <= limit
}

// levenshtein is the standard edit distance over a single rolling row.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
