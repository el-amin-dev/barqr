package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/code39"
)

// Code39 is the registry name of the Code 39 encoder.
const Code39 = "code39"

// code39QuietZone is the margin ISO/IEC 16388 requires on each side, in
// modules: ten narrow elements.
const code39QuietZone = 10

// code39Alphabet is the 43-character set of ISO/IEC 16388. The start and stop
// character '*' is added by the encoder and is deliberately absent here, so a
// payload containing one is rejected rather than closing the symbol early.
const code39Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-. $/+%"

func init() { Register(code39Encoder{}) }

// code39Encoder wraps boombuler/barcode's Code 39 implementation.
//
// Two deliberate choices here. Full-ASCII mode is off: it encodes a lowercase
// letter as a two-character escape, so a payload would silently become twice as
// wide and read back only on a reader configured for the extension. And the
// modulo-43 check character is off, which is the common configuration — most
// Code 39 deployments carry no check character at all, and EncodeOpts has no
// per-symbology flag with which to ask for one.
type code39Encoder struct{}

func (code39Encoder) Name() string { return Code39 }

func (code39Encoder) Caps() Capabilities {
	return Capabilities{
		Name:      Code39,
		Title:     "Code 39",
		Kind:      Kind1D,
		Available: true,
		Charset:   "digits 0-9, uppercase A-Z, space, and - . $ / + %",
		MaxLength: linearMaxLength,
		QuietZone: code39QuietZone,
		HRI:       true,
		Notes:     "encoded without the optional modulo-43 check character and without full-ASCII escapes",
	}
}

func (code39Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(Code39, data, linearMaxLength); err != nil {
		return Matrix{}, err
	}
	if err := requireAlphabet(Code39, data, code39Alphabet); err != nil {
		return Matrix{}, err
	}
	if err := requireAutoOpts(Code39, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(Code39, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := code39.Encode(data, false, false)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: code39: %w", ErrInvalidData, err)
	}

	return linearFromImage(Code39, code, code39QuietZone, data), nil
}
