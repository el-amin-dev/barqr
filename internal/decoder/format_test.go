package decoder

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

func TestFormatMappingIsBijective(t *testing.T) {
	t.Parallel()

	for _, e := range formatMap {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			name, ok := SymbologyName(e.format)
			if !ok || name != e.name {
				t.Fatalf("SymbologyName(%v) = %q, %v; want %q, true", e.format, name, ok, e.name)
			}

			f, ok := BarcodeFormat(e.name)
			if !ok || f != e.format {
				t.Fatalf("BarcodeFormat(%q) = %v, %v; want %v, true", e.name, f, ok, e.format)
			}
		})
	}
}

// TestFormatMappingCoversTheContract pins the exact names the rest of barqr
// and the public API depend on. A rename here is a breaking API change, so it
// must fail a test rather than pass silently.
func TestFormatMappingCoversTheContract(t *testing.T) {
	t.Parallel()

	want := map[gozxing.BarcodeFormat]string{
		gozxing.BarcodeFormat_QR_CODE:     "qr",
		gozxing.BarcodeFormat_DATA_MATRIX: "datamatrix",
		gozxing.BarcodeFormat_AZTEC:       "aztec",
		gozxing.BarcodeFormat_PDF_417:     "pdf417",
		gozxing.BarcodeFormat_CODE_128:    "code128",
		gozxing.BarcodeFormat_CODE_39:     "code39",
		gozxing.BarcodeFormat_CODE_93:     "code93",
		gozxing.BarcodeFormat_CODABAR:     "codabar",
		gozxing.BarcodeFormat_EAN_13:      "ean13",
		gozxing.BarcodeFormat_EAN_8:       "ean8",
		gozxing.BarcodeFormat_UPC_A:       "upca",
		gozxing.BarcodeFormat_UPC_E:       "upce",
		gozxing.BarcodeFormat_ITF:         "itf",
	}

	if len(want) != len(formatMap) {
		t.Fatalf("formatMap has %d entries, the contract names %d", len(formatMap), len(want))
	}
	for f, name := range want {
		if got, _ := SymbologyName(f); got != name {
			t.Errorf("SymbologyName(%v) = %q, want %q", f, got, name)
		}
	}
}

func TestSymbologyNameRejectsUnmappedFormat(t *testing.T) {
	t.Parallel()

	for _, f := range []gozxing.BarcodeFormat{
		gozxing.BarcodeFormat_MAXICODE,
		gozxing.BarcodeFormat_RSS_14,
		gozxing.BarcodeFormat_UPC_EAN_EXTENSION,
	} {
		if name, ok := SymbologyName(f); ok {
			t.Errorf("SymbologyName(%v) = %q, true; want false", f, name)
		}
	}
}

func TestBarcodeFormatRejectsUnknownName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "QR", "maxicode", "rss14"} {
		if f, ok := BarcodeFormat(name); ok {
			t.Errorf("BarcodeFormat(%q) = %v, true; want false", name, f)
		}
	}
}

// TestSymbologiesOmitsPDF417 guards the honesty rule: pdf417 is mapped so a
// result could be named, but gozxing v0.1.1 has no reader, so it must not be
// advertised as decodable.
func TestSymbologiesOmitsPDF417(t *testing.T) {
	t.Parallel()

	got := Symbologies()
	if slices.Contains(got, "pdf417") {
		t.Fatalf("Symbologies() = %v; pdf417 has no reader and must not be listed", got)
	}
	if !slices.IsSorted(got) {
		t.Errorf("Symbologies() = %v; want sorted", got)
	}
	for _, want := range []string{"qr", "ean13", "code128", "aztec", "datamatrix"} {
		if !slices.Contains(got, want) {
			t.Errorf("Symbologies() = %v; missing %q", got, want)
		}
	}
}

func TestPossibleFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []string
		want    []gozxing.BarcodeFormat
		wantErr error
	}{
		{
			name: "empty means every format",
			in:   nil,
			want: nil,
		},
		{
			name: "maps each name",
			in:   []string{"qr", "ean13"},
			want: []gozxing.BarcodeFormat{
				gozxing.BarcodeFormat_QR_CODE, gozxing.BarcodeFormat_EAN_13,
			},
		},
		{
			name: "deduplicates repeats",
			in:   []string{"qr", "qr", "qr"},
			want: []gozxing.BarcodeFormat{gozxing.BarcodeFormat_QR_CODE},
		},
		{
			name:    "rejects an unknown name",
			in:      []string{"qr", "quirk"},
			wantErr: encoder.ErrUnknownSymbology,
		},
		{
			name:    "rejects a symbology with no reader",
			in:      []string{"pdf417"},
			wantErr: encoder.ErrUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := PossibleFormats(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PossibleFormats(%v) error = %v, want %v", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("PossibleFormats(%v) = %v", tc.in, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("PossibleFormats(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestPossibleFormatsErrorNamesTheOffender is separate because the message is
// the whole point: a caller who typed a symbology wrong must be able to see
// which one from the error alone.
func TestPossibleFormatsErrorNamesTheOffender(t *testing.T) {
	t.Parallel()

	_, err := PossibleFormats([]string{"qr", "code12"})
	if err == nil {
		t.Fatal("PossibleFormats accepted an unknown symbology")
	}
	if !strings.Contains(err.Error(), "code12") {
		t.Fatalf("error %q does not name the offending value", err)
	}
}

func TestBuildReadersCollapsesTheUPCEANFamily(t *testing.T) {
	t.Parallel()

	formats := []gozxing.BarcodeFormat{
		gozxing.BarcodeFormat_EAN_13, gozxing.BarcodeFormat_EAN_8,
		gozxing.BarcodeFormat_UPC_A, gozxing.BarcodeFormat_UPC_E,
	}
	got := buildReaders(formats, nil)
	if len(got) != 1 || got[0].name != "upc/ean" {
		names := make([]string, len(got))
		for i, r := range got {
			names[i] = r.name
		}
		t.Fatalf("buildReaders(upc/ean family) = %v, want one shared reader", names)
	}
}

func TestBuildReadersHonoursTheFilter(t *testing.T) {
	t.Parallel()

	got := buildReaders([]gozxing.BarcodeFormat{gozxing.BarcodeFormat_QR_CODE}, nil)
	if len(got) != 1 || got[0].name != "qr" {
		t.Fatalf("buildReaders(qr) returned %d readers, want just qr", len(got))
	}
}

// TestBuildReadersUnfilteredScansMatrixFirst pins the scan order. The loosest
// linear formats must come last so they cannot claim noise before a stricter
// reader has had a look.
func TestBuildReadersUnfilteredScansMatrixFirst(t *testing.T) {
	t.Parallel()

	got := buildReaders(nil, nil)
	if len(got) < 5 {
		t.Fatalf("buildReaders(nil) = %d readers, want every readable format", len(got))
	}
	if got[0].name != "qr" {
		t.Errorf("first reader is %q, want qr", got[0].name)
	}
	if last := got[len(got)-1].name; last != "codabar" {
		t.Errorf("last reader is %q, want codabar", last)
	}
	for _, r := range got {
		if r.name == "pdf417" {
			t.Error("buildReaders included pdf417, which has no reader")
		}
	}
}
