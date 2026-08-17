package encoder

import (
	"fmt"
	"image"
	"strings"
	"unicode/utf8"
)

// linearMaxLength caps the payload of the variable-length linear symbologies.
// Code 39, Code 93, Codabar and the two-of-five family set no limit in their
// specifications: the real limit is how wide a symbol a scanner can sweep in
// one pass. The cap stops a request from asking for a code thousands of
// modules wide, which no reader could resolve anyway.
const linearMaxLength = 128

// linearFromBits builds the single-row Matrix of a linear symbology. A true
// entry is a bar and a false one a space; the quiet zone is left to the
// renderer so that a style can widen it.
func linearFromBits(sym string, bars []bool, quietZone int, hri string) Matrix {
	m := NewMatrix(len(bars), 1)
	m.Symbology = sym
	m.Kind = Kind1D
	m.QuietZone = quietZone
	m.HRI = hri

	for x, dark := range bars {
		m.Set(x, 0, dark)
	}
	return m
}

// linearFromImage converts an encoder library's barcode image into a Matrix.
// The library renders a linear code one pixel tall, so the first row carries
// the whole symbol and everything below it would be a copy.
func linearFromImage(sym string, img image.Image, quietZone int, hri string) Matrix {
	b := img.Bounds()
	bars := make([]bool, b.Dx())
	for x := range bars {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y).RGBA()
		bars[x] = isDark(r, g, bl)
	}
	return linearFromBits(sym, bars, quietZone, hri)
}

// gridFromImage converts a two-dimensional symbology's image into a Matrix,
// one module per pixel. The libraries emit no quiet zone, which is what Matrix
// wants.
func gridFromImage(sym string, img image.Image, quietZone int) Matrix {
	b := img.Bounds()
	m := NewMatrix(b.Dx(), b.Dy())
	m.Symbology = sym
	m.Kind = Kind2D
	m.QuietZone = quietZone

	for y := range m.Rows {
		for x := range m.Cols {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			m.Set(x, y, isDark(r, g, bl))
		}
	}
	return m
}

// gs1CheckDigit returns the GS1 modulo-10 check digit of a numeric payload.
// Weights alternate 3, 1, 3 from the rightmost payload digit, which is the one
// rule EAN-13, EAN-8, UPC-A and ITF-14 all share — they differ only in how
// many digits precede the check.
//
// The payload must already be digits-only; callers run requireDigits first.
func gs1CheckDigit(payload string) byte {
	sum, weight := 0, 3
	for i := len(payload) - 1; i >= 0; i-- {
		sum += int(payload[i]-'0') * weight
		weight = 4 - weight // alternates between 3 and 1
	}
	return byte('0' + (10-sum%10)%10)
}

// withCheckDigit normalises a GS1-style numeric payload to its full form.
//
// payloadLen digits mean "compute the check digit for me"; one more means
// "verify the one I supplied". A mismatch names the digit that was expected,
// because a caller who mistyped one digit of a GTIN needs to know which end of
// the number is wrong, not merely that something is.
func withCheckDigit(sym, data string, payloadLen int) (string, error) {
	if err := requireDigits(sym, data); err != nil {
		return "", err
	}

	switch len(data) {
	case payloadLen:
		return data + string(gs1CheckDigit(data)), nil
	case payloadLen + 1:
		want := gs1CheckDigit(data[:payloadLen])
		if got := data[payloadLen]; got != want {
			return "", fmt.Errorf("%w: %s: check digit %c is wrong, expected %c",
				ErrInvalidData, sym, got, want)
		}
		return data, nil
	default:
		return "", fmt.Errorf("%w: %s: expected %d digits, or %d including the check digit, got %d",
			ErrInvalidData, sym, payloadLen, payloadLen+1, len(data))
	}
}

// requireDigits rejects a payload that is not decimal digits throughout.
//
// Positions in these messages count characters from one, not bytes from zero,
// because the reader of the error is a person looking at their input.
func requireDigits(sym, data string) error {
	pos := 0
	for _, r := range data {
		pos++
		if r < '0' || r > '9' {
			return fmt.Errorf("%w: %s: character %d is %q, expected a digit",
				ErrInvalidData, sym, pos, r)
		}
	}
	return nil
}

// requireAlphabet rejects a payload containing a character the symbology
// cannot represent. The message quotes the character rather than listing the
// whole alphabet, which Capabilities.Charset already carries.
func requireAlphabet(sym, data, alphabet string) error {
	pos := 0
	for _, r := range data {
		pos++
		if !strings.ContainsRune(alphabet, r) {
			return fmt.Errorf("%w: %s: character %d is %q, which is outside this symbology's character set",
				ErrInvalidData, sym, pos, r)
		}
	}
	return nil
}

// requireASCII rejects anything above U+007F. The linear symbologies that claim
// "full ASCII" mean exactly that, and a UTF-8 payload would otherwise be
// encoded byte by byte into something no reader could reconstruct.
func requireASCII(sym, data string) error {
	pos := 0
	for _, r := range data {
		pos++
		if r > 127 {
			return fmt.Errorf("%w: %s: character %d is %q, which is outside ASCII",
				ErrInvalidData, sym, pos, r)
		}
	}
	return nil
}

// requireLength rejects an empty payload and one longer than the symbology
// carries. Length is counted in runes because that is the unit the encoder
// libraries count in.
func requireLength(sym, data string, maxLen int) error {
	if data == "" {
		return fmt.Errorf("%w: %s: data is empty", ErrInvalidData, sym)
	}
	if n := utf8.RuneCountInString(data); n > maxLen {
		return fmt.Errorf("%w: %s: %d characters exceeds the %d this symbology carries",
			ErrDataTooLong, sym, n, maxLen)
	}
	return nil
}

// requireAutoOpts rejects the size and mask pins. No symbology in this build
// exposes either: the encoder libraries choose both, and accepting the option
// silently would report a symbol the caller did not ask for.
func requireAutoOpts(sym string, o EncodeOpts) error {
	if o.Version != 0 {
		return fmt.Errorf("%w: %s: version must be automatic in this build", ErrUnsupportedOption, sym)
	}
	if o.Mask >= 0 {
		return fmt.Errorf("%w: %s: mask must be automatic in this build", ErrUnsupportedOption, sym)
	}
	return nil
}

// requireNoECC rejects an error-correction level on a symbology that has none
// to choose from — every linear code here, plus Data Matrix, whose ECC 200
// level is fixed by the specification.
func requireNoECC(sym, ecc string) error {
	if strings.TrimSpace(ecc) != "" {
		return fmt.Errorf("%w: %s: ecc %q: this symbology has a single fixed error-correction scheme",
			ErrUnsupportedOption, sym, ecc)
	}
	return nil
}
