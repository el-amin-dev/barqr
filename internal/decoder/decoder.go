// Package decoder reads codes back out of an image: bytes in, data out.
//
// It is the second half of barqr's round-trip invariant. Everything else in
// the service runs one way — build a payload, encode it, render it, write it —
// and this package is the only thing that can prove the result of that chain
// is a code a scanner will actually read:
//
//	Build -> Encode -> Render -> Write(png) -> Decode -> Parse == payload
//
// Decoding is also the only place where barqr parses attacker-controlled
// binary data, so image.go is written to a different standard from the rest of
// the codebase: every limit is checked before the allocation it bounds, and no
// third-party parser is trusted not to panic.
//
// The scan itself is gozxing, a pure-Go port of ZXing. Only the formats with a
// reader in that port are decodable, and Symbologies is honest about which
// those are — it is a strict subset of what the encoder side can draw.
package decoder

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/makiuchi-d/gozxing"
)

// Sentinel errors. The HTTP layer maps these onto stable error codes, so a
// caller can switch on the code rather than on message text.
var (
	// ErrNoCodeFound means the image was read successfully but contains no
	// code any enabled reader recognised. It is the expected outcome of
	// pointing the service at a photograph, not a failure of the service.
	ErrNoCodeFound = errors.New("no code found in the image")
	// ErrUnsupportedImage means the bytes are not an image barqr can read:
	// an unknown container, a truncated file, or a non-image data: uri.
	ErrUnsupportedImage = errors.New("unsupported or malformed image")
	// ErrImageTooLarge means the input, or the image it declares, exceeds the
	// configured limits. It is a limit, not a bug: it is what stops a
	// hundred-byte decompression bomb from claiming ten gigabytes.
	ErrImageTooLarge = errors.New("image exceeds the maximum decodable size")
	// ErrDecodeFailed means a code was located but could not be read, for
	// example a checksum that does not verify or a symbol damaged past
	// recovery.
	ErrDecodeFailed = errors.New("code found but could not be decoded")
)

// Default limits, applied when Options leaves them at zero.
//
// Both are chosen from the memory a single decode costs rather than from what
// looks generous. A decoded image is roughly four bytes per pixel, and the
// scanner allocates one luminance byte plus one bit of binarised matrix on top
// of that, so eight megapixels is already about forty megabytes in flight for
// one request. That is a phone photograph at full resolution, which is the
// largest thing anyone legitimately points at a barcode scanner.
const (
	// DefaultMaxPixels caps width*height of the decoded image.
	DefaultMaxPixels int64 = 8_000_000
	// DefaultMaxBytes caps the encoded input. Eight mebibytes holds any
	// realistic photograph and is far more than a generated code needs.
	DefaultMaxBytes int64 = 8 << 20
)

// Options controls a decode. The zero value is usable and applies the default
// limits; DefaultOptions spells them out for a caller that wants to adjust one.
type Options struct {
	// TryHarder asks for a slower, more thorough scan. It is worth setting
	// for a photograph — rotated, skewed or poorly lit — and wasted on an
	// image barqr generated itself.
	TryHarder bool
	// Multi finds every code in the image rather than stopping at the first.
	Multi bool
	// Symbologies restricts the scan to these barqr symbology names. Empty
	// means every decodable symbology. Narrowing it is both faster and safer:
	// the loosest linear formats cannot then invent a code out of noise.
	Symbologies []string
	// MaxPixels caps width*height of the decoded image. Zero or negative
	// means DefaultMaxPixels.
	MaxPixels int64
	// MaxBytes caps the encoded input size. Zero or negative means
	// DefaultMaxBytes.
	MaxBytes int64
}

// DefaultOptions returns the options a decode with no overrides gets.
func DefaultOptions() Options {
	return Options{MaxPixels: DefaultMaxPixels, MaxBytes: DefaultMaxBytes}
}

