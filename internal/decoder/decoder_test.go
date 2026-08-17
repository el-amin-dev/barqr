package decoder

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"github.com/makiuchi-d/gozxing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// codeImage runs the real pipeline — encode, render, rasterise — and hands
// back the image a client would receive. Fixtures are generated rather than
// committed on purpose: a committed PNG proves the decoder still reads a file
// from 2026, while this proves the decoder reads what barqr produces today.
//
// It skips rather than fails when a symbology is not registered, so this
// package's tests stay green while the encoder set is still being filled in.
func codeImage(t *testing.T, symbology, data string, scale int) image.Image {
	t.Helper()

	enc, err := encoder.Get(symbology)
	if err != nil {
		t.Skipf("symbology %q is not available in this build: %v", symbology, err)
	}

	m, err := enc.Encode(data, encoder.AutoEncodeOpts())
	if err != nil {
		t.Fatalf("encode %s(%q): %v", symbology, data, err)
	}

	st := render.DefaultStyle()
	// The human-readable text is switched off so the test measures the symbol
	// and not the font rendering underneath it.
	st.HRI = false

	r, err := render.Get(render.StandardRenderer)
	if err != nil {
		t.Fatalf("render.Get: %v", err)
	}
	canvas, err := r.Render(m, st)
	if err != nil {
		t.Fatalf("render %s: %v", symbology, err)
	}

	o := writer.DefaultOutputOpts(writer.PNG)
	o.Scale = scale
	img, err := writer.Rasterize(canvas, o)
	if err != nil {
		t.Fatalf("rasterise %s: %v", symbology, err)
	}
	return img
}

// codePNG is codeImage taken all the way to bytes, which is what Decode wants.
func codePNG(t *testing.T, symbology, data string, scale int) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, codeImage(t, symbology, data, scale)); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestDecodeRoundTripsQRThroughPNG closes the invariant the package exists for.
func TestDecodeRoundTripsQRThroughPNG(t *testing.T) {
	t.Parallel()

	const payload = "https://example.com/barqr?ref=round-trip"

	got, err := Decode(t.Context(), codePNG(t, "qr", payload, 6), Options{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Decode returned %d results, want 1", len(got))
	}
	if got[0].Data != payload {
		t.Errorf("Data = %q, want %q", got[0].Data, payload)
	}
	if got[0].Symbology != "qr" {
		t.Errorf("Symbology = %q, want qr", got[0].Symbology)
	}
	if len(got[0].Points) == 0 {
		t.Error("no result points; a caller cannot draw a box around the code")
	}
	for _, p := range got[0].Points {
		if p.X < 0 || p.Y < 0 {
			t.Errorf("point %+v is outside the image", p)
		}
	}
}

// TestDecodeRoundTripsLinearSymbologies covers the 1D readers. Each case skips
// if its encoder is not registered yet rather than failing, because the
// encoder set is still growing alongside this package.
func TestDecodeRoundTripsLinearSymbologies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		symbology string
		payload   string
		// want is the text the reader returns, which is not always the text
		// the encoder was given.
		want string
	}{
		{symbology: "code128", payload: "BARQR-128", want: "BARQR-128"},
		{symbology: "code39", payload: "BARQR39", want: "BARQR39"},
		{symbology: "code93", payload: "BARQR93", want: "BARQR93"},
		{symbology: "ean13", payload: "590123412345", want: "5901234123457"},
		{symbology: "ean8", payload: "9638507", want: "96385074"},
		// gozxing strips the start and stop letters unless asked to keep them.
		{symbology: "codabar", payload: "A31117013A", want: "31117013"},
	}

	for _, tc := range tests {
		t.Run(tc.symbology, func(t *testing.T) {
			t.Parallel()

			// Linear codes need several pixels per module to survive
			// binarisation; at scale 1 a bar and its neighbour blur together.
			data := codePNG(t, tc.symbology, tc.payload, 4)

			got, err := Decode(t.Context(), data, Options{})
			if err != nil {
				t.Fatalf("Decode(%s): %v", tc.symbology, err)
			}
			if len(got) != 1 {
				t.Fatalf("Decode(%s) returned %d results, want 1", tc.symbology, len(got))
			}
			if got[0].Data != tc.want {
				t.Errorf("Data = %q, want %q", got[0].Data, tc.want)
			}
			if got[0].Symbology != tc.symbology {
				t.Errorf("Symbology = %q, want %q", got[0].Symbology, tc.symbology)
			}
		})
	}
}

func TestDecodeAcceptsADataURI(t *testing.T) {
	t.Parallel()

	const payload = "data-uri round trip"
	raw := codePNG(t, "qr", payload, 6)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	got, err := Decode(t.Context(), []byte(uri), Options{})
	if err != nil {
		t.Fatalf("Decode(data uri): %v", err)
	}
	if len(got) != 1 || got[0].Data != payload {
		t.Fatalf("Decode(data uri) = %+v, want the payload back", got)
	}
}

