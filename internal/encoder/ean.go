package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/ean"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
)

// Registry names of the EAN/UPC retail symbologies.
const (
	// EAN13 is the registry name of the EAN-13 encoder.
	EAN13 = "ean13"
	// EAN8 is the registry name of the EAN-8 encoder.
	EAN8 = "ean8"
	// UPCA is the registry name of the UPC-A encoder.
	UPCA = "upca"
	// UPCE is the registry name of the UPC-E encoder.
	UPCE = "upce"
)

// Payload lengths, excluding the check digit. The check digit may be supplied
// for verification or left off and computed.
const (
	ean13PayloadLen = 12
	ean8PayloadLen  = 7
	upcaPayloadLen  = 11
	upcePayloadLen  = 7
)

// Quiet zones from ISO/IEC 15420, in modules. EAN-13 and UPC ask for eleven
// modules on the left and seven on the right; a Matrix carries one margin for
// both sides, so nine is the figure that keeps the pair's total right.
const (
	ean13QuietZone = 9
	ean8QuietZone  = 7
	upcQuietZone   = 9
)

func init() {
	Register(ean13Encoder{})
	Register(ean8Encoder{})
	Register(upcaEncoder{})
	Register(upceEncoder{})
}

// ean13Encoder wraps boombuler/barcode's EAN implementation.
//
// The library accepts either length and computes or verifies the check digit
// itself, but reports a mismatch as "checksum missmatch" with no hint of what
// it wanted. Check digits are normalised here instead, so the error can name
// the digit that was expected.
type ean13Encoder struct{}

func (ean13Encoder) Name() string { return EAN13 }

func (ean13Encoder) Caps() Capabilities {
	return Capabilities{
		Name:         EAN13,
		Title:        "EAN-13",
		Kind:         Kind1D,
		Available:    true,
		Charset:      "digits 0-9 only",
		FixedLengths: []int{ean13PayloadLen, ean13PayloadLen + 1},
		QuietZone:    ean13QuietZone,
		HRI:          true,
		Notes:        "supply 12 digits to have the check digit computed, or 13 to have it verified",
	}
}

func (ean13Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireAutoOpts(EAN13, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(EAN13, o.ECC); err != nil {
		return Matrix{}, err
	}

	full, err := withCheckDigit(EAN13, data, ean13PayloadLen)
	if err != nil {
		return Matrix{}, err
	}

	code, err := ean.Encode(full)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: ean13: %w", ErrInvalidData, err)
	}

	return linearFromImage(EAN13, code, ean13QuietZone, full), nil
}

// ean8Encoder is EAN-13's shorter sibling: same alphabet, same check-digit
// rule, four digits per half instead of six.
type ean8Encoder struct{}

func (ean8Encoder) Name() string { return EAN8 }

func (ean8Encoder) Caps() Capabilities {
	return Capabilities{
		Name:         EAN8,
		Title:        "EAN-8",
		Kind:         Kind1D,
		Available:    true,
		Charset:      "digits 0-9 only",
		FixedLengths: []int{ean8PayloadLen, ean8PayloadLen + 1},
		QuietZone:    ean8QuietZone,
		HRI:          true,
		Notes:        "supply 7 digits to have the check digit computed, or 8 to have it verified",
	}
}

func (ean8Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireAutoOpts(EAN8, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(EAN8, o.ECC); err != nil {
		return Matrix{}, err
	}

	full, err := withCheckDigit(EAN8, data, ean8PayloadLen)
	if err != nil {
		return Matrix{}, err
	}

	code, err := ean.Encode(full)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: ean8: %w", ErrInvalidData, err)
	}

	return linearFromImage(EAN8, code, ean8QuietZone, full), nil
}

// upcaEncoder produces UPC-A, which is EAN-13 with a leading zero.
//
// The two are the same 95-module symbol: prefixing the zero and encoding as
// EAN-13 is the encoding, not a substitution. The check digit is unaffected
// because the added digit is a zero at the weight-1 end of the sum. Only the
// human-readable text differs, and that keeps the twelve digits the caller
// supplied.
type upcaEncoder struct{}

func (upcaEncoder) Name() string { return UPCA }

func (upcaEncoder) Caps() Capabilities {
	return Capabilities{
		Name:         UPCA,
		Title:        "UPC-A",
		Kind:         Kind1D,
		Available:    true,
		Charset:      "digits 0-9 only",
		FixedLengths: []int{upcaPayloadLen, upcaPayloadLen + 1},
		QuietZone:    upcQuietZone,
		HRI:          true,
		Notes:        "supply 11 digits to have the check digit computed, or 12 to have it verified",
	}
}

