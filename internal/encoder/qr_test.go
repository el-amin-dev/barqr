package encoder_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

// auto is the automatic option set every test starts from.
func auto() encoder.EncodeOpts { return encoder.AutoEncodeOpts() }

func TestQRIsRegistered(t *testing.T) {
	t.Parallel()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatalf("Get(qr) returned error: %v", err)
	}
	if got, want := enc.Name(), "qr"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}

	caps := enc.Caps()
	if !caps.Available {
		t.Error("qr reports itself unavailable")
	}
	if got, want := caps.Kind, encoder.Kind2D; got != want {
		t.Errorf("Kind = %q, want %q", got, want)
	}
	if got, want := caps.QuietZone, 4; got != want {
		t.Errorf("QuietZone = %d, want %d (the QR specification's margin)", got, want)
	}
	if len(caps.ECCLevels) != 4 {
		t.Errorf("ECCLevels = %v, want all four levels", caps.ECCLevels)
	}
}

// TestQREncodeGeometry pins the well-known geometry of a version-1 symbol:
// 21x21 modules with no quiet zone, since the renderer applies that.
func TestQREncodeGeometry(t *testing.T) {
	t.Parallel()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatal(err)
	}

	m, err := enc.Encode("HELLO", auto())
	if err != nil {
		t.Fatalf("Encode() returned error: %v", err)
	}

	if m.Cols != m.Rows {
		t.Errorf("matrix is %dx%d, want square", m.Cols, m.Rows)
	}
	if m.Cols != 21 {
		t.Errorf("Cols = %d, want 21 for a version-1 symbol", m.Cols)
	}
	if got, want := m.Symbology, "qr"; got != want {
		t.Errorf("Symbology = %q, want %q", got, want)
	}
	if m.HRI != "" {
		t.Errorf("HRI = %q, want empty for a 2D symbology", m.HRI)
	}

	// The three finder patterns are solid 7x7 rings. Their outer corners are
	// dark in every valid symbol, which is a cheap structural assertion that
	// the pixel-to-module conversion did not silently invert.
	for _, p := range [][2]int{{0, 0}, {m.Cols - 1, 0}, {0, m.Rows - 1}} {
		if !m.At(p[0], p[1]) {
			t.Errorf("module at (%d, %d) is light; a finder pattern corner must be dark",
				p[0], p[1])
		}
	}
	// The bottom-right corner has no finder pattern, so it is not part of one.
	if m.At(m.Cols-1, m.Rows-1) && m.At(m.Cols-2, m.Rows-2) && m.At(m.Cols-3, m.Rows-3) {
		t.Error("bottom-right looks like a fourth finder pattern")
	}
}

func TestQRErrorCorrectionChangesSize(t *testing.T) {
	t.Parallel()

	enc, _ := encoder.Get(encoder.QR)
	const payload = "https://example.com/a-reasonably-long-url-for-this-test"

	sizes := make(map[string]int)
	for _, ecc := range []string{"L", "M", "Q", "H"} {
		o := auto()
		o.ECC = ecc
		m, err := enc.Encode(payload, o)
		if err != nil {
			t.Fatalf("Encode(ecc=%s) returned error: %v", ecc, err)
		}
		sizes[ecc] = m.Cols
	}

	// Stronger correction spends more modules on redundancy, so the symbol
	// can only grow. Equal is fine — versions are quantised.
	if sizes["H"] < sizes["L"] {
		t.Errorf("H produced a smaller symbol (%d) than L (%d)", sizes["H"], sizes["L"])
	}
}

