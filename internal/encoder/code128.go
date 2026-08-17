package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/code128"
)

// Code128 is the registry name of the Code 128 encoder.
const Code128 = "code128"

// code128QuietZone is the margin ISO/IEC 15417 requires on each side, in
// modules. The specification states ten modules or the bar height, whichever
// is greater; a module grid cannot know the bar height, so the renderer takes
// the fixed part and the height rule is a rendering concern.
const code128QuietZone = 10

// code128MaxLength is the longest payload this build encodes. Code 128 sets no
// limit — the encoder library stops at 80 characters, which is already wider
// than most readers resolve in a single sweep.
const code128MaxLength = 80

func init() { Register(code128Encoder{}) }

// code128Encoder wraps boombuler/barcode's Code 128 implementation.
//
// The library switches between character sets A, B and C as the payload runs,
// which is what makes Code 128 compact for digit strings, and appends the
// modulo-103 check symbol the specification makes mandatory. Neither is
// optional, so this encoder exposes no options at all.
type code128Encoder struct{}

func (code128Encoder) Name() string { return Code128 }

func (code128Encoder) Caps() Capabilities {
	return Capabilities{
		Name:      Code128,
		Title:     "Code 128",
		Kind:      Kind1D,
		Available: true,
		Charset:   "any ASCII character, 0 to 127; digit runs are packed two per symbol",
		MaxLength: code128MaxLength,
		QuietZone: code128QuietZone,
		HRI:       true,
		Notes:     "character set A/B/C switching and the modulo-103 check symbol are automatic",
	}
}

func (code128Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(Code128, data, code128MaxLength); err != nil {
		return Matrix{}, err
	}
	// The library maps runes U+00F1 to U+00F4 onto FNC1 to FNC4. Those belong
	// to GS1-128, which this build registers as unavailable, so a payload above
	// ASCII is a mistake rather than a function character.
	if err := requireASCII(Code128, data); err != nil {
		return Matrix{}, err
	}
	if err := requireAutoOpts(Code128, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(Code128, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := code128.Encode(data)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: code128: %w", ErrInvalidData, err)
	}

	return linearFromImage(Code128, code, code128QuietZone, data), nil
}
