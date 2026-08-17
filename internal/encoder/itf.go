package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/twooffive"
)

// Registry names of the two-of-five family.
const (
	// ITF is the registry name of Interleaved 2 of 5.
	ITF = "itf"
	// ITF14 is the registry name of the fixed-length GS1 shipping variant.
	ITF14 = "itf14"
	// TwoOfFive is the registry name of Standard (non-interleaved) 2 of 5.
	TwoOfFive = "2of5"
)

// itfQuietZone is the margin ISO/IEC 16390 requires on each side, in modules.
// Ten is the specification's minimum for the whole family.
const itfQuietZone = 10

// itf14PayloadLen is the GTIN-14 length without its check digit.
const itf14PayloadLen = 13

func init() {
	Register(itfEncoder{})
	Register(itf14Encoder{})
	Register(twoOfFiveEncoder{})
}

// itfEncoder wraps boombuler/barcode's interleaved two-of-five implementation.
//
// Interleaving is the whole point of the symbology: one digit is carried by the
// bars and the next by the spaces between them, which is why an odd digit count
// cannot be encoded. That trips up nearly everyone the first time, so the error
// says what to do about it.
type itfEncoder struct{}

func (itfEncoder) Name() string { return ITF }

func (itfEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      ITF,
		Title:     "Interleaved 2 of 5",
		Kind:      Kind1D,
		Available: true,
		Charset:   "digits 0-9 only, in an even count",
		MaxLength: linearMaxLength,
		QuietZone: itfQuietZone,
		HRI:       true,
		Notes:     "digits are encoded in pairs, so an odd count is rejected; no check digit is added",
	}
}

func (itfEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(ITF, data, linearMaxLength); err != nil {
		return Matrix{}, err
	}
	if err := requireDigits(ITF, data); err != nil {
		return Matrix{}, err
	}
	if len(data)%2 != 0 {
		return Matrix{}, fmt.Errorf("%w: itf: %d digits is odd: interleaved 2 of 5 encodes digits "+
			"in pairs, so pad with a leading zero", ErrInvalidData, len(data))
	}
	if err := requireAutoOpts(ITF, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(ITF, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := twooffive.Encode(data, true)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: itf: %w", ErrInvalidData, err)
	}

	return linearFromImage(ITF, code, itfQuietZone, data), nil
}

// itf14Encoder is Interleaved 2 of 5 pinned to a GTIN-14.
//
// The fourteen digits are always even, so the interleaving trap cannot arise;
// what does arise is a mistyped GTIN, which the check digit catches.
type itf14Encoder struct{}

func (itf14Encoder) Name() string { return ITF14 }

func (itf14Encoder) Caps() Capabilities {
	return Capabilities{
		Name:         ITF14,
		Title:        "ITF-14",
		Kind:         Kind1D,
		Available:    true,
		Charset:      "digits 0-9 only",
		FixedLengths: []int{itf14PayloadLen, itf14PayloadLen + 1},
		QuietZone:    itfQuietZone,
		HRI:          true,
		Notes: "supply 13 digits to have the check digit computed, or 14 to have it verified; " +
			"the bearer bars the printing standard asks for are a rendering concern",
	}
}

func (itf14Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireAutoOpts(ITF14, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(ITF14, o.ECC); err != nil {
		return Matrix{}, err
	}

	full, err := withCheckDigit(ITF14, data, itf14PayloadLen)
	if err != nil {
		return Matrix{}, err
	}

	code, err := twooffive.Encode(full, true)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: itf14: %w", ErrInvalidData, err)
	}

	return linearFromImage(ITF14, code, itfQuietZone, full), nil
}

// twoOfFiveEncoder produces Standard 2 of 5, where the spaces carry nothing and
// every digit is a run of bars. It is roughly twice as wide as the interleaved
// form for the same number, which is why it survives only in older
// installations, but it accepts any digit count.
type twoOfFiveEncoder struct{}

func (twoOfFiveEncoder) Name() string { return TwoOfFive }

func (twoOfFiveEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      TwoOfFive,
		Title:     "Standard 2 of 5",
		Kind:      Kind1D,
		Available: true,
		Charset:   "digits 0-9 only",
		MaxLength: linearMaxLength,
		QuietZone: itfQuietZone,
		HRI:       true,
		Notes:     "the non-interleaved form: any digit count is accepted and no check digit is added",
	}
}

func (twoOfFiveEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(TwoOfFive, data, linearMaxLength); err != nil {
		return Matrix{}, err
	}
	if err := requireDigits(TwoOfFive, data); err != nil {
		return Matrix{}, err
	}
	if err := requireAutoOpts(TwoOfFive, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(TwoOfFive, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := twooffive.Encode(data, false)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: 2of5: %w", ErrInvalidData, err)
	}

	return linearFromImage(TwoOfFive, code, itfQuietZone, data), nil
}