func TestDecodeRejectsANonImageDataURI(t *testing.T) {
	t.Parallel()

	_, err := Decode(t.Context(), []byte("data:text/plain;base64,aGVsbG8="), Options{})
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("Decode(text data uri) = %v, want ErrUnsupportedImage", err)
	}
}

func TestDecodeReportsNoCodeFoundOnABlankImage(t *testing.T) {
	t.Parallel()

	blank := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	draw.Draw(blank, blank.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, blank); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	if _, err := Decode(t.Context(), buf.Bytes(), Options{}); !errors.Is(err, ErrNoCodeFound) {
		t.Fatalf("Decode(blank) = %v, want ErrNoCodeFound", err)
	}
}

func TestDecodeRejectsRandomBytes(t *testing.T) {
	t.Parallel()

	junk := make([]byte, 1024)
	for i := range junk {
		junk[i] = byte(i*31 + 7)
	}

	if _, err := Decode(t.Context(), junk, Options{}); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("Decode(random bytes) = %v, want ErrUnsupportedImage", err)
	}
}

func TestDecodeEnforcesMaxPixelsFromTheHeader(t *testing.T) {
	t.Parallel()

	bomb := bombPNG(t, 40_000, 40_000)
	if _, err := Decode(t.Context(), bomb, Options{}); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("Decode(bomb) = %v, want ErrImageTooLarge", err)
	}
}

func TestDecodeImageEnforcesMaxPixels(t *testing.T) {
	t.Parallel()

	img := codeImage(t, "qr", "too big", 6)

	_, err := DecodeImage(t.Context(), img, Options{MaxPixels: 16})
	if !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("DecodeImage with MaxPixels=16 = %v, want ErrImageTooLarge", err)
	}
}

func TestDecodeImageRejectsAnUnusableImage(t *testing.T) {
	t.Parallel()

	if _, err := DecodeImage(t.Context(), nil, Options{}); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("DecodeImage(nil) = %v, want ErrUnsupportedImage", err)
	}

	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	if _, err := DecodeImage(t.Context(), empty, Options{}); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("DecodeImage(0x0) = %v, want ErrUnsupportedImage", err)
	}
}

func TestDecodeImageRejectsAnUnknownSymbologyFilter(t *testing.T) {
	t.Parallel()

	img := codeImage(t, "qr", "filtered", 6)

	_, err := DecodeImage(t.Context(), img, Options{Symbologies: []string{"nonsense"}})
	if !errors.Is(err, encoder.ErrUnknownSymbology) {
		t.Fatalf("DecodeImage with a bad filter = %v, want ErrUnknownSymbology", err)
	}
}

// TestDecodeHonoursTheSymbologyFilter proves the filter narrows the scan
// rather than merely labelling the result.
func TestDecodeHonoursTheSymbologyFilter(t *testing.T) {
	t.Parallel()

	img := codeImage(t, "qr", "only qr here", 6)

	if _, err := DecodeImage(t.Context(), img, Options{Symbologies: []string{"qr"}}); err != nil {
		t.Fatalf("DecodeImage restricted to qr = %v", err)
	}

	_, err := DecodeImage(t.Context(), img, Options{Symbologies: []string{"ean13", "code128"}})
	if !errors.Is(err, ErrNoCodeFound) {
		t.Fatalf("DecodeImage restricted away from qr = %v, want ErrNoCodeFound", err)
	}
}

func TestDecodeTryHarderStillRoundTrips(t *testing.T) {
	t.Parallel()

	const payload = "try harder"
	got, err := Decode(t.Context(), codePNG(t, "qr", payload, 6), Options{TryHarder: true})
	if err != nil {
		t.Fatalf("Decode(TryHarder): %v", err)
	}
	if len(got) != 1 || got[0].Data != payload {
		t.Fatalf("Decode(TryHarder) = %+v, want the payload back", got)
	}
}

// TestDecodeMultiFindsEveryCode composes two independent QR symbols onto one
// sheet, which is the case Multi exists for.
func TestDecodeMultiFindsEveryCode(t *testing.T) {
	t.Parallel()

	left := codeImage(t, "qr", "left-hand code", 6)
	right := codeImage(t, "qr", "right-hand code", 6)

	sheet := image.NewNRGBA(image.Rect(0, 0, 900, 500))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(sheet, left.Bounds().Add(image.Pt(20, 20)), left, image.Point{}, draw.Src)
	draw.Draw(sheet, right.Bounds().Add(image.Pt(520, 20)), right, image.Point{}, draw.Src)

	got, err := DecodeImage(t.Context(), sheet, Options{Multi: true, Symbologies: []string{"qr"}})
	if err != nil {
		t.Fatalf("DecodeImage(Multi): %v", err)
	}

	found := make(map[string]bool, len(got))
	for _, r := range got {
		found[r.Data] = true
	}
	for _, want := range []string{"left-hand code", "right-hand code"} {
		if !found[want] {
			t.Errorf("Multi decode missed %q; found %v", want, found)
		}
	}
}