// normalise fills in the automatic limits. It is applied at every entry point
// rather than once at the top, because LoadImage and DecodeImage are both
// public and neither may run without a cap.
func (o Options) normalise() Options {
	if o.MaxPixels <= 0 {
		o.MaxPixels = DefaultMaxPixels
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	return o
}

// Point is a corner position in the source image, in pixels.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Result is one decoded code.
type Result struct {
	// Symbology is the barqr registry name, e.g. "qr" or "ean13".
	Symbology string `json:"symbology"`
	// Data is the decoded payload, exactly as the symbol carried it.
	Data string `json:"data"`
	// Points are the positions the reader locked onto: finder-pattern centres
	// for a matrix code, the ends of the scanned line for a linear one. They
	// are what a caller needs to draw a box around what it found.
	Points []Point `json:"points,omitempty"`
}

// Decode finds codes in an encoded image.
//
// data is either raw image bytes or a data: uri; the two are told apart by the
// "data:" prefix, which no image format can begin with. Results are returned
// in the order they were found, which is scan order: matrix symbologies first,
// then the linear ones.
func Decode(ctx context.Context, data []byte, o Options) ([]Result, error) {
	o = o.normalise()

	if strings.HasPrefix(strings.ToLower(string(peek(data))), dataURIScheme) {
		raw, _, err := DataFromURI(string(data))
		if err != nil {
			return nil, err
		}
		data = raw
	}

	img, err := LoadImage(ctx, data, o)
	if err != nil {
		return nil, err
	}
	return DecodeImage(ctx, img, o)
}

// peek returns the first few bytes of data, for a prefix test that must not
// copy a multi-megabyte body into a string just to look at its head.
func peek(data []byte) []byte {
	return data[:min(len(data), len(dataURIScheme))]
}

// DecodeImage decodes an already-parsed image.
//
// It is the entry point for a caller that produced the image itself and has
// therefore already bounded it — the round-trip tests, and anything that has
// been through LoadImage. o.MaxPixels is still enforced: an image.Image from
// an untrusted source is no safer for having been decoded elsewhere.
func DecodeImage(ctx context.Context, img image.Image, o Options) ([]Result, error) {
	o = o.normalise()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("%w: no image", ErrUnsupportedImage)
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("%w: image is %dx%d", ErrUnsupportedImage, b.Dx(), b.Dy())
	}
	if px := int64(b.Dx()) * int64(b.Dy()); px > o.MaxPixels {
		return nil, fmt.Errorf("%w: %dx%d is %d pixels, the limit is %d",
			ErrImageTooLarge, b.Dx(), b.Dy(), px, o.MaxPixels)
	}

	formats, err := PossibleFormats(o.Symbologies)
	if err != nil {
		return nil, err
	}
	hints := buildHints(o, formats)

	var bmp *gozxing.BinaryBitmap
	if err := safely(func() error {
		var e error
		bmp, e = gozxing.NewBinaryBitmapFromImage(img)
		return e
	}); err != nil {
		if errors.Is(err, ErrUnsupportedImage) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: the image could not be binarised", ErrUnsupportedImage)
	}

	if o.Multi {
		return decodeMultiple(ctx, bmp, formats, hints)
	}
	return decodeFirst(ctx, bmp, formats, hints)
}

// buildHints assembles the gozxing hint map for one decode.
//
// PURE_BARCODE is never set. It tells gozxing the image is a clean,
// unrotated, quiet-zone-trimmed symbol and lets it skip detection entirely —
// which is true of barqr's own output and false of everything a client
// uploads. Guessing wrong makes the reader miss codes it would otherwise find,
// so the honest default is to always detect.
func buildHints(o Options, formats []gozxing.BarcodeFormat) map[gozxing.DecodeHintType]any {
	hints := map[gozxing.DecodeHintType]any{}
	if o.TryHarder {
		hints[gozxing.DecodeHintType_TRY_HARDER] = true
		// A photograph of a code is as likely to be light-on-dark as the
		// reverse, and inverting is cheap next to the harder scan already
		// asked for.
		hints[gozxing.DecodeHintType_ALSO_INVERTED] = true
	}
	if len(formats) > 0 {
		hints[gozxing.DecodeHintType_POSSIBLE_FORMATS] = formats
	}
	return hints
}

// decodeFirst returns the first code any reader recognises.
func decodeFirst(
	ctx context.Context,
	bmp *gozxing.BinaryBitmap,
	formats []gozxing.BarcodeFormat,
	hints map[gozxing.DecodeHintType]any,
) ([]Result, error) {
	res, err := scan(ctx, bmp, formats, hints)
	if err != nil {
		return nil, err
	}
	out, ok := toResult(res, 0, 0)
	if !ok {
		return nil, ErrNoCodeFound
	}
	return []Result{out}, nil
}

