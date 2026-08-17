package encoder

import (
	"fmt"
	"strings"

	"github.com/boombuler/barcode/pdf417"
)

// PDF417 is the registry name of the PDF417 encoder.
const PDF417 = "pdf417"

// pdf417QuietZone is the margin ISO/IEC 15438 requires on every side, in
// modules.
const pdf417QuietZone = 2

// pdf417MaxBytes is the byte capacity of the largest PDF417 symbol. Text and
// digits compact better; an overflow beyond what the chosen error-correction
// level leaves is reported by the library.
const pdf417MaxBytes = 1108

// pdf417DefaultECC is the security level used when none is named. Level 2 is
// what ISO/IEC 15438 recommends for the payload sizes a web request carries,
// and it costs eight check codewords.
const pdf417DefaultECC = 2

// PDF417 rows are wider than they are tall. The library renders each codeword
// row two pixels high; the specification asks for at least three module widths
// so that a scan line crossing at an angle still lands inside one row. Sampling
// one pixel per codeword row and re-expanding to three gets there without
// assuming the library's figure is the right one.
const (
	pdf417LibRowHeight = 2
	pdf417RowHeight    = 3
)

func init() { Register(pdf417Encoder{}) }

// pdf417Encoder wraps boombuler/barcode's PDF417 implementation.
//
// Column count and row count are derived from the payload to keep the symbol
// near a 3:1 aspect ratio, which is what the library does and what a caller
// without a printing constraint wants. Truncated PDF417 and Micro PDF417 are
// different symbologies and are not produced here.
type pdf417Encoder struct{}

func (pdf417Encoder) Name() string { return PDF417 }

func (pdf417Encoder) Caps() Capabilities {
	return Capabilities{
		Name:      PDF417,
		Title:     "PDF417",
		Kind:      Kind2D,
		Available: true,
		ECCLevels: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8"},
		Charset:   "any UTF-8 text; digits and text are packed more densely than raw bytes",
		MaxLength: pdf417MaxBytes,
		QuietZone: pdf417QuietZone,
		HRI:       false,
		Notes:     "ecc is the security level 0 to 8, defaulting to 2; the row and column counts are automatic",
	}
}

func (pdf417Encoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if data == "" {
		return Matrix{}, fmt.Errorf("%w: pdf417: data is empty", ErrInvalidData)
	}
	if len(data) > pdf417MaxBytes {
		return Matrix{}, fmt.Errorf("%w: pdf417: %d bytes exceeds the %d-byte capacity of a PDF417 symbol",
			ErrDataTooLong, len(data), pdf417MaxBytes)
	}
	if err := requireAutoOpts(PDF417, o); err != nil {
		return Matrix{}, err
	}

	level, err := pdf417Level(o.ECC)
	if err != nil {
		return Matrix{}, err
	}

	code, err := pdf417.Encode(data, level)
	if err != nil {
		// Every failure the library reports comes down to the codewords not
		// fitting the 30-by-30 maximum, which stronger error correction and
		// longer payloads both push towards.
		return Matrix{}, fmt.Errorf("%w: pdf417: %w", ErrDataTooLong, err)
	}

	b := code.Bounds()
	step := pdf417LibRowHeight
	if b.Dy()%step != 0 {
		// A library that changed its rendered row height would corrupt the grid
		// silently; falling back to one pixel per row keeps the symbol correct,
		// only shorter than the specification prefers.
		step = 1
	}

	srcRows := b.Dy() / step
	m := NewMatrix(b.Dx(), srcRows*pdf417RowHeight)
	m.Symbology = PDF417
	m.Kind = Kind2D
	m.QuietZone = pdf417QuietZone

	for row := range srcRows {
		for x := range m.Cols {
			r, g, bl, _ := code.At(b.Min.X+x, b.Min.Y+row*step).RGBA()
			dark := isDark(r, g, bl)
			for i := range pdf417RowHeight {
				m.Set(x, row*pdf417RowHeight+i, dark)
			}
		}
	}

	return m, nil
}

// pdf417Level maps an ECC name onto the library's security level. PDF417 names
// its own levels 0 to 8, each doubling the check codewords of the one before,
// so those names are used rather than inventing letters for them.
func pdf417Level(ecc string) (byte, error) {
	s := strings.TrimSpace(ecc)
	if s == "" {
		return pdf417DefaultECC, nil
	}
	if len(s) == 1 && s[0] >= '0' && s[0] <= '8' {
		return s[0] - '0', nil
	}
	return 0, fmt.Errorf("%w: pdf417: ecc %q: expected a security level from 0 to 8",
		ErrUnsupportedOption, ecc)
}
