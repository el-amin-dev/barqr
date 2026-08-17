package encoder

import (
	"fmt"
	"strings"

	"github.com/boombuler/barcode/aztec"
)

// Aztec is the registry name of the Aztec Code encoder.
const Aztec = "aztec"

// aztecQuietZone is the margin used around an Aztec symbol, in modules.
//
// ISO/IEC 24778 requires none: the bullseye finder sits in the middle of the
// symbol, so there is no edge pattern for a stray mark to break. One module is
// kept anyway because a code printed hard against a border, or cropped by a
// viewer, is the failure this cheaply prevents.
const aztecQuietZone = 1

// aztecMaxBytes is the largest byte payload a full-range Aztec symbol carries.
// Text and digits pack denser, so a longer payload of those may still fit; the
// library reports the overflow and it is mapped to ErrDataTooLong.
const aztecMaxBytes = 1914

// aztecDefaultLayers asks the library to choose the symbol size.
const aztecDefaultLayers = 0

func init() { Register(aztecEncoder{}) }

// aztecEncoder wraps boombuler/barcode's Aztec implementation.
//
// Aztec sizes its error correction as a percentage of the message rather than
// in named levels, so the four level names are mapped onto the percentages the
// standard's own recommendation and the common toolchains use. M is the
// default because 23 per cent is what ISO/IEC 24778 recommends for general use.
type aztecEncoder struct{}

func (aztecEncoder) Name() string { return Aztec }

func (aztecEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      Aztec,
		Title:     "Aztec Code",
		Kind:      Kind2D,
		Available: true,
		ECCLevels: []string{"L", "M", "Q", "H"},
		Charset:   "any UTF-8 text; digits and uppercase text are packed more densely",
		MaxLength: aztecMaxBytes,
		QuietZone: aztecQuietZone,
		HRI:       false,
		Notes:     "ecc levels map onto 10, 23, 36 and 50 per cent redundancy; the layer count is automatic",
	}
}

func (aztecEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if data == "" {
		return Matrix{}, fmt.Errorf("%w: aztec: data is empty", ErrInvalidData)
	}
	if len(data) > aztecMaxBytes {
		return Matrix{}, fmt.Errorf("%w: aztec: %d bytes exceeds the %d-byte capacity of an Aztec symbol",
			ErrDataTooLong, len(data), aztecMaxBytes)
	}
	if err := requireAutoOpts(Aztec, o); err != nil {
		return Matrix{}, err
	}

	ecc, err := aztecECCPercent(o.ECC)
	if err != nil {
		return Matrix{}, err
	}

	code, err := aztec.Encode([]byte(data), ecc, aztecDefaultLayers)
	if err != nil {
		// The library fails only when the message plus its check words outgrow
		// the largest symbol, which stronger error correction brings closer.
		return Matrix{}, fmt.Errorf("%w: aztec: %w", ErrDataTooLong, err)
	}

	return gridFromImage(Aztec, code, aztecQuietZone), nil
}

// aztecECCPercent maps an ECC name onto the share of the symbol given over to
// check words. An empty name selects M.
func aztecECCPercent(ecc string) (int, error) {
	switch strings.ToUpper(strings.TrimSpace(ecc)) {
	case "", "M":
		return 23, nil
	case "L":
		return 10, nil
	case "Q":
		return 36, nil
	case "H":
		return 50, nil
	default:
		return 0, fmt.Errorf("%w: aztec: ecc %q: expected L, M, Q, or H", ErrUnsupportedOption, ecc)
	}
}
