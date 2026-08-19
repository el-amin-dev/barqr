package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

// StandardRenderer is the registry name of the default renderer.
const StandardRenderer = "standard"

func init() { Register(standard{}) }

// standard applies the quiet zone, extrudes linear codes to their bar height,
// and marks finder patterns so writers can style them. Everything beyond that
// — which shape a module takes, what colour it is — is carried on the Canvas
// for the writer to interpret, because a vector writer and a rasteriser want
// the same decisions expressed differently.
type standard struct{}

func (standard) Name() string { return StandardRenderer }

// eyeSize is the side of a QR finder pattern in modules, and ballOffset the
// inset of its solid centre. Both are fixed by the QR specification.
const (
	eyeSize    = 7
	ballOffset = 2
	ballSize   = 3
)

func (standard) Render(m encoder.Matrix, s Style) (Canvas, error) {
	if m.Cols <= 0 || m.Rows <= 0 {
		return Canvas{}, fmt.Errorf("%w: empty matrix", ErrInvalidStyle)
	}
	if _, err := ModuleShape(s.Module); err != nil {
		return Canvas{}, err
	}
	if _, err := EyeShape(s.Eye); err != nil {
		return Canvas{}, err
	}
	if _, err := EyeShape(s.EyeBall); err != nil {
		return Canvas{}, err
	}
	if err := validateStyleExtras(s); err != nil {
		return Canvas{}, err
	}

	quiet := s.QuietZone
	if quiet < 0 {
		quiet = m.QuietZone
	}
	if quiet < 0 {
		return Canvas{}, fmt.Errorf("%w: negative quiet zone", ErrInvalidStyle)
	}

	// A linear code is one module tall in the matrix and is extruded here.
	// The default is the ISO guidance of 15% of the symbol width, floored at
	// a height that stays readable for short codes.
	rows := m.Rows
	if m.Kind == encoder.Kind1D {
		rows = s.BarHeight
		if rows <= 0 {
			rows = max(m.Cols*15/100, 20)
		}
	}

	// A frame and a caption grow the canvas outwards, never inwards. The quiet
	// zone stays exactly as wide as it was resolved to be: reusing it for
	// decoration is the classic way a framed code stops scanning, because the
	// decoder needs that clear margin to find the symbol's edge.
	pad, band := frameLayout(s)
	offset := quiet + pad

	cols := m.Cols + 2*offset
	c := Canvas{
		Cols:      cols,
		Rows:      rows + 2*offset + band,
		QuietZone: quiet,
		Style:     s,
		Symbology: m.Symbology,
		Kind:      m.Kind,
		pad:       pad,
		band:      band,
		grid:      make([]bool, cols*(rows+2*offset+band)),
	}
	if s.HRI {
		c.HRI = m.HRI
	}

	for y := range rows {
		src := y
		if m.Kind == encoder.Kind1D {
			src = 0 // every extruded row repeats the single row of bars
		}
		for x := range m.Cols {
			if m.At(x, src) {
				c.grid[(y+offset)*c.Cols+(x+offset)] = true
			}
		}
	}

	if m.Kind == encoder.Kind2D && hasFinderPatterns(m) {
		c.eye = markFinderPatterns(m, offset, c.Cols, c.Rows)
	}

	// Excavation runs last so that it can consult the finder-pattern roles and
	// refuse to clear one.
	if s.Logo != nil && s.Logo.Excavate {
		c.excavate()
	}

	return c, nil
}

// validateStyleExtras checks the decorative parts of a style that have their
// own invariants. They are validated here rather than at parse time so that a
// style built in Go — by a test, or by a caller using the package directly —
// cannot smuggle an out-of-range logo or an unknown frame kind past the
// renderer.
func validateStyleExtras(s Style) error {
	if err := s.Gradient.validate(); err != nil {
		return err
	}
	if err := s.Logo.validate(); err != nil {
		return err
	}
	if err := validateHRI(s); err != nil {
		return err
	}
	return s.Frame.validate()
}

// validateHRI checks the human-readable text options.
//
// Zero is tolerated for the size and empty for the family, because a Style
// built in Go without either still has to render — that is what makes the
// zero value usable. An explicit out-of-range size is refused, since silently
// clamping it would be an option accepted and then ignored.
func validateHRI(s Style) error {
	// Stated positively so NaN is refused; see the note in httpapi's style().
	if s.HRISize != 0 && !(s.HRISize >= MinHRISize && s.HRISize <= MaxHRISize) {
		return fmt.Errorf("%w: hri size %g is outside %g..%g modules",
			ErrInvalidStyle, s.HRISize, MinHRISize, MaxHRISize)
	}
	if s.HRIFont != "" && !slices.Contains(HRIFonts(), s.HRIFont) {
		return fmt.Errorf("%w: unknown hri font %q, expected one of %s",
			ErrInvalidStyle, s.HRIFont, strings.Join(HRIFonts(), ", "))
	}
	return nil
}

// hasFinderPatterns reports whether the matrix looks like a QR symbol, which
// is the only symbology in the pure-Go set with the three-corner finder layout
// that eye styling applies to.
//
// It checks the symbology name rather than probing the grid: a data matrix
// symbol can incidentally contain a 7x7 dark ring, and mis-detecting one would
// paint an eye shape over live data.
func hasFinderPatterns(m encoder.Matrix) bool {
	return m.Symbology == "qr" && m.Cols >= 21 && m.Cols == m.Rows
}

// markFinderPatterns classifies the modules of the three finder patterns so
// that writers can paint them as whole shapes. Coordinates are translated into
// canvas space by offset, which is the quiet zone plus any frame thickness.
func markFinderPatterns(m encoder.Matrix, offset, cols, rows int) []EyeRole {
	roles := make([]EyeRole, cols*rows)

	corners := [][2]int{
		{0, 0},                // top-left
		{m.Cols - eyeSize, 0}, // top-right
		{0, m.Rows - eyeSize}, // bottom-left
	}

	for _, corner := range corners {
		ox, oy := corner[0], corner[1]
		for dy := range eyeSize {
			for dx := range eyeSize {
				x, y := ox+dx+offset, oy+dy+offset
				if x < 0 || y < 0 || x >= cols || y >= rows {
					continue
				}
				switch {
				case dx >= ballOffset && dx < ballOffset+ballSize &&
					dy >= ballOffset && dy < ballOffset+ballSize:
					roles[y*cols+x] = RoleEyeBall
				default:
					roles[y*cols+x] = RoleEyeFrame
				}
			}
		}
	}

	return roles
}
