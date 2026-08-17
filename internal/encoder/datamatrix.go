package encoder

import (
	"fmt"

	"github.com/boombuler/barcode/datamatrix"
)

// DataMatrix is the registry name of the Data Matrix encoder.
const DataMatrix = "datamatrix"

// dmQuietZone is the margin ISO/IEC 16022 requires around a Data Matrix
// symbol: one module on every side, the smallest quiet zone of any symbology
// here. The L-shaped finder pattern does the work a wider margin does
// elsewhere.
const dmQuietZone = 1

// dmMaxBytes is the largest byte payload the largest symbol (144 by 144) can
// carry. Digit pairs pack two to a codeword, so a numeric payload may exceed
// this and still fit; the library reports that case and it is mapped to
// ErrDataTooLong.
const dmMaxBytes = 1556

func init() { Register(dmEncoder{}) }

// dmEncoder wraps boombuler/barcode's Data Matrix implementation.
//
// The library emits ECC 200 — the Reed-Solomon scheme every modern reader
// expects — and picks the smallest square symbol that fits. Neither is
// configurable, and neither should be: the older convolutional ECC levels are
// deprecated and a caller pinning a symbol size gains nothing.
type dmEncoder struct{}

func (dmEncoder) Name() string { return DataMatrix }

func (dmEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      DataMatrix,
		Title:     "Data Matrix",
		Kind:      Kind2D,
		Available: true,
		Charset:   "any UTF-8 text; digit pairs are packed two per codeword",
		MaxLength: dmMaxBytes,
		QuietZone: dmQuietZone,
		HRI:       false,
		Notes:     "ECC 200 with an automatically chosen square symbol size; rectangular sizes need the full build",
	}
}

func (dmEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if data == "" {
		return Matrix{}, fmt.Errorf("%w: datamatrix: data is empty", ErrInvalidData)
	}
	if len(data) > dmMaxBytes {
		return Matrix{}, fmt.Errorf("%w: datamatrix: %d bytes exceeds the %d-byte capacity of the "+
			"largest symbol", ErrDataTooLong, len(data), dmMaxBytes)
	}
	if err := requireAutoOpts(DataMatrix, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(DataMatrix, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := datamatrix.Encode(data)
	if err != nil {
		// The only failure the library reports is running out of symbol sizes.
		return Matrix{}, fmt.Errorf("%w: datamatrix: %w", ErrDataTooLong, err)
	}

	return gridFromImage(DataMatrix, code, dmQuietZone), nil
}