// scan runs every enabled reader over one bitmap and returns the first hit.
//
// Every reader is tried in turn because gozxing v0.1.1 ships no
// MultiFormatReader to do it for us. A reader reporting "not found" is
// unremarkable and the scan moves on; a reader reporting a checksum or format
// failure has seen something that looks like its symbology and could not read
// it, which is a far more useful error than a flat "nothing here" and is kept
// in case nothing else succeeds.
func scan(
	ctx context.Context,
	bmp *gozxing.BinaryBitmap,
	formats []gozxing.BarcodeFormat,
	hints map[gozxing.DecodeHintType]any,
) (*gozxing.Result, error) {
	var damaged error

	for _, nr := range buildReaders(formats, hints) {
		// Checked per reader rather than once at the top: a TRY_HARDER scan
		// across a dozen readers is the slowest thing the service does, and a
		// client that has hung up should not pay for the rest of it.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		res, err := readOne(nr, bmp, hints)
		if err == nil && res != nil {
			return res, nil
		}
		if err != nil && damaged == nil && !isNotFound(err) {
			damaged = fmt.Errorf("%w: %s: %s", ErrDecodeFailed, nr.name, readerReason(err))
		}
	}

	if damaged != nil {
		return nil, damaged
	}
	return nil, ErrNoCodeFound
}

// Bounds on the multi-code search. They are ZXing's own figures for the first
// two, and a budget of our own for the third.
const (
	// multiMaxDepth limits how many times the search subdivides. Four levels
	// already covers a sheet of stacked labels; deeper is quartering noise.
	multiMaxDepth = 4
	// multiMinDimension is the smallest strip worth re-scanning, in pixels. A
	// sliver narrower than this cannot hold a readable symbol.
	multiMinDimension = 100
	// multiScanBudget caps total scans. The subdivision fans out four ways per
	// level, so the depth limit alone permits hundreds of full scans of a
	// hostile image; this is what actually bounds the work.
	multiScanBudget = 48
)

// multiScan is the state of one multi-code search.
type multiScan struct {
	formats []gozxing.BarcodeFormat
	hints   map[gozxing.DecodeHintType]any
	// seen deduplicates by symbology and data: the same physical code is
	// found again in every sub-rectangle that still contains it.
	seen   map[string]bool
	out    []Result
	budget int
}

// decodeMultiple finds every code in the image.
//
// gozxing v0.1.1 has no GenericMultipleBarcodeReader, so this is ZXing's own
// algorithm reimplemented: decode once, then re-scan the four rectangles left
// over around what was found, recursively. It is not a detector — it cannot
// see two codes at once — it just keeps cutting away the part of the image it
// has already read until nothing recognisable is left.
func decodeMultiple(
	ctx context.Context,
	bmp *gozxing.BinaryBitmap,
	formats []gozxing.BarcodeFormat,
	hints map[gozxing.DecodeHintType]any,
) ([]Result, error) {
	s := &multiScan{
		formats: formats,
		hints:   hints,
		seen:    make(map[string]bool),
		budget:  multiScanBudget,
	}
	if err := s.walk(ctx, bmp, 0, 0, 0); err != nil {
		return nil, err
	}
	if len(s.out) == 0 {
		return nil, ErrNoCodeFound
	}
	return s.out, nil
}

