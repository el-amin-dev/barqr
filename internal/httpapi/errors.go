package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// Error codes. These are the stable contract: a client switches on the code,
// never on the message, so a code is never renamed once released.
const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeUnknownField     = "UNKNOWN_FIELD"
	CodeInvalidValue     = "INVALID_VALUE"
	CodeMissingData      = "MISSING_DATA"
	CodeUnknownType      = "UNKNOWN_TYPE"
	CodeInvalidPayload   = "INVALID_PAYLOAD"
	CodeUnknownSymbology = "UNKNOWN_SYMBOLOGY"
	CodeUnavailable      = "SYMBOLOGY_UNAVAILABLE"
	CodeDataTooLong      = "DATA_TOO_LONG"
	CodeInvalidData      = "INVALID_DATA"
	CodeUnsupported      = "UNSUPPORTED_OPTION"
	CodeUnknownFormat    = "UNKNOWN_FORMAT"
	CodeUnknownShape     = "UNKNOWN_SHAPE"
	CodeInvalidColor     = "INVALID_COLOR"
	CodeCanvasTooLarge   = "CANVAS_TOO_LARGE"
	CodeUnscannable      = "UNSCANNABLE"
	CodeBodyTooLarge     = "BODY_TOO_LARGE"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeRateLimited      = "RATE_LIMITED"
	CodeTimeout          = "TIMEOUT"
	CodeOverloaded       = "OVERLOADED"
	CodeNotFound         = "NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeInternal         = "INTERNAL"
)

// Fault is the single error shape barqr returns, from every endpoint and every
// layer. It is deliberately flat and self-describing: a caller should be able
// to fix their request from the response alone, without reading the docs.
//
// It never carries a stack trace, a file path, an environment value, or an
// internal type name.
type Fault struct {
	// Code is the stable machine-readable identifier.
	Code string `json:"code"`
	// Message is a human-readable summary.
	Message string `json:"message"`
	// Field is the offending request field in dot notation, when there is one.
	Field string `json:"field,omitempty"`
	// Expected and Got describe the mismatch.
	Expected string `json:"expected,omitempty"`
	Got      string `json:"got,omitempty"`
	// Hint is the next thing to try.
	Hint string `json:"hint,omitempty"`
	// RequestID correlates with the server log line.
	RequestID string `json:"request_id,omitempty"`

	// status is the HTTP status to serve this with. It is not serialised:
	// the status is already on the response.
	status int
}

// Error makes Fault an error so handlers can return it directly.
func (f *Fault) Error() string {
	if f.Field != "" {
		return fmt.Sprintf("%s: %s (field %s)", f.Code, f.Message, f.Field)
	}
	return fmt.Sprintf("%s: %s", f.Code, f.Message)
}

// Status is the HTTP status this fault is served with.
func (f *Fault) Status() int {
	if f.status == 0 {
		return http.StatusInternalServerError
	}
	return f.status
}

// faultEnvelope is the wire form: the fault always nests under "error" so a
// successful JSON body and an error body can never be confused.
type faultEnvelope struct {
	Error *Fault `json:"error"`
}

// newFault builds a fault with a status and code.
func newFault(status int, code, format string, args ...any) *Fault {
	return &Fault{Code: code, Message: fmt.Sprintf(format, args...), status: status}
}

// withField returns a copy of the fault annotated with the offending field.
func (f *Fault) withField(field string) *Fault {
	out := *f
	out.Field = field
	return &out
}

// asFault maps an error from any layer onto the wire shape.
//
// It walks the sentinel errors each package exports rather than inspecting
// message text, so a reworded error can never change a client-visible code.
// Anything unrecognised becomes a generic 500 with the detail logged, not
// returned: an unmapped error is a barqr bug, and its text may name internals.
func asFault(err error) *Fault {
	var f *Fault
	if errors.As(err, &f) {
		return f
	}

	switch {
	// Builder and request layer.
	case errors.Is(err, errUnknownField):
		return newFault(http.StatusBadRequest, CodeUnknownField, "%s", err)
	case errors.Is(err, errInvalidValue):
		return newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)

	// Encoder.
	case errors.Is(err, encoder.ErrUnknownSymbology):
		return newFault(http.StatusBadRequest, CodeUnknownSymbology, "%s", err).
			withField("symbology")
	case errors.Is(err, encoder.ErrUnavailable):
		return newFault(http.StatusNotImplemented, CodeUnavailable, "%s", err).
			withField("symbology")
	case errors.Is(err, encoder.ErrDataTooLong):
		return newFault(http.StatusBadRequest, CodeDataTooLong, "%s", err).withField("data")
	case errors.Is(err, encoder.ErrInvalidData):
		return newFault(http.StatusBadRequest, CodeInvalidData, "%s", err).withField("data")
	case errors.Is(err, encoder.ErrUnsupportedOption):
		return newFault(http.StatusBadRequest, CodeUnsupported, "%s", err).withField("encode")

	// Render.
	case errors.Is(err, render.ErrUnknownShape):
		return newFault(http.StatusBadRequest, CodeUnknownShape, "%s", err).withField("style.module")
	case errors.Is(err, render.ErrInvalidColor):
		return newFault(http.StatusBadRequest, CodeInvalidColor, "%s", err).withField("style.fg")
	case errors.Is(err, render.ErrInvalidStyle):
		return newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err).withField("style")
	case errors.Is(err, render.ErrUnknownRenderer):
		return newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)

	// Writer.
	case errors.Is(err, writer.ErrUnknownFormat):
		return newFault(http.StatusBadRequest, CodeUnknownFormat, "%s", err).
			withField("output.format")
	case errors.Is(err, writer.ErrCanvasTooLarge):
		return newFault(http.StatusRequestEntityTooLarge, CodeCanvasTooLarge, "%s", err).
			withField("output.scale")
	case errors.Is(err, writer.ErrInvalidOutput):
		return newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err).withField("output")
	case errors.Is(err, writer.ErrUnsupportedOutput):
		return newFault(http.StatusBadRequest, CodeUnsupported, "%s", err).
			withField("output.format")

	default:
		return newFault(http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

// fail writes an error response in the standard shape.
//
// The original error is logged with the request id so an operator can find the
// detail; only the mapped fault reaches the client.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	f := asFault(err)
	f.RequestID = requestIDFrom(r.Context())

	level := slog.LevelWarn
	if f.Status() >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	s.log.Log(r.Context(), level, "request failed",
		slog.String("code", f.Code),
		slog.Int("status", f.Status()),
		slog.String("path", r.URL.Path),
		slog.String("error", err.Error()))

	// An error body must never be cached: the next request may well succeed.
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, f.Status(), faultEnvelope{Error: f})
}