func TestQRRejections(t *testing.T) {
	t.Parallel()

	enc, _ := encoder.Get(encoder.QR)

	tests := []struct {
		name    string
		data    string
		mutate  func(*encoder.EncodeOpts)
		wantErr error
	}{
		{
			name:    "empty data",
			data:    "",
			wantErr: encoder.ErrInvalidData,
		},
		{
			name:    "payload beyond capacity",
			data:    strings.Repeat("x", 3000),
			wantErr: encoder.ErrDataTooLong,
		},
		{
			name:    "unknown ecc level",
			data:    "hi",
			mutate:  func(o *encoder.EncodeOpts) { o.ECC = "Z" },
			wantErr: encoder.ErrUnsupportedOption,
		},
		{
			name:    "explicit version is not supported in this build",
			data:    "hi",
			mutate:  func(o *encoder.EncodeOpts) { o.Version = 5 },
			wantErr: encoder.ErrUnsupportedOption,
		},
		{
			name:    "explicit mask is not supported in this build",
			data:    "hi",
			mutate:  func(o *encoder.EncodeOpts) { o.Mask = 3 },
			wantErr: encoder.ErrUnsupportedOption,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o := auto()
			if tt.mutate != nil {
				tt.mutate(&o)
			}
			if _, err := enc.Encode(tt.data, o); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Encode() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetUnknownSymbology(t *testing.T) {
	t.Parallel()

	if _, err := encoder.Get("definitely-not-a-symbology"); !errors.Is(err, encoder.ErrUnknownSymbology) {
		t.Fatalf("Get() error = %v, want %v", err, encoder.ErrUnknownSymbology)
	}
}

func TestMatrixAccessors(t *testing.T) {
	t.Parallel()

	m := encoder.NewMatrix(3, 2)
	m.Set(0, 0, true)
	m.Set(2, 1, true)

	if !m.At(0, 0) || !m.At(2, 1) {
		t.Error("Set values did not read back")
	}
	if m.At(1, 1) {
		t.Error("an unset module reads dark")
	}
	// Out-of-range reads are light rather than a panic, so renderers can walk
	// a padded area without bounds-checking every access.
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {3, 0}, {0, 2}} {
		if m.At(p[0], p[1]) {
			t.Errorf("At(%d, %d) = true, want false for an out-of-range read", p[0], p[1])
		}
	}
	if got, want := m.Dark(), 2; got != want {
		t.Errorf("Dark() = %d, want %d", got, want)
	}
	if got := m.String(); !strings.Contains(got, "#") || !strings.Contains(got, ".") {
		t.Errorf("String() = %q, want a # and . rendering", got)
	}
}

func TestMatrixSetOutOfRangePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Set outside the matrix did not panic")
		}
	}()

	m := encoder.NewMatrix(2, 2)
	m.Set(5, 5, true)
}

// TestAllCapabilitiesAreHonest asserts the invariant /v1/symbologies depends
// on: every registered symbology describes itself completely, and one that is
// unavailable says why rather than leaving a caller guessing.
func TestAllCapabilitiesAreHonest(t *testing.T) {
	t.Parallel()

	all := encoder.All()
	if len(all) == 0 {
		t.Fatal("no symbologies registered")
	}

	for _, caps := range all {
		t.Run(caps.Name, func(t *testing.T) {
			if caps.Name == "" || caps.Title == "" {
				t.Error("name and title must both be set")
			}
			if caps.Kind != encoder.Kind1D && caps.Kind != encoder.Kind2D {
				t.Errorf("Kind = %q, want 1d or 2d", caps.Kind)
			}
			if caps.Charset == "" {
				t.Error("Charset must describe the accepted alphabet")
			}
			if caps.QuietZone < 0 {
				t.Errorf("QuietZone = %d, want zero or more", caps.QuietZone)
			}
			if !caps.Available && caps.Reason == "" {
				t.Error("an unavailable symbology must explain why")
			}
			if caps.Available && caps.Reason != "" {
				t.Errorf("an available symbology should not carry a reason: %q", caps.Reason)
			}
		})
	}
}

func BenchmarkQREncode(b *testing.B) {
	enc, _ := encoder.Get(encoder.QR)
	o := auto()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := enc.Encode("https://example.com/benchmark", o); err != nil {
			b.Fatal(err)
		}
	}
}