// walk decodes one rectangle and recurses into what surrounds the hit.
//
// dx and dy are the offset of this rectangle within the original image, so
// that result points come back in the coordinates the caller sent us.
//
// A returned error is always fatal — a cancelled context. "Nothing here" and
// "something unreadable here" both end this branch quietly, because either can
// be true of a corner of an image whose other corners hold perfectly good
// codes.
func (s *multiScan) walk(
	ctx context.Context, bmp *gozxing.BinaryBitmap, dx, dy, depth int,
) error {
	if depth > multiMaxDepth || s.budget <= 0 {
		return nil
	}
	s.budget--

	res, err := scan(ctx, bmp, s.formats, s.hints)
	if err != nil {
		if errors.Is(err, ErrNoCodeFound) || errors.Is(err, ErrDecodeFailed) {
			return nil
		}
		return err
	}

	r, ok := toResult(res, dx, dy)
	if !ok {
		return nil
	}
	if key := r.Symbology + "\x00" + r.Data; !s.seen[key] {
		s.seen[key] = true
		s.out = append(s.out, r)
	}

	// Without result points there is nothing to cut away, and re-scanning the
	// same rectangle would find the same code until the depth limit stopped
	// it.
	box, ok := boundingBox(res.GetResultPoints())
	if !ok {
		return nil
	}

	w, h := bmp.GetWidth(), bmp.GetHeight()
	for _, sub := range surrounding(box, w, h) {
		// A crop that the luminance source refuses costs this strip and
		// nothing else: the codes already found still stand, and the other
		// three strips are still worth a look.
		crop, cropErr := bmp.Crop(sub.x, sub.y, sub.w, sub.h)
		if cropErr != nil {
			continue
		}
		if err := s.walk(ctx, crop, dx+sub.x, dy+sub.y, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// rect is a sub-rectangle of a bitmap, in pixels.
type rect struct{ x, y, w, h int }

// boundingBox returns the integer box enclosing a reader's result points. It
// reports false when no usable point was given.
func boundingBox(pts []gozxing.ResultPoint) (rect, bool) {
	minX, minY := 0.0, 0.0
	maxX, maxY := 0.0, 0.0
	found := false

	for _, p := range pts {
		if p == nil {
			continue
		}
		x, y := p.GetX(), p.GetY()
		if !found {
			minX, minY, maxX, maxY = x, y, x, y
			found = true
			continue
		}
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}
	if !found {
		return rect{}, false
	}

	x0, y0 := int(minX), int(minY)
	return rect{x: x0, y: y0, w: int(maxX) - x0, h: int(maxY) - y0}, true
}

// surrounding returns the rectangles of a w-by-h bitmap that lie outside box,
// one per side, skipping any too small to hold a code.
//
// The four strips overlap at the corners on purpose: a code sitting diagonally
// from the one just read must fall inside at least one of them, and the
// deduplication upstream makes the double coverage free.
func surrounding(box rect, w, h int) []rect {
	var out []rect
	if box.x > multiMinDimension {
		out = append(out, rect{0, 0, box.x, h})
	}
	if box.y > multiMinDimension {
		out = append(out, rect{0, 0, w, box.y})
	}
	if right := box.x + box.w; right < w-multiMinDimension {
		out = append(out, rect{right, 0, w - right, h})
	}
	if bottom := box.y + box.h; bottom < h-multiMinDimension {
		out = append(out, rect{0, bottom, w, h - bottom})
	}
	return out
}

// readOne runs a single reader under the panic backstop.
func readOne(
	nr namedReader,
	bmp *gozxing.BinaryBitmap,
	hints map[gozxing.DecodeHintType]any,
) (*gozxing.Result, error) {
	var res *gozxing.Result
	err := safely(func() error {
		var e error
		res, e = nr.reader.Decode(bmp, hints)
		return e
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// isNotFound reports whether a gozxing error simply means "this reader saw
// nothing of its own symbology here", which is the normal case for all but at
// most one reader in any scan.
func isNotFound(err error) bool {
	if errors.Is(err, ErrUnsupportedImage) {
		// A recovered panic. Treated as "not found" so one buggy reader
		// cannot mask a code another reader would have found.
		return true
	}
	var nf gozxing.NotFoundException
	return errors.As(err, &nf)
}

// readerReason classifies a non-not-found reader error for the error message.
// The library's own text is never forwarded: it carries wrapped stack frames.
func readerReason(err error) string {
	var cs gozxing.ChecksumException
	if errors.As(err, &cs) {
		return "checksum does not verify"
	}
	var fe gozxing.FormatException
	if errors.As(err, &fe) {
		return "symbol is malformed"
	}
	return "symbol could not be read"
}

// toResult converts a gozxing result, translating its points by the offset of
// the sub-image it was found in. It reports false for a format barqr has no
// name for, which should be unreachable: only mapped formats get a reader.
func toResult(res *gozxing.Result, dx, dy int) (Result, bool) {
	if res == nil {
		return Result{}, false
	}
	name, ok := SymbologyName(res.GetBarcodeFormat())
	if !ok {
		return Result{}, false
	}

	pts := res.GetResultPoints()
	out := Result{Symbology: name, Data: res.GetText()}
	if len(pts) > 0 {
		out.Points = make([]Point, 0, len(pts))
		for _, p := range pts {
			// gozxing leaves a nil in the slice for a point it could not
			// locate, which is legal and must not be dereferenced.
			if p == nil {
				continue
			}
			out.Points = append(out.Points, Point{X: p.GetX() + float64(dx), Y: p.GetY() + float64(dy)})
		}
	}
	return out, true
}
