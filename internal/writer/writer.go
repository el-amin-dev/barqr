// Package writer serialises a render.Canvas into bytes.
//
// Writers are registered by format name and looked up through the registry.
// Adding an output format is one new file plus one Register call; no caller
// switches on a format string.
//
// Sizing is resolved here rather than in render, because pixels are an output
// concern: the same Canvas becomes a 10-pixels-per-module PNG, a resolution
// independent SVG, and a grid of terminal half-blocks without being rendered
// three times.
package writer

import (
	"errors"
	"fmt"

	"github.com/el-amin-dev/barqr/internal/render"
)

// Sentinel errors. The HTTP layer maps these onto stable error codes.
var (
	// ErrUnknownFormat means no writer is registered under that name.
	ErrUnknownFormat = errors.New("unknown output format")
	// ErrCanvasTooLarge means the requested pixel dimensions exceed the
	// configured cap. It is a limit, not a bug: it exists so that
	// output.scale=99999 cannot exhaust memory.
	ErrCanvasTooLarge = errors.New("output exceeds the maximum canvas size")
	// ErrInvalidOutput means the output options are inconsistent, such as a
	// negative scale or a JPEG quality outside 1..100.
	ErrInvalidOutput = errors.New("invalid output options")
	// ErrUnsupportedOutput means the writer cannot represent this canvas, for
	// example a terminal writer asked for a logo.
	ErrUnsupportedOutput = errors.New("output format cannot represent this canvas")
)

// Unit is the measurement system for an explicit output size.
type Unit string

// Supported units.
const (
	// UnitPx sizes in pixels and ignores DPI.
	UnitPx Unit = "px"
	// UnitMM sizes in millimetres, converted through DPI.
	UnitMM Unit = "mm"
	// UnitIn sizes in inches, converted through DPI.
	UnitIn Unit = "in"
)

// DefaultScale is the pixels-per-module used when neither a scale nor an
// explicit size is given. Ten pixels per module reproduces reliably on screen
// and on a consumer printer.
const DefaultScale = 10

// OutputOpts are the format-level options for a single write.
type OutputOpts struct {
	// Format is the registry key, e.g. "png".
	Format string

	// Scale is pixels per module. Zero means derive from Size, or fall back
	// to DefaultScale.
	Scale int
	// Size is the target width of the whole image, including the quiet zone,
	// expressed in Unit. Zero means derive from Scale.
	Size float64
	// Unit interprets Size. Empty means UnitPx.
	Unit Unit
	// DPI converts physical units to pixels and is written into formats that
	// record it. Zero means 300, the usual print default.
	DPI int

	// Quality is the JPEG and WebP quality, 1..100. Zero means 92.
	Quality int

	// MaxPixels caps width*height. Zero means no cap, which only tests and
	// the CLI should use; the HTTP layer always sets it.
	MaxPixels int64

	// Filename and Attachment drive the Content-Disposition header. They are
	// carried here so one struct describes the whole output.
	Filename   string
	Attachment bool
}

// DefaultOutputOpts returns the options a request with no output section gets.
func DefaultOutputOpts(format string) OutputOpts {
	return OutputOpts{Format: format, Scale: DefaultScale, Unit: UnitPx, DPI: 300, Quality: 92}
}

// Writer serialises a Canvas.
//
// Implementations must be safe for concurrent use and must not retain the
// Canvas they are given.
type Writer interface {
	// Name is the registry key and the value of output.format.
	Name() string
	// MIME is the Content-Type to serve the result with.
	MIME() string
	// Extension is the filename suffix, without the dot.
	Extension() string
	// Binary reports whether the output is binary rather than text. It drives
	// data-URI base64 selection and terminal-safety checks.
	Binary() bool
	// Write serialises the canvas.
	Write(c render.Canvas, o OutputOpts) ([]byte, error)
}

// PixelSize resolves the output dimensions for a canvas in pixels.
//
// Precedence is explicit Size first, then Scale, then DefaultScale. The result
// is always a whole number of pixels per module: a fractional module width is
// the most common cause of a code that looks fine and scans badly, because the
// rasteriser rounds different modules differently.
func PixelSize(c render.Canvas, o OutputOpts) (w, h, scale int, err error) {
	if c.Cols <= 0 || c.Rows <= 0 {
		return 0, 0, 0, fmt.Errorf("%w: empty canvas", ErrInvalidOutput)
	}

	switch {
	case o.Size > 0:
		dpi := o.DPI
		if dpi <= 0 {
			dpi = 300
		}
		var px float64
		switch o.Unit {
		case UnitMM:
			px = o.Size / 25.4 * float64(dpi)
		case UnitIn:
			px = o.Size * float64(dpi)
		case UnitPx, "":
			px = o.Size
		default:
			return 0, 0, 0, fmt.Errorf("%w: unknown unit %q", ErrInvalidOutput, o.Unit)
		}
		scale = int(px) / c.Cols
		if scale < 1 {
			return 0, 0, 0, fmt.Errorf(
				"%w: %g%s is smaller than one pixel per module for a %d-module code",
				ErrInvalidOutput, o.Size, cmp(o.Unit), c.Cols)
		}

	case o.Scale > 0:
		scale = o.Scale

	default:
		scale = DefaultScale
	}

	if scale < 1 {
		return 0, 0, 0, fmt.Errorf("%w: scale must be at least 1", ErrInvalidOutput)
	}

	w, h = c.Cols*scale, c.Rows*scale
	if o.MaxPixels > 0 && int64(w)*int64(h) > o.MaxPixels {
		return 0, 0, 0, fmt.Errorf("%w: %dx%d exceeds %d pixels",
			ErrCanvasTooLarge, w, h, o.MaxPixels)
	}
	return w, h, scale, nil
}

// cmp renders a unit for an error message, defaulting to pixels.
func cmp(u Unit) Unit {
	if u == "" {
		return UnitPx
	}
	return u
}
