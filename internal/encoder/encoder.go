// Package encoder turns a data string into a Matrix of modules.
//
// Encoders are registered by name in an init function and looked up through
// the registry, never through a switch statement in a caller. Adding a
// symbology is therefore one new file plus one Register call.
//
// Every encoder also publishes Capabilities, which is what /v1/symbologies
// serves. Capabilities are honest about a given build: a symbology that needs
// the optional zint linkage registers itself as unavailable with a reason
// rather than disappearing from the list.
package encoder

import "errors"

// Sentinel errors. The HTTP layer maps these onto stable error codes, so a
// caller can switch on the code rather than on message text.
var (
	// ErrUnknownSymbology means no encoder is registered under that name.
	ErrUnknownSymbology = errors.New("unknown symbology")
	// ErrUnavailable means the symbology is known but not compiled into this
	// build. Capabilities.Reason explains what is missing.
	ErrUnavailable = errors.New("symbology unavailable in this build")
	// ErrDataTooLong means the payload exceeds what the symbology can carry.
	ErrDataTooLong = errors.New("data too long for this symbology")
	// ErrInvalidData means the payload violates the symbology's alphabet,
	// length, or check-digit rules.
	ErrInvalidData = errors.New("data invalid for this symbology")
	// ErrUnsupportedOption means the option is meaningful in general but not
	// honoured by this encoder.
	ErrUnsupportedOption = errors.New("option not supported by this symbology")
)

// Kind distinguishes linear symbologies from two-dimensional ones. It drives
// rendering: a 1D matrix is one module tall and is extruded to a bar height,
// and may carry human-readable interpretation text beneath the bars.
type Kind string

// Symbology kinds.
const (
	// Kind1D is a linear barcode such as EAN-13 or Code 128.
	Kind1D Kind = "1d"
	// Kind2D is a matrix symbology such as QR or Data Matrix.
	Kind2D Kind = "2d"
)

// Capabilities describes what a symbology accepts, for /v1/symbologies and
// for pre-flight validation.
type Capabilities struct {
	// Name is the registry key, e.g. "qr".
	Name string `json:"name"`
	// Title is the human-readable name, e.g. "QR Code".
	Title string `json:"title"`
	// Kind is 1d or 2d.
	Kind Kind `json:"kind"`
	// Available reports whether this build can actually encode it.
	Available bool `json:"available"`
	// Reason explains an Available:false, e.g. "requires full build".
	Reason string `json:"reason,omitempty"`
	// ECCLevels lists accepted error-correction levels, strongest last.
	ECCLevels []string `json:"ecc_levels,omitempty"`
	// Charset describes the accepted alphabet in prose.
	Charset string `json:"charset"`
	// MaxLength is the largest payload in characters, 0 when unbounded in
	// practice or when the limit depends on the character mix.
	MaxLength int `json:"max_length,omitempty"`
	// FixedLengths lists the exact input lengths accepted, if the symbology
	// requires one. Empty means variable length.
	FixedLengths []int `json:"fixed_lengths,omitempty"`
	// QuietZone is the margin the specification requires, in modules.
	QuietZone int `json:"quiet_zone"`
	// HRI reports whether human-readable text is normally printed with the
	// code.
	HRI bool `json:"hri"`
	// Notes records build-specific limitations, e.g. an option this encoder
	// accepts only in its automatic form.
	Notes string `json:"notes,omitempty"`
}

// EncodeOpts are the symbology-level options for a single encode.
//
// The zero value asks for every automatic default, which is what a caller who
// supplies no encode options gets.
type EncodeOpts struct {
	// ECC is the error-correction level. Empty means the symbology default.
	ECC string
	// Version pins the symbology version or size. Zero means automatic.
	Version int
	// Mask pins the data-mask pattern. Negative means automatic; the zero
	// value is normalised to automatic by Normalise.
	Mask int
	// QuietZone overrides the specified margin, in modules. Negative means
	// use the symbology default.
	QuietZone int
}

// AutoEncodeOpts returns EncodeOpts with every field set to its automatic
// value. Callers building options from a request should start here so that
// "unset" is distinguishable from "explicitly zero".
func AutoEncodeOpts() EncodeOpts {
	return EncodeOpts{Mask: -1, QuietZone: -1}
}

// Encoder converts a data string into a module Matrix.
//
// Implementations must be safe for concurrent use: they are shared across all
// requests and must hold no per-encode state.
type Encoder interface {
	// Name is the registry key.
	Name() string
	// Caps describes what this encoder accepts.
	Caps() Capabilities
	// Encode produces the module grid, without a quiet zone. The quiet zone
	// is applied by the renderer so that style can widen it.
	Encode(data string, o EncodeOpts) (Matrix, error)
}
