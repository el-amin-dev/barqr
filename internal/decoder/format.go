package decoder

import (
	"fmt"
	"sort"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/aztec"
	"github.com/makiuchi-d/gozxing/datamatrix"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

// formatMap pairs every gozxing BarcodeFormat barqr cares about with the
// registry name the encoder side uses for the same symbology.
//
// It is a single list rather than two maps so the two directions cannot drift:
// a new symbology is one line here and both lookups follow.
//
// The order is the scan order used when no symbology filter is given. It is
// not arbitrary. Matrix symbologies come first because they are cheap to
// reject and almost never produce a false positive. The linear ones follow,
// loosest last: ITF and Code 39 have no mandatory check digit and the shortest
// start/stop patterns, so they are the two most likely to read noise as data
// and must only get a look once the stricter readers have declined.
var formatMap = []struct {
	format gozxing.BarcodeFormat
	name   string
}{
	{gozxing.BarcodeFormat_QR_CODE, "qr"},
	{gozxing.BarcodeFormat_DATA_MATRIX, "datamatrix"},
	{gozxing.BarcodeFormat_AZTEC, "aztec"},
	{gozxing.BarcodeFormat_PDF_417, "pdf417"},
	{gozxing.BarcodeFormat_EAN_13, "ean13"},
	{gozxing.BarcodeFormat_EAN_8, "ean8"},
	{gozxing.BarcodeFormat_UPC_A, "upca"},
	{gozxing.BarcodeFormat_UPC_E, "upce"},
	{gozxing.BarcodeFormat_CODE_128, "code128"},
	{gozxing.BarcodeFormat_CODE_93, "code93"},
	{gozxing.BarcodeFormat_CODE_39, "code39"},
	{gozxing.BarcodeFormat_ITF, "itf"},
	{gozxing.BarcodeFormat_CODABAR, "codabar"},
}

// nameByFormat and formatByName are the two lookup directions over formatMap.
// Both are built once at package initialisation and never written again.
var nameByFormat, formatByName = buildFormatIndex()

func buildFormatIndex() (map[gozxing.BarcodeFormat]string, map[string]gozxing.BarcodeFormat) {
	byFormat := make(map[gozxing.BarcodeFormat]string, len(formatMap))
	byName := make(map[string]gozxing.BarcodeFormat, len(formatMap))
	for _, e := range formatMap {
		byFormat[e.format] = e.name
		byName[e.name] = e.format
	}
	return byFormat, byName
}

// SymbologyName maps a gozxing BarcodeFormat onto the barqr registry name for
// the same symbology. The second result is false for a format barqr has no
// name for, such as MaxiCode or the RSS family.
func SymbologyName(f gozxing.BarcodeFormat) (string, bool) {
	name, ok := nameByFormat[f]
	return name, ok
}

// BarcodeFormat maps a barqr registry name onto the gozxing BarcodeFormat for
// the same symbology. The second result is false for an unknown name.
func BarcodeFormat(name string) (gozxing.BarcodeFormat, bool) {
	f, ok := formatByName[name]
	return f, ok
}

// Symbologies lists every symbology this build can decode, sorted.
//
// It is deliberately narrower than the encode side: a symbology barqr can draw
// is not automatically one it can read back, and a caller comparing the two
// lists should see the honest difference rather than a promise that fails at
// decode time.
func Symbologies() []string {
	out := make([]string, 0, len(formatMap))
	for _, e := range formatMap {
		if readerCtor(e.format) != nil {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
}

// PossibleFormats turns the caller's symbology filter into the value of the
// gozxing POSSIBLE_FORMATS hint. An empty filter returns nil, which means
// "every format this build can read" rather than "none".
//
// An unrecognised name is an error naming the offending value: silently
// ignoring it would let a typo widen the scan instead of narrowing it, which
// is the opposite of what the caller asked for.
func PossibleFormats(names []string) ([]gozxing.BarcodeFormat, error) {
	if len(names) == 0 {
		return nil, nil
	}

	out := make([]gozxing.BarcodeFormat, 0, len(names))
	seen := make(map[gozxing.BarcodeFormat]bool, len(names))

	for _, name := range names {
		f, ok := formatByName[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a decodable symbology, want one of %v",
				encoder.ErrUnknownSymbology, name, Symbologies())
		}
		if readerCtor(f) == nil {
			return nil, fmt.Errorf("%w: %q: no pure-Go reader is linked into this build",
				encoder.ErrUnavailable, name)
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// upcEAN lists the four retail formats that share one reader. They cannot be
// read independently without misreporting the symbology: a UPC-A symbol is
// bit-for-bit an EAN-13 symbol with a leading zero, and only a reader that
// knows both were requested can tell the caller which one it meant.
var upcEAN = map[gozxing.BarcodeFormat]bool{
	gozxing.BarcodeFormat_EAN_13: true,
	gozxing.BarcodeFormat_EAN_8:  true,
	gozxing.BarcodeFormat_UPC_A:  true,
	gozxing.BarcodeFormat_UPC_E:  true,
}

// readerCtor returns the constructor for one format's reader, or nil when this
// build has no reader for it.
//
// It is a constructor rather than a reader because a fresh reader is built per
// decode: gozxing readers carry per-decode state and expose Reset, so a shared
// package-level instance would need a lock on the hottest path in the service
// to stay correct. Returning the constructor also lets Symbologies ask "can we
// read this?" without allocating anything.
func readerCtor(f gozxing.BarcodeFormat) func(map[gozxing.DecodeHintType]any) gozxing.Reader {
	switch f {
	case gozxing.BarcodeFormat_QR_CODE:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return qrcode.NewQRCodeReader() }
	case gozxing.BarcodeFormat_DATA_MATRIX:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader {
			return datamatrix.NewDataMatrixReader()
		}
	case gozxing.BarcodeFormat_AZTEC:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return aztec.NewAztecReader() }
	case gozxing.BarcodeFormat_EAN_13, gozxing.BarcodeFormat_EAN_8,
		gozxing.BarcodeFormat_UPC_A, gozxing.BarcodeFormat_UPC_E:
		return oned.NewMultiFormatUPCEANReader
	case gozxing.BarcodeFormat_CODE_128:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return oned.NewCode128Reader() }
	case gozxing.BarcodeFormat_CODE_93:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return oned.NewCode93Reader() }
	case gozxing.BarcodeFormat_CODE_39:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return oned.NewCode39Reader() }
	case gozxing.BarcodeFormat_ITF:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return oned.NewITFReader() }
	case gozxing.BarcodeFormat_CODABAR:
		return func(map[gozxing.DecodeHintType]any) gozxing.Reader { return oned.NewCodaBarReader() }
	default:
		// PDF417 is mapped for naming but gozxing v0.1.1 ships no reader for
		// it, so it falls through here and PossibleFormats rejects it.
		return nil
	}
}

// namedReader is one reader together with the symbology it was built for, kept
// only so a decode failure can say which reader produced it.
type namedReader struct {
	name   string
	reader gozxing.Reader
}

// buildReaders returns the readers to try, in scan order.
//
// formats is the POSSIBLE_FORMATS filter; nil means every readable format. The
// four UPC/EAN formats collapse into a single reader, which is both faster and
// the only way to get UPC-A reported as UPC-A.
func buildReaders(
	formats []gozxing.BarcodeFormat,
	hints map[gozxing.DecodeHintType]any,
) []namedReader {
	want := make(map[gozxing.BarcodeFormat]bool, len(formats))
	for _, f := range formats {
		want[f] = true
	}

	out := make([]namedReader, 0, len(formatMap))
	upcDone := false

	for _, e := range formatMap {
		if len(formats) > 0 && !want[e.format] {
			continue
		}
		if upcEAN[e.format] {
			if upcDone {
				continue
			}
			upcDone = true
			out = append(out, namedReader{"upc/ean", oned.NewMultiFormatUPCEANReader(hints)})
			continue
		}
		if ctor := readerCtor(e.format); ctor != nil {
			out = append(out, namedReader{e.name, ctor(hints)})
		}
	}
	return out
}
