package writer

import (
	"encoding/json"
	"fmt"

	"github.com/el-amin-dev/barqr/internal/render"
)

// JSON is the registry name of the JSON writer.
const JSON = "json"

func init() { Register(jsonWriter{}) }

// jsonWriter emits the module grid as data rather than as a picture.
//
// It exists for callers that want to draw the code themselves — a mobile app
// with its own renderer, a test fixture, a diff in code review. The grid is
// given as one string of '0' and '1' per row rather than as nested arrays
// because that form is both a third of the bytes and readable in a diff: a
// changed row shows as a changed line.
type jsonWriter struct{}

func (jsonWriter) Name() string      { return JSON }
func (jsonWriter) MIME() string      { return "application/json" }
func (jsonWriter) Extension() string { return "json" }
func (jsonWriter) Binary() bool      { return false }

// jsonCanvas is the wire shape. Field names are snake_case to match the rest
// of the HTTP API, and the geometry fields repeat what is in modules because a
// consumer should not have to count characters to size its own canvas.
type jsonCanvas struct {
	// Symbology and Kind identify what was encoded.
	Symbology string `json:"symbology"`
	Kind      string `json:"kind"`
	// Cols and Rows are the grid size in modules, quiet zone included.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
	// QuietZone is the margin already present in Modules, in modules.
	QuietZone int `json:"quiet_zone"`
	// FG and BG are the colours as "#rrggbb", or "#rrggbbaa" when translucent.
	FG string `json:"fg"`
	BG string `json:"bg"`
	// HRI is the human-readable text of a linear code, omitted when there is
	// none.
	HRI string `json:"hri,omitempty"`
	// Modules is one string per row, '1' for a dark module, top row first.
	Modules []string `json:"modules"`
}

func (jsonWriter) Write(c render.Canvas, _ OutputOpts) ([]byte, error) {
	if err := checkTextCanvas(c); err != nil {
		return nil, err
	}

	doc := jsonCanvas{
		Symbology: c.Symbology,
		Kind:      string(c.Kind),
		Cols:      c.Cols,
		Rows:      c.Rows,
		QuietZone: c.QuietZone,
		FG:        render.HexColor(c.Style.FG),
		BG:        render.HexColor(c.Style.BG),
		HRI:       c.HRI,
		Modules:   make([]string, 0, c.Rows),
	}

	row := make([]byte, c.Cols)
	for y := range c.Rows {
		for x := range c.Cols {
			row[x] = '0'
			if c.At(x, y) {
				row[x] = '1'
			}
		}
		doc.Modules = append(doc.Modules, string(row))
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: json: %w", ErrInvalidOutput, err)
	}
	return append(out, '\n'), nil
}