func TestDecodeMultiReportsNoCodeFoundOnABlankImage(t *testing.T) {
	t.Parallel()

	blank := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	draw.Draw(blank, blank.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	_, err := DecodeImage(t.Context(), blank, Options{Multi: true})
	if !errors.Is(err, ErrNoCodeFound) {
		t.Fatalf("DecodeImage(blank, Multi) = %v, want ErrNoCodeFound", err)
	}
}

// TestDecodeReturnsPromptlyOnACancelledContext checks that a client hanging up
// stops the work rather than being noticed only at the end of it.
func TestDecodeReturnsPromptlyOnACancelledContext(t *testing.T) {
	t.Parallel()

	data := codePNG(t, "qr", "cancelled", 6)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := Decode(ctx, data, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Decode with a cancelled context = %v, want context.Canceled", err)
	}

	img := codeImage(t, "qr", "cancelled", 6)
	if _, err := DecodeImage(ctx, img, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("DecodeImage with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestSurroundingSkipsSliversAndCoversTheRest(t *testing.T) {
	t.Parallel()

	// A code in the middle of a large sheet leaves a usable strip on all four
	// sides.
	got := surrounding(rect{x: 300, y: 300, w: 100, h: 100}, 1000, 1000)
	if len(got) != 4 {
		t.Fatalf("surrounding(centred box) = %v, want four strips", got)
	}

	// A code filling the sheet leaves nothing worth re-scanning.
	got = surrounding(rect{x: 0, y: 0, w: 1000, h: 1000}, 1000, 1000)
	if len(got) != 0 {
		t.Fatalf("surrounding(full-bleed box) = %v, want none", got)
	}
}

func TestBoundingBoxIgnoresMissingPoints(t *testing.T) {
	t.Parallel()

	if _, ok := boundingBox(nil); ok {
		t.Error("boundingBox(nil) reported a box")
	}

	// gozxing may leave a nil in the slice for a point it could not locate,
	// which must be skipped rather than dereferenced.
	pts := []gozxing.ResultPoint{
		nil,
		gozxing.NewResultPoint(10, 20),
		gozxing.NewResultPoint(40, 5),
	}
	box, ok := boundingBox(pts)
	if !ok {
		t.Fatal("boundingBox returned no box for real points")
	}
	if box.x != 10 || box.y != 5 || box.w != 30 || box.h != 15 {
		t.Fatalf("boundingBox = %+v, want {10 5 30 15}", box)
	}

	if _, ok := boundingBox([]gozxing.ResultPoint{nil, nil}); ok {
		t.Error("boundingBox of nothing but nil points reported a box")
	}
}

// TestIsNotFoundSeparatesAMissFromADamagedSymbol pins the distinction the
// whole error story rests on: almost every reader in a scan misses, and only a
// reader that saw its own symbology and failed on it deserves ErrDecodeFailed.
func TestIsNotFoundSeparatesAMissFromADamagedSymbol(t *testing.T) {
	t.Parallel()

	if !isNotFound(gozxing.NewNotFoundException()) {
		t.Error("a NotFoundException must count as a miss")
	}
	// A recovered panic is treated as a miss so one buggy reader cannot mask
	// a code a later reader would have found.
	if !isNotFound(errParserPanic) {
		t.Error("a recovered panic must count as a miss")
	}
	if isNotFound(gozxing.NewChecksumException()) {
		t.Error("a checksum failure is not a miss; the reader saw a symbol")
	}
}

func TestReaderReasonNeverForwardsTheLibraryText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"checksum", gozxing.NewChecksumException("secret internals"), "checksum does not verify"},
		{"format", gozxing.NewFormatException("secret internals"), "symbol is malformed"},
		{"anything else", errors.New("secret internals"), "symbol could not be read"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := readerReason(tc.err); got != tc.want {
				t.Fatalf("readerReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToResult(t *testing.T) {
	t.Parallel()

	if _, ok := toResult(nil, 0, 0); ok {
		t.Error("toResult(nil) reported success")
	}

	unmapped := gozxing.NewResult("x", nil, nil, gozxing.BarcodeFormat_MAXICODE)
	if _, ok := toResult(unmapped, 0, 0); ok {
		t.Error("toResult accepted a format barqr has no name for")
	}

	pts := []gozxing.ResultPoint{gozxing.NewResultPoint(1, 2), nil}
	res := gozxing.NewResult("hi", nil, pts, gozxing.BarcodeFormat_QR_CODE)

	got, ok := toResult(res, 10, 20)
	if !ok {
		t.Fatal("toResult rejected a well-formed QR result")
	}
	if got.Symbology != "qr" || got.Data != "hi" {
		t.Fatalf("toResult = %+v, want qr/hi", got)
	}
	// The offset of the sub-image the code was found in must be added back,
	// or a Multi result would point at the wrong part of the sheet.
	if len(got.Points) != 1 || got.Points[0] != (Point{X: 11, Y: 22}) {
		t.Fatalf("toResult points = %+v, want one point at (11, 22)", got.Points)
	}
}
