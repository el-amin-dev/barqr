package encoder

import (
	"fmt"
	"strings"

	"github.com/boombuler/barcode/qr"
)

// QR is the registry name of the QR Code encoder.
const QR = "qr"

// qrQuietZone is the margin the QR specification requires, in modules.
const qrQuietZone = 4

// qrMaxBytes is the largest byte payload a version-40 symbol can carry at the
// weakest error correction. It is used for the pre-flight length check so that
// an oversized payload gets a clear message instead of a library error.
const qrMaxBytes = 2953

func init() { Register(qrEncoder{}) }

// qrEncoder wraps boombuler/barcode's QR implementation.
//
// That library selects version and mask automatically and exposes no hook for
// pinning either. Rather than pretend otherwise, this encoder rejects an
// explicit version or mask with ErrUnsupportedOption and says so in its
// capabilities, so /v1/symbologies stays honest about the build.
type qrEncoder struct{}

func (qrEncoder) Name() string { return QR }

func (qrEncoder) Caps() Capabilities {
	return Capabilities{
		Name:      QR,
		Title:     "QR Code",
		Kind:      Kind2D,
		Available: true,
		ECCLevels: []string{"L", "M", "Q", "H"},
		Charset:   "any UTF-8 text; numeric and alphanumeric payloads are packed more densely",
		MaxLength: qrMaxBytes,
		QuietZone: qrQuietZone,
		HRI:       false,
		Notes:     "version and mask are selected automatically; pinning either requires the full build",
	}
}

func (qrEncoder) Encode(data string, o EncodeOpts) (Matrix, error) {
	if data == "" {
		return Matrix{}, fmt.Errorf("%w: data is empty", ErrInvalidData)
	}
	if len(data) > qrMaxBytes {
		return Matrix{}, fmt.Errorf("%w: %d bytes exceeds the %d-byte capacity of a QR symbol",
			ErrDataTooLong, len(data), qrMaxBytes)
	}
	if o.Version != 0 {
		return Matrix{}, fmt.Errorf("%w: qr: version must be automatic in this build", ErrUnsupportedOption)
	}
	if o.Mask >= 0 {
		return Matrix{}, fmt.Errorf("%w: qr: mask must be automatic in this build", ErrUnsupportedOption)
	}

	level, err := qrLevel(o.ECC)
	if err != nil {
		return Matrix{}, err
	}

	code, err := qr.Encode(data, level, qr.Auto)
	if err != nil {
		// The library reports a capacity overflow for payloads that pass the
		// byte check but exceed the denser encodings' limits.
		return Matrix{}, fmt.Errorf("%w: qr: %w", ErrDataTooLong, err)
	}

	b := code.Bounds()
	m := NewMatrix(b.Dx(), b.Dy())
	m.Symbology = QR
	m.Kind = Kind2D
	m.QuietZone = qrQuietZone

	for y := range m.Rows {
		for x := range m.Cols {
			r, g, bl, _ := code.At(b.Min.X+x, b.Min.Y+y).RGBA()
			m.Set(x, y, isDark(r, g, bl))
		}
	}

	return m, nil
}

// isDark classifies a pixel from the encoder library's output. The library
// emits pure black and white, so a midpoint threshold is exact rather than
// approximate.
func isDark(r, g, b uint32) bool {
	const midpoint = 0x8000
	return (r+g+b)/3 < midpoint
}

// qrLevel maps an ECC name onto the library's level. An empty name selects M,
// the specification's default and a sensible balance for a code that may be
// printed.
func qrLevel(ecc string) (qr.ErrorCorrectionLevel, error) {
	switch strings.ToUpper(strings.TrimSpace(ecc)) {
	case "", "M":
		return qr.M, nil
	case "L":
		return qr.L, nil
	case "Q":
		return qr.Q, nil
	case "H":
		return qr.H, nil
	default:
		return 0, fmt.Errorf("%w: qr: ecc %q: expected L, M, Q, or H", ErrUnsupportedOption, ecc)
	}
}
