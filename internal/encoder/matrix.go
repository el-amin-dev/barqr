package encoder

import "fmt"

// Matrix is the module grid produced by an Encoder.
//
// A module is the smallest unit of a code: one square for a 2D symbology, one
// bar-width column for a linear one. The grid excludes the quiet zone, which
// the renderer applies, and carries no pixel dimensions — sizing is a render
// and output concern.
//
// For a linear symbology Rows is 1: the single row of bars is extruded to the
// requested bar height at render time.
type Matrix struct {
	// Cols and Rows are the grid dimensions in modules.
	Cols, Rows int
	// Symbology is the encoder that produced this grid.
	Symbology string
	// Kind distinguishes 1D from 2D, which changes how it is rendered.
	Kind Kind
	// QuietZone is the margin this symbology specifies, in modules.
	QuietZone int
	// HRI is the human-readable interpretation text printed beneath a linear
	// code. Empty for 2D symbologies and when the caller suppressed it.
	HRI string

	// modules is the grid in row-major order; true means a dark module.
	modules []bool
}

// NewMatrix allocates an all-light grid of the given size.
func NewMatrix(cols, rows int) Matrix {
	return Matrix{Cols: cols, Rows: rows, modules: make([]bool, cols*rows)}
}

// At reports whether the module at (x, y) is dark. Coordinates outside the
// grid read as light, which lets renderers walk a padded area without
// bounds-checking every access.
func (m Matrix) At(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Cols || y >= m.Rows {
		return false
	}
	return m.modules[y*m.Cols+x]
}

// Set marks the module at (x, y). It panics on an out-of-range coordinate,
// which can only be an encoder bug: request data never reaches this method.
func (m Matrix) Set(x, y int, dark bool) {
	if x < 0 || y < 0 || x >= m.Cols || y >= m.Rows {
		panic(fmt.Sprintf("encoder: Set(%d, %d) outside %dx%d matrix", x, y, m.Cols, m.Rows))
	}
	m.modules[y*m.Cols+x] = dark
}

// Modules returns the grid in row-major order. The slice aliases the matrix;
// callers must not modify it.
func (m Matrix) Modules() []bool { return m.modules }

// Dark counts the dark modules. It backs the scannability heuristics, which
// care about the light-to-dark balance of a rendered code.
func (m Matrix) Dark() int {
	n := 0
	for _, on := range m.modules {
		if on {
			n++
		}
	}
	return n
}

// String renders the grid with '#' for dark and '.' for light. It exists for
// test failure messages, not for output; the ascii writer is the real thing.
func (m Matrix) String() string {
	buf := make([]byte, 0, m.Rows*(m.Cols+1))
	for y := range m.Rows {
		for x := range m.Cols {
			if m.At(x, y) {
				buf = append(buf, '#')
			} else {
				buf = append(buf, '.')
			}
		}
		buf = append(buf, '\n')
	}
	return string(buf)
}
