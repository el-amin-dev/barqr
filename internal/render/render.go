// Package render turns an encoder.Matrix into a Canvas: a resolution
// independent description of a drawn code.
//
// Rendering is deliberately separate from writing. A Canvas knows which
// modules are dark, what shape and colour they take, and where the quiet zone
// is, but nothing about pixels, files, or MIME types. That split is what lets
// one render feed a rasteriser, an SVG path writer, and a terminal writer
// without any of them knowing about the others.
package render

import (
	"errors"
	"image/color"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

// Sentinel errors. The HTTP layer maps these onto stable error codes.
var (
	// ErrUnknownRenderer means no renderer is registered under that name.
	ErrUnknownRenderer = errors.New("unknown renderer")
	// ErrUnknownShape means the requested module or eye shape is not
	// registered.
	ErrUnknownShape = errors.New("unknown shape")
	// ErrInvalidColor means a colour string could not be parsed.
	ErrInvalidColor = errors.New("invalid colour")
	// ErrInvalidStyle means the style is internally inconsistent, for example
	// a negative quiet zone or a logo larger than the code.
	ErrInvalidStyle = errors.New("invalid style")
)

// Style is the appearance of a rendered code. The zero value is not usable;
// build one with DefaultStyle and override from there.
type Style struct {
	// Module names the shape each dark module takes. Must be a registered
	// module shape.
	Module string
	// Eye names the shape of the three QR finder-pattern frames, and EyeBall
	// the solid centre inside them. Ignored by symbologies without finders.
	Eye     string
	EyeBall string

	// FG and BG are the module and background colours. A fully transparent
	// BG is legal and is what "transparent" parses to.
	FG color.NRGBA
	BG color.NRGBA

	// EyeFG, when non-nil, colours the finder patterns differently from the
	// data modules.
	EyeFG *color.NRGBA

	// Gradient, when non-nil, replaces FG for data modules. Finder patterns
	// keep FG or EyeFG: a ramp that runs light across an eye costs the
	// scanner the landmark it locates the symbol with.
	Gradient *Gradient

	// Logo, when non-nil, reserves an area in the centre of the symbol and,
	// if Logo.Excavate is set, clears the modules underneath it.
	Logo *Logo

	// Frame, when non-nil, reserves a border around the code. The renderer
	// grows the canvas for it; the writer draws it.
	Frame *Frame
	// Caption is the text for the caption band beneath the code. Frame.Caption
	// takes precedence when both are set.
	Caption string

	// ECC records the error-correction level the symbol was encoded at, so
	// that style-level checks can judge how much data a logo may safely
	// destroy. It is informational only: the renderer never changes the
	// encoding, and an empty value means "unknown", which the scannability
	// checks treat conservatively.
	ECC string

	// QuietZone is the margin in modules. Negative means "use whatever the
	// symbology specifies".
	QuietZone int

	// BarHeight is the height of a linear code in modules. Ignored for 2D.
	BarHeight int
	// HRI controls whether human-readable text accompanies a linear code.
	HRI bool
	// HRISize is the height of that text in modules. Zero means
	// DefaultHRISize, so a Style built in Go without it still renders.
	HRISize float64
	// HRIFont names the type family for it. Empty means HRIFontMono.
	HRIFont string
}

// Human-readable text sizing and type families.
//
// The families are generic names rather than font names because every output
// format has to be able to honour one: a name barqr cannot draw in raster
// would be an option accepted for SVG and quietly dropped for PNG. There is
// deliberately no serif family — it is free on the vector paths and impossible
// in a bitmap cell this small, so offering it would be a promise one writer
// could not keep.
const (
	DefaultHRISize = 2.0
	MinHRISize     = 1.0
	MaxHRISize     = 8.0

	HRIFontMono = "mono"
	HRIFontSans = "sans"
)

// HRIFonts lists the type families, sorted, for validation and discovery.
func HRIFonts() []string { return []string{HRIFontMono, HRIFontSans} }

// DefaultStyle is a plain black-on-white code with the symbology's own quiet
// zone: the most scannable thing barqr can produce, and the baseline every
// other style is a deviation from.
func DefaultStyle() Style {
	return Style{
		Module:    ShapeSquare,
		Eye:       ShapeSquare,
		EyeBall:   ShapeSquare,
		FG:        color.NRGBA{A: 0xFF},
		BG:        color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
		QuietZone: -1,
		BarHeight: 0,
		HRI:       true,
		HRISize:   DefaultHRISize,
		HRIFont:   HRIFontMono,
	}
}

// Canvas is a rendered code: the module grid with its quiet zone applied,
// plus everything a writer needs to draw it.
//
// Coordinates are in modules. Cols and Rows include the quiet zone, so a
// writer can iterate the whole drawable area without adding margins itself.
type Canvas struct {
	// Cols and Rows are the drawable size in modules, quiet zone included.
	Cols, Rows int
	// QuietZone is the margin already included in Cols and Rows.
	QuietZone int
	// Style is the style as resolved, with every automatic value filled in.
	Style Style
	// Symbology and Kind describe what was rendered.
	Symbology string
	Kind      encoder.Kind
	// HRI is the human-readable text to draw beneath a linear code, or empty.
	HRI string

	// pad is the frame thickness reserved on every side, and band the height
	// of the caption strip above the frame's bottom edge. Both are outside the
	// quiet zone and are included in Cols and Rows. They are unexported
	// because a writer should ask FrameRect, CaptionRect or SymbolRect rather
	// than reconstruct the layout arithmetic itself.
	pad, band int

	// grid is row-major over Cols x Rows, quiet zone included.
	grid []bool
	// eye marks modules belonging to a finder pattern, so writers can paint
	// them with the eye shape and colour. Nil when the symbology has none.
	eye []EyeRole
}

// EyeRole classifies a module's part in a finder pattern, so that writers can
// paint finder frames and their centres differently from data modules.
type EyeRole uint8

// Module roles within a code.
const (
	// RoleData is an ordinary data or timing module.
	RoleData EyeRole = iota
	// RoleEyeFrame is part of the ring of a finder pattern.
	RoleEyeFrame
	// RoleEyeBall is part of the solid centre of a finder pattern.
	RoleEyeBall
)

// At reports whether the module at (x, y) is dark. Out-of-range coordinates
// read as light so writers can walk a padded area freely.
func (c *Canvas) At(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Cols || y >= c.Rows {
		return false
	}
	return c.grid[y*c.Cols+x]
}

// Role reports what part of the code the module at (x, y) belongs to.
func (c *Canvas) Role(x, y int) EyeRole {
	if c.eye == nil || x < 0 || y < 0 || x >= c.Cols || y >= c.Rows {
		return RoleData
	}
	return c.eye[y*c.Cols+x]
}

// ColorAt returns the colour a dark module at (x, y) should be painted with,
// honouring a distinct eye colour when one is set and a gradient fill over the
// data modules.
//
// The gradient is sampled in symbol coordinates so that the ramp spans the
// code and not the quiet zone or the frame, which would compress it into the
// middle of the canvas and change with every margin the caller picks.
func (c *Canvas) ColorAt(x, y int) color.NRGBA {
	if c.Style.EyeFG != nil && c.Role(x, y) != RoleData {
		return *c.Style.EyeFG
	}
	if c.Style.Gradient != nil && c.Role(x, y) == RoleData {
		sym := c.SymbolRect()
		if !sym.Empty() {
			return c.Style.Gradient.ColorAt(x-sym.Min.X, y-sym.Min.Y, sym.Dx(), sym.Dy())
		}
	}
	return c.Style.FG
}

// Dark counts dark modules, for scannability heuristics and tests.
func (c *Canvas) Dark() int {
	n := 0
	for _, on := range c.grid {
		if on {
			n++
		}
	}
	return n
}

// Renderer converts a Matrix into a Canvas.
//
// Implementations must be safe for concurrent use and must not retain the
// Matrix they are given.
type Renderer interface {
	// Name is the registry key.
	Name() string
	// Render applies style to a matrix.
	Render(m encoder.Matrix, s Style) (Canvas, error)
}
