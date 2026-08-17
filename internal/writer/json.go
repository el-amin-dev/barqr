package writer

import (
	"encoding/json"
	"fmt"
	"image"

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
//
// Where the other writers paint a logo, a frame and a caption, this one
// describes them. The renderer reserves canvas space for those decorations, so
// a document that reported only the grid would leave a client with unexplained
// blank margins and no way to know what belonged in them. Describing the
// geometry is what "drawing it yourself" means for a data format.
type jsonWriter struct{}

func (jsonWriter) Name() string      { return JSON }
func (jsonWriter) MIME() string      { return "application/json" }
func (jsonWriter) Extension() string { return "json" }
func (jsonWriter) Binary() bool      { return false }

// jsonRect is a rectangle in module coordinates, with the canvas origin at its
// top-left corner.
//
// It is emitted as position plus extent rather than as two corners so that it
// reads like the canvas's own cols and rows, and a client can size a sub-canvas
// from it without subtracting anything.
type jsonRect struct {
	// X and Y are the top-left corner in modules.
	X int `json:"x"`
	Y int `json:"y"`
	// Cols and Rows are the width and height in modules.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// jsonFrame is the border reserved around the code.
type jsonFrame struct {
	// The rectangle is the frame's OUTER edge, which is the whole canvas: the
	// stroke runs Width modules inwards from it.
	jsonRect
	// Kind is the outline style: border, rounded, banner, or bubble.
	Kind string `json:"kind"`
	// Width is the stroke thickness in modules.
	Width int `json:"width"`
	// Color is the stroke colour as "#rrggbb", or "#rrggbbaa" when translucent.
	Color string `json:"color"`
}

// jsonCaption is the text band beneath the code.
type jsonCaption struct {
	// The rectangle is the band the text must stay inside. It never overlaps
	// the quiet zone.
	jsonRect
	// Text is the caption to draw.
	Text string `json:"text"`
	// Color is the text colour as "#rrggbb", or "#rrggbbaa" when translucent.
	Color string `json:"color"`
}

// jsonStop is one colour stop of a gradient.
type jsonStop struct {
	// Offset is the stop's position along the ramp, 0 to 1.
	Offset float64 `json:"offset"`
	// Color is the colour at that position, as "#rrggbb" or "#rrggbbaa".
	Color string `json:"color"`
}

// jsonGradient is the ramp that fills the data modules.
type jsonGradient struct {
	// Kind is linear or radial.
	Kind string `json:"kind"`
	// Angle is the sweep direction in degrees, measured clockwise from
	// left-to-right because the module grid's y axis points down. A radial
	// gradient ignores it.
	Angle float64 `json:"angle"`
	// Stops are the colour stops in non-decreasing offset order.
	Stops []jsonStop `json:"stops"`
}

// jsonCanvas is the wire shape. Field names are snake_case to match the rest
// of the HTTP API, and the geometry fields repeat what is in modules because a
// consumer should not have to count characters to size its own canvas.
//
// Every field here is API surface: a client parses it, so a rename is a
// breaking change and a new field must be additive.
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

	// Symbol is the code itself: quiet zone, frame and caption band all
	// excluded. A client placing anything relative to the data must use this
	// and not the canvas bounds, which drift as soon as a frame is added.
	Symbol jsonRect `json:"symbol"`
	// Logo is the area reserved in the centre of the symbol for the caller's
	// artwork, omitted when the style carries no logo. The image itself is
	// never inlined: the caller supplied it and already has it.
	Logo *jsonRect `json:"logo,omitempty"`
	// Frame is the border, omitted when the style asks for none.
	Frame *jsonFrame `json:"frame,omitempty"`
	// Caption is the text band, omitted when there is no caption.
	Caption *jsonCaption `json:"caption,omitempty"`
	// Gradient is the ramp filling the DATA modules, omitted when the style
	// has none. Finder patterns keep FG, so a client must not apply it to a
	// module the symbology treats as an eye.
	Gradient *jsonGradient `json:"gradient,omitempty"`

	// Modules is one string per row, '1' for a dark module, top row first.
	Modules []string `json:"modules"`
}

func (jsonWriter) Write(c render.Canvas, _ OutputOpts) ([]byte, error) {
	if err := checkCanvas(c); err != nil {
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
		Symbol:    jsonRectOf(c.SymbolRect()),
		Modules:   make([]string, 0, c.Rows),
	}

	// Each decoration is reported only when the renderer actually reserved
	// space for it. An absent logo emitted as a zero rectangle would be
	// indistinguishable from a logo squeezed to nothing, and a client cannot
	// tell those apart after the fact.
	if r, ok := c.LogoRect(); ok {
		rect := jsonRectOf(r)
		doc.Logo = &rect
	}
	if r, ok := c.FrameRect(); ok {
		doc.Frame = &jsonFrame{
			jsonRect: jsonRectOf(r),
			Kind:     c.Style.Frame.Kind,
			Width:    frameWidth(c),
			// frameInk and captionInk are the same resolutions the raster and
			// vector writers paint with, so a client redrawing from this
			// document lands on the colour every other format produced.
			Color: render.HexColor(frameInk(c)),
		}
	}
	if r, ok := c.CaptionRect(); ok {
		doc.Caption = &jsonCaption{
			jsonRect: jsonRectOf(r),
			Text:     c.Caption(),
			Color:    render.HexColor(captionInk(c)),
		}
	}
	if g := c.Style.Gradient; g != nil {
		doc.Gradient = jsonGradientOf(g)
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

// jsonRectOf converts a module-space rectangle to the wire form.
func jsonRectOf(r image.Rectangle) jsonRect {
	return jsonRect{X: r.Min.X, Y: r.Min.Y, Cols: r.Dx(), Rows: r.Dy()}
}

// frameWidth is the frame's stroke thickness in modules, resolving an unset
// width the way the renderer did when it reserved the space.
//
// Reporting the caller's literal zero would be worse than useless: the canvas
// grew by DefaultFrameWidth on every side regardless, and a client stroking a
// zero-width border would leave that reservation empty.
func frameWidth(c render.Canvas) int {
	if f := c.Style.Frame; f != nil && f.Width > 0 {
		return f.Width
	}
	return render.DefaultFrameWidth
}

// jsonGradientOf converts a gradient to the wire form.
func jsonGradientOf(g *render.Gradient) *jsonGradient {
	out := &jsonGradient{
		Kind:  string(g.Kind),
		Angle: g.Angle,
		Stops: make([]jsonStop, len(g.Stops)),
	}
	for i, s := range g.Stops {
		out.Stops[i] = jsonStop{Offset: s.Offset, Color: render.HexColor(s.Color)}
	}
	return out
}
