package writer

import (
	"bytes"
	"fmt"

	"github.com/HugoSmits86/nativewebp"

	"github.com/el-amin-dev/barqr/internal/render"
)

// WebP is the registry name of the WebP writer.
const WebP = "webp"

func init() { Register(webpWriter{}) }

// webpWriter encodes the rasterised canvas as a lossless WebP.
//
// The encoder is nativewebp, which is pure Go and therefore keeps the build
// free of cgo and libwebp. It emits VP8L only, so every WebP barqr produces is
// lossless with full alpha — the right trade for a barcode, where lossy
// artefacts land on exactly the hard edges a scanner is looking for.
type webpWriter struct{}

func (webpWriter) Name() string      { return WebP }
func (webpWriter) MIME() string      { return "image/webp" }
func (webpWriter) Extension() string { return "webp" }
func (webpWriter) Binary() bool      { return true }

// Write rasterises and encodes.
//
// OutputOpts.Quality is validated for consistency with the JPEG writer but has
// no effect here: a lossless codec has no quality dial. Rejecting an
// out-of-range value rather than ignoring the field entirely means a caller who
// mistypes quality=1000 hears about it whichever format they asked for.
func (webpWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	if _, err := jpegQuality(o.Quality); err != nil {
		return nil, err
	}

	img, err := Rasterize(c, o)
	if err != nil {
		return nil, err
	}

	// Best compression is affordable on an image made of a few flat colours,
	// and the palette transform it enables is where most of the saving is.
	opts := nativewebp.Options{CompressionLevel: nativewebp.BestCompression}

	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, &opts); err != nil {
		return nil, fmt.Errorf("%w: webp: %w", ErrInvalidOutput, err)
	}
	return buf.Bytes(), nil
}
