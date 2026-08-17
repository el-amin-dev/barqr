package encoder

import (
	"fmt"
	"strings"

	"github.com/boombuler/barcode/codabar"
)

// Codabar is the registry name of the Codabar encoder.
const Codabar = "codabar"

// codabarQuietZone is the margin Codabar requires on each side, in modules.
const codabarQuietZone = 10

// codabarData is the set of characters Codabar carries between its start and
// stop characters.
const codabarData = "0123456789-$:/.+"

// codabarStartStop is the set of characters that may open and close a Codabar
// symbol. The four letters are interchangeable in the symbology and are used
// by conventions such as blood banking to tag what the number means.
const codabarStartStop = "ABCD"

func init() { Register(codabarEncoder{}) }

// codabarEncoder wraps boombuler/barcode's Codabar implementation.
//
// Codabar carries no check character and the library validates with a regular
// expression whose failure message quotes the whole payload back, so this
// encoder does its own validation first: a caller who forgot the start and stop
// letters needs to be told that, not that their content "can not be encoded".
type codabarEncoder struct{}

func (codabarEncoder) Name() string { return Codabar }

func (codabarEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      Codabar,
		Title:     "Codabar",
		Kind:      Kind1D,
		Available: true,
		Charset:   "digits 0-9 and - $ : / . +, opened and closed by one of A, B, C or D",
		MaxLength: linearMaxLength,
		QuietZone: codabarQuietZone,
		HRI:       true,
		Notes:     "no check character is defined by the symbology, so none is added",
	}
}

func (codabarEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if err := requireLength(Codabar, data, linearMaxLength); err != nil {
		return Matrix{}, err
	}
	if err := codabarStructure(data); err != nil {
		return Matrix{}, err
	}
	if err := requireAutoOpts(Codabar, o); err != nil {
		return Matrix{}, err
	}
	if err := requireNoECC(Codabar, o.ECC); err != nil {
		return Matrix{}, err
	}

	code, err := codabar.Encode(data)
	if err != nil {
		return Matrix{}, fmt.Errorf("%w: codabar: %w", ErrInvalidData, err)
	}

	return linearFromImage(Codabar, code, codabarQuietZone, data), nil
}

// codabarStructure checks the payload's alphabet and the start/stop letters
// that frame it, which is the rule callers most often miss.
func codabarStructure(data string) error {
	if err := requireAlphabet(Codabar, data, codabarData+codabarStartStop); err != nil {
		return err
	}

	if len(data) < 3 {
		return fmt.Errorf("%w: codabar: %q is too short: a symbol is a start character, "+
			"at least one data character, and a stop character", ErrInvalidData, data)
	}

	first, last := rune(data[0]), rune(data[len(data)-1])
	if !strings.ContainsRune(codabarStartStop, first) || !strings.ContainsRune(codabarStartStop, last) {
		return fmt.Errorf("%w: codabar: must start and stop with A, B, C or D, got %q and %q",
			ErrInvalidData, first, last)
	}

	// A start/stop letter in the middle would close the symbol early on some
	// readers, so it is rejected rather than passed through.
	for i, r := range data[1 : len(data)-1] {
		if strings.ContainsRune(codabarStartStop, r) {
			return fmt.Errorf("%w: codabar: character %d is %q, which may only appear "+
				"as the start or stop character", ErrInvalidData, i+2, r)
		}
	}
	return nil
}
