package writer

import (
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// Unicode is the registry name of the half-block writer.
const Unicode = "unicode"

func init() { Register(unicodeWriter{}) }

// unicodeWriter draws the canvas with Unicode half blocks.
//
// Each character cell carries two matrix rows: the upper half block for the
// row above, the lower half for the row below. That halves the line count and
// makes the modules square in a terminal without doubling the columns, so a
// version-10 QR fits on a normal screen where the ascii writer would not.
//
// Polarity matches the ascii writer: the block glyph is the ink. On a
// dark-background terminal the result is a negative image, which most phone
// scanners still read but none are obliged to — ansi is the reliable form.
type unicodeWriter struct{}

func (unicodeWriter) Name() string      { return Unicode }
func (unicodeWriter) MIME() string      { return "text/plain; charset=utf-8" }
func (unicodeWriter) Extension() string { return "txt" }
func (unicodeWriter) Binary() bool      { return false }

// Half-block glyphs. A cell is named for the halves that are dark.
const (
	blockFull  = '█' // both rows dark
	blockUpper = '▀' // upper row dark only
	blockLower = '▄' // lower row dark only
	blockNone  = ' ' // neither
)

func (unicodeWriter) Write(c render.Canvas, _ OutputOpts) ([]byte, error) {
	if err := checkTextCanvas(c); err != nil {
		return nil, err
	}

	var b strings.Builder
	// Three bytes per block glyph, one for the newline.
	b.Grow((c.Rows/2 + 1) * (3*c.Cols + 1))

	// An odd number of rows leaves the final cell's lower half empty; At
	// reports out-of-range coordinates as light, so no special case is needed.
	for y := 0; y < c.Rows; y += 2 {
		for x := range c.Cols {
			switch {
			case c.At(x, y) && c.At(x, y+1):
				b.WriteRune(blockFull)
			case c.At(x, y):
				b.WriteRune(blockUpper)
			case c.At(x, y+1):
				b.WriteRune(blockLower)
			default:
				b.WriteRune(blockNone)
			}
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}
