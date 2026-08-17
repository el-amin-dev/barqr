package decoder

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"net/url"
	"strings"

	// Decoders registered for their side effects. This list is the whole set of
	// image formats barqr will accept, and it is deliberately short: every
	// entry is a format-detection routine that untrusted bytes get to drive,
	// so an unused decoder is pure attack surface.
	_ "image/gif"  // GIF: still the common output of older QR generators.
	_ "image/jpeg" // JPEG: what a phone camera produces, so the usual upload.
	_ "image/png"  // PNG: barqr's own default output, so the round-trip case.

	_ "golang.org/x/image/bmp"  // BMP: what Windows scanning software emits.
	_ "golang.org/x/image/tiff" // TIFF: the usual format out of a flatbed scanner.
	_ "golang.org/x/image/webp" // WebP: increasingly what a browser hands over.
)

// dataURIScheme is the prefix Decode sniffs for before treating its input as
// text rather than as image bytes. No image format begins with these five
// bytes, so the test cannot misfire on a real image.
const dataURIScheme = "data:"

// LoadImage decodes untrusted image bytes under the limits in o.
//
// This is the most dangerous surface in the service: the bytes come off the
// network and drive a lot of code barqr did not write. The order of the checks
// below is the whole defence and must not be rearranged — size first, then the
// header, then and only then actual pixels.
func LoadImage(ctx context.Context, data []byte, o Options) (image.Image, error) {
	o = o.normalise()

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input, expected image bytes", ErrUnsupportedImage)
	}
	// Cheapest check first: reject an oversized body before any parsing at all.
	if int64(len(data)) > o.MaxBytes {
		return nil, fmt.Errorf("%w: input is %d bytes, the limit is %d",
			ErrImageTooLarge, len(data), o.MaxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// DecodeConfig reads the header only. This is the decompression-bomb
	// guard: a hundred-byte PNG can legally declare itself 50000x50000, and
	// decoding it would allocate ten gigabytes before anyone could object. The
	// declared size is checked here, before a single pixel exists.
	var cfg image.Config
	var format string
	err := safely(func() error {
		var e error
		cfg, format, e = image.DecodeConfig(bytes.NewReader(data))
		return e
	})
	if errors.Is(err, errParserPanic) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: not a png, jpeg, gif, webp, bmp or tiff image",
			ErrUnsupportedImage)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: %s header declares a %dx%d image",
			ErrUnsupportedImage, format, cfg.Width, cfg.Height)
	}
	if px := int64(cfg.Width) * int64(cfg.Height); px > o.MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d is %d pixels, the limit is %d",
			ErrImageTooLarge, cfg.Width, cfg.Height, px, o.MaxPixels)
	}

	var img image.Image
	err = safely(func() error {
		var e error
		img, _, e = image.Decode(bytes.NewReader(data))
		return e
	})
	if errors.Is(err, errParserPanic) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s data is truncated or corrupt",
			ErrUnsupportedImage, format)
	}

	// Decoding is the expensive step, so the context is re-read on the way out:
	// a client that gave up while a large TIFF was inflating should not also
	// pay for the scan that follows.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// The header is a claim, not a guarantee. A decoder that produced more
	// pixels than it advertised has already allocated them, but it must not
	// then get to feed them to the scanner, which allocates several bytes more
	// per pixel on top.
	b := img.Bounds()
	if px := int64(b.Dx()) * int64(b.Dy()); px > o.MaxPixels {
		return nil, fmt.Errorf("%w: decoded to %dx%d, %d pixels, the limit is %d",
			ErrImageTooLarge, b.Dx(), b.Dy(), px, o.MaxPixels)
	}

	return img, nil
}

// errParserPanic is what safely reports. It is a distinct value so a caller
// can tell a recovered panic from an ordinary parse failure and keep the
// message, rather than overwriting it with a guess about what went wrong.
var errParserPanic = fmt.Errorf("%w: the image could not be parsed", ErrUnsupportedImage)

// safely runs fn and converts a panic into errParserPanic.
//
// This is a backstop for library bugs, not a strategy. The size and header
// guards in LoadImage are what actually keep a decode bounded; this only
// ensures that a slice-bounds bug in a third-party format parser costs one
// request rather than the whole process. The recovered value is deliberately
// discarded: it routinely carries internal type names and offsets that must
// not reach a client.
func safely(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errParserPanic
		}
	}()
	return fn()
}

// DataFromURI decodes a data: URI into its bytes and its media type.
//
// Both RFC 2397 payload encodings are accepted: base64 and percent-encoding.
// Anything that is not an image/* media type is rejected here rather than
// left for the decoder, so that a text/html or application/octet-stream URI
// fails with a message that names the real problem.
func DataFromURI(s string) ([]byte, string, error) {
	if !strings.HasPrefix(strings.ToLower(s), dataURIScheme) {
		return nil, "", fmt.Errorf("%w: not a data: uri", ErrUnsupportedImage)
	}

	meta, payload, ok := strings.Cut(s[len(dataURIScheme):], ",")
	if !ok {
		return nil, "", fmt.Errorf("%w: data: uri has no comma separating its payload",
			ErrUnsupportedImage)
	}

	// RFC 2397: parameters follow the media type, and ";base64" is the last of
	// them. An absent media type defaults to text/plain, which is never an
	// image, so an empty one is rejected along with every other non-image.
	params := strings.Split(meta, ";")
	mediaType := strings.ToLower(strings.TrimSpace(params[0]))
	base64Encoded := false
	for _, p := range params[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			base64Encoded = true
		}
	}

	if !strings.HasPrefix(mediaType, "image/") {
		if mediaType == "" {
			mediaType = "text/plain"
		}
		return nil, "", fmt.Errorf("%w: data: uri carries %s, expected an image/* media type",
			ErrUnsupportedImage, mediaType)
	}

	if !base64Encoded {
		decoded, err := url.PathUnescape(payload)
		if err != nil {
			return nil, "", fmt.Errorf("%w: data: uri payload is not valid percent-encoding",
				ErrUnsupportedImage)
		}
		return []byte(decoded), mediaType, nil
	}

	// Browsers emit unpadded and line-wrapped base64 often enough that being
	// strict here would reject inputs every other tool accepts.
	payload = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(payload)
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return nil, "", fmt.Errorf("%w: data: uri payload is not valid base64",
			ErrUnsupportedImage)
	}
	return raw, mediaType, nil
}
