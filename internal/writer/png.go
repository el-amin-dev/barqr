package writer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/png"

	"github.com/el-amin-dev/barqr/internal/render"
)

// PNG is the registry name of the PNG writer.
const PNG = "png"

func init() { Register(pngWriter{}) }

// pngWriter encodes the rasterised canvas as a PNG.
//
// PNG is the default raster format because it is the only widely supported one
// that is both lossless and alpha-capable: a QR code is hard edges on a flat
// background, which is exactly what a lossy codec ruins and what PNG's filters
// compress to almost nothing.
type pngWriter struct{}

func (pngWriter) Name() string      { return PNG }
func (pngWriter) MIME() string      { return "image/png" }
func (pngWriter) Extension() string { return "png" }
func (pngWriter) Binary() bool      { return true }

func (pngWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	img, err := Rasterize(c, o)
	if err != nil {
		return nil, err
	}

	// Best compression costs a few milliseconds on an image this small and is
	// worth it: the result is usually cached or served many times.
	enc := png.Encoder{CompressionLevel: png.BestCompression}

	var buf bytes.Buffer
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("%w: png: %w", ErrInvalidOutput, err)
	}

	return withPhys(buf.Bytes(), o.DPI), nil
}

// pngSignature is the eight-byte file magic every PNG starts with.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// withPhys inserts a pHYs chunk recording the physical resolution.
//
// image/png has no API for ancillary chunks, so the chunk is spliced in
// directly after IHDR, where the specification requires it to sit. Without it
// a print pipeline has no idea whether a 1200-pixel code is meant to be four
// inches or twelve, and scales it to whatever it feels like.
//
// A DPI that cannot be expressed is dropped rather than approximated: an
// honestly absent chunk means "unspecified", a wrong one means "wrong".
func withPhys(raw []byte, dpi int) []byte {
	if dpi <= 0 {
		dpi = 300
	}
	// Past a million dots per inch there is no device to describe and the
	// chunk's 32-bit field would overflow, so the resolution is left
	// unrecorded rather than wrapped around to a plausible-looking lie.
	const maxDPI = 1 << 20
	if dpi > maxDPI {
		return raw
	}

	// pHYs counts pixels per metre. One metre is 10000/254 inches, and the
	// chunk cannot express a fraction, so the quotient is rounded rather than
	// truncated: at 300 DPI truncation loses about 0.4 pixels per metre.
	ppm := (int64(dpi)*10000 + 127) / 254

	// The header chunk is fixed-length, but its length field is read rather
	// than assumed so that a malformed stream is left alone instead of cut.
	const sigLen, lenLen, typeLen, crcLen = 8, 4, 4, 4
	if len(raw) < sigLen+lenLen+typeLen || !bytes.HasPrefix(raw, pngSignature) {
		return raw
	}
	ihdrLen := int(binary.BigEndian.Uint32(raw[sigLen : sigLen+lenLen]))
	if !bytes.Equal(raw[sigLen+lenLen:sigLen+lenLen+typeLen], []byte("IHDR")) {
		return raw
	}
	at := sigLen + lenLen + typeLen + ihdrLen + crcLen
	if at > len(raw) {
		return raw
	}

	chunk := make([]byte, 0, lenLen+typeLen+9+crcLen)
	chunk = binary.BigEndian.AppendUint32(chunk, 9)
	chunk = append(chunk, "pHYs"...)
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(ppm))
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(ppm))
	chunk = append(chunk, 1) // unit specifier 1: the values are per metre
	// The CRC covers the type and the data, but not the length field.
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[lenLen:]))

	out := make([]byte, 0, len(raw)+len(chunk))
	out = append(out, raw[:at]...)
	out = append(out, chunk...)
	out = append(out, raw[at:]...)
	return out
}
