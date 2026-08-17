package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/code93"
)

// Code93 is the registry name of the Code 93 encoder.
const Code93 = "code93"

// code93QuietZone is the margin ISO/IEC 15417's Code 93 annex requires on each
// side, in modules.
const code93QuietZone = 10

// code93Alphabet is the 43-character set Code 93 shares with Code 39. The four
// shift characters that unlock full ASCII are not listed: like Code 39, this
// build stays in the base alphabet.
const code93Alphabet = code39Alphabet

func init() { Register(code93Encoder{}) }

// code93Encoder wraps boombuler/barcode's Code 93 implementation.
//
// Both check characters are requested. Code 93 defines C and K as mandatory,
// but the library's includeChecksum flag governs only K — it appends C
// unconditionally — so passing false would emit a symbol one check character
// short of the specification.
type code93Encoder struct{}

func (code93Encoder) Name() string { return Code93 }

func (code93Encoder) Caps() Capabilities {
	return Capabilities{
		Name:      Code93,
		Title:     "Code 93",
		Kind:      Kind1D,
		Available: true,
		Charset:   "digits 0-9, uppercase A-Z, space, and - . $ / + %",
		MaxLength: linearMaxLength,
		QuietZone: code93QuietZone,
		HRI:       true,
		Notes:     "the mandatory C and K check characters are added automatically; full-ASCII escapes are not used",
	}
}

func (code93Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(Code93, data, linearMaxLength); err != nil {
		return Matrix{}, err
	}
	if err := requireAlphabet(Code93, data, code93Alphabet); err != nil {
		return Matrix{}, err
	}
	if err := requireAutoOpts(Code93, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(Code93, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := code93.Encode(data, true, false)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: code93: %w", ErrInvalidData, err)
	}

	return linearFromImage(Code93, code, code93QuietZone, data), nil
}
