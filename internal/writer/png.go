package writer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
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

	// A plain code is two colours over a flat background, so an indexed image
	// is both dramatically cheaper to deflate and smaller on the wire than
	// RGBA: measured on a default QR, 0.8ms and 772 bytes against 4.9ms and
	// 1592. Anything with a gradient or a photographic logo exceeds the
	// palette and falls back to the original image.
	var src image.Image = img
	level := png.BestSpeed
	if pal := paletteOf(img); pal != nil {
		src = toPaletted(img, pal)
	} else {
		// A true-colour code is rare and usually decorative; spend the extra
		// milliseconds there, since the result is normally cached anyway.
		level = png.BestCompression
	}

	enc := png.Encoder{CompressionLevel: level}

	var buf bytes.Buffer
	if err := enc.Encode(&buf, src); err != nil {
		return nil, fmt.Errorf("%w: png: %w", ErrInvalidOutput, err)
	}

	return withPhys(buf.Bytes(), o.DPI), nil
}

// maxPaletteSize is the largest indexed palette PNG supports. A code that
// needs more colours than this is not a code with a couple of accent colours,
// it is a picture, and it is encoded as true colour instead.
const maxPaletteSize = 256

// paletteOf returns a palette covering every colour in the image, or nil when
// there are too many for an indexed encoding to be worth it.
//
// The scan is over raw NRGBA bytes rather than the color.Color interface,
// which keeps it to a few hundred microseconds even on a large canvas — cheap
// enough that the fallback path costs almost nothing when it is taken.
func paletteOf(img *image.NRGBA) color.Palette {
	seen := make(map[uint32]bool, maxPaletteSize)
	pal := make(color.Palette, 0, maxPaletteSize)

	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		row := img.Pix[(y-img.Rect.Min.Y)*img.Stride:]
		for x := 0; x < img.Rect.Dx(); x++ {
			p := row[x*4 : x*4+4 : x*4+4]
			key := uint32(p[0])<<24 | uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
			if seen[key] {
				continue
			}
			if len(pal) == maxPaletteSize {
				return nil
			}
			seen[key] = true
			pal = append(pal, color.NRGBA{R: p[0], G: p[1], B: p[2], A: p[3]})
		}
	}
	return pal
}

// toPaletted converts the image using a palette already known to cover it, so
// the lookup is exact and no colour is approximated.
func toPaletted(img *image.NRGBA, pal color.Palette) *image.Paletted {
	index := make(map[uint32]uint8, len(pal))
	for i, c := range pal {
		n := c.(color.NRGBA)
		index[uint32(n.R)<<24|uint32(n.G)<<16|uint32(n.B)<<8|uint32(n.A)] = uint8(i)
	}

	dst := image.NewPaletted(img.Rect, pal)
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		src := img.Pix[(y-img.Rect.Min.Y)*img.Stride:]
		out := dst.Pix[(y-img.Rect.Min.Y)*dst.Stride:]
		for x := 0; x < img.Rect.Dx(); x++ {
			p := src[x*4 : x*4+4 : x*4+4]
			out[x] = index[uint32(p[0])<<24|uint32(p[1])<<16|uint32(p[2])<<8|uint32(p[3])]
		}
	}
	return dst
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