func (upcaEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireAutoOpts(UPCA, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(UPCA, o.ECC); err != nil {
		return Matrix{}, err
	}

	full, err := withCheckDigit(UPCA, data, upcaPayloadLen)
	if err != nil {
		return Matrix{}, err
	}

	code, err := ean.Encode("0" + full)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: upca: %w", ErrInvalidData, err)
	}

	return linearFromImage(UPCA, code, upcQuietZone, full), nil
}

// upceEncoder produces UPC-E, the zero-suppressed 51-module symbol.
//
// boombuler/barcode has no UPC-E: it is not a shortened EAN but a different
// symbol, six digits carried by parity alone with no centre guard. Expanding
// it to UPC-A and encoding that would produce a valid but different barcode,
// so this one encoder uses gozxing's UPC-E writer instead of faking it.
//
// The check digit is normalised here rather than by the writer, because the
// writer computes it over the short form when given seven digits and over the
// expanded UPC-A when given eight — only the second is what the specification
// says.
type upceEncoder struct{}

func (upceEncoder) Name() string { return UPCE }

func (upceEncoder) Caps() Capabilities {
	return Capabilities{
		Name:         UPCE,
		Title:        "UPC-E",
		Kind:         Kind1D,
		Available:    true,
		Charset:      "digits 0-9 only; the first digit is the number system and must be 0 or 1",
		FixedLengths: []int{upcePayloadLen, upcePayloadLen + 1},
		QuietZone:    upcQuietZone,
		HRI:          true,
		Notes:        "the check digit is computed over the expanded UPC-A number, as the specification requires",
	}
}

func (upceEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireAutoOpts(UPCE, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(UPCE, o.ECC); err != nil {
		return Matrix{}, err
	}

	full, err := upceNormalise(data)
	if err != nil {
		return Matrix{}, err
	}

	// Width 0 and margin 0 ask the writer for the bare symbol at one pixel per
	// module, which is exactly what a Matrix holds; the renderer adds the quiet
	// zone.
	hints := map[gozxing.EncodeHintType]any{gozxing.EncodeHintType_MARGIN: 0}
	code, err := oned.NewUPCEWriter().Encode(full, gozxing.BarcodeFormat_UPC_E, 0, 1, hints)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: upce: %w", ErrInvalidData, err)
	}

	bars := make([]bool, code.GetWidth())
	for x := range bars {
		bars[x] = code.Get(x, 0)
	}

	return linearFromBits(UPCE, bars, upcQuietZone, full), nil
}

// upceNormalise validates a UPC-E payload and returns it with its check digit.
func upceNormalise(data string) (string, error) {
	if err := requireDigits(UPCE, data); err != nil {
		return "", err
	}
	if len(data) != upcePayloadLen && len(data) != upcePayloadLen+1 {
		return "", fmt.Errorf("%w: upce: expected %d digits, or %d including the check digit, got %d",
			ErrInvalidData, upcePayloadLen, upcePayloadLen+1, len(data))
	}
	if data[0] != '0' && data[0] != '1' {
		return "", fmt.Errorf("%w: upce: the first digit is the number system and must be 0 or 1, got %c",
			ErrInvalidData, data[0])
	}

	want := gs1CheckDigit(upceExpand(data[:upcePayloadLen]))
	if len(data) == upcePayloadLen+1 {
		if got := data[upcePayloadLen]; got != want {
			return "", fmt.Errorf("%w: upce: check digit %c is wrong, expected %c",
				ErrInvalidData, got, want)
		}
		return data, nil
	}
	return data + string(want), nil
}

// upceExpand rebuilds the eleven-digit UPC-A payload that a UPC-E symbol stands
// for.
//
// UPC-E is not a shorter number: it is a compression that removes a run of
// zeroes whose position and length the last digit records. The check digit is
// computed over the expanded number, which is why this expansion has to exist
// even though nothing else here needs the UPC-A form.
func upceExpand(seven string) string {
	sys, body := seven[:1], seven[1:]
	last := body[5:]

	switch last {
	case "0", "1", "2":
		// The last digit is the manufacturer code's third digit.
		return sys + body[:2] + last + "0000" + body[2:5]
	case "3":
		return sys + body[:3] + "00000" + body[3:5]
	case "4":
		return sys + body[:4] + "00000" + body[4:5]
	default:
		// 5 to 9: the last digit is the item number's final digit.
		return sys + body[:5] + "0000" + last
	}
}
