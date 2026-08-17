package writer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"github.com/el-amin-dev/barqr/internal/render"
)

// JPEG is the registry name of the JPEG writer.
const JPEG = "jpeg"

// DefaultQuality is the JPEG quality used when none is given. Ninety-two is
// high enough that the ringing around a module edge stays well inside one
// module at the default scale, which is what keeps a JPEG code scannable.
const DefaultQuality = 92

func init() { Register(jpegWriter{}) }

// jpegWriter encodes the rasterised canvas as a JPEG.
//
// JPEG is a poor fit for a barcode — a lossy DCT on hard black-and-white edges
// is exactly the worst case — and is offered only because some print and
// e-commerce pipelines accept nothing else. PNG is the right default.
type jpegWriter struct{}

func (jpegWriter) Name() string      { return JPEG }
func (jpegWriter) MIME() string      { return "image/jpeg" }
func (jpegWriter) Extension() string { return "jpg" }
func (jpegWriter) Binary() bool      { return true }

// Write rasterises and encodes.
//
// JPEG has no alpha channel. A transparent or translucent background is
// therefore composited onto an opaque backdrop first: the requested background
// when it is already opaque, white otherwise. Without that step image/jpeg
// reads a fully transparent pixel as premultiplied black and the code comes
// out inverted on a black field.
func (jpegWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	quality, err := jpegQuality(o.Quality)
	if err != nil {
		return nil, err
	}

	img, err := Rasterize(c, o)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flatten(img, c.Style.BG), &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("%w: jpeg: %w", ErrInvalidOutput, err)
	}
	return buf.Bytes(), nil
}

// jpegQuality validates the requested quality, defaulting an unset one.
func jpegQuality(q int) (int, error) {
	if q == 0 {
		return DefaultQuality, nil
	}
	if q < 1 || q > 100 {
		return 0, fmt.Errorf("%w: quality %d: expected 1..100", ErrInvalidOutput, q)
	}
	return q, nil
}

// flatten composites an image onto an opaque backdrop, for formats that cannot
// carry alpha. An already-opaque background is used as the backdrop so that a
// coloured code keeps its colour; anything else falls back to white, which is
// what a scanner expects behind a code.
func flatten(img *image.NRGBA, bg color.NRGBA) *image.NRGBA {
	backdrop := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	if bg.A == 0xFF {
		backdrop = bg
	}

	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), &image.Uniform{C: backdrop}, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Over)
	return out
}
