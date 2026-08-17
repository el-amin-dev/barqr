package encoder

import (
	"strings"
	"testing"
)

func TestPDF417Encodes(t *testing.T) {
	t.Parallel()

	m := encodeOK(t, PDF417, "barqr")
	if m.Cols != 120 {
		t.Errorf("cols = %d, want 120", m.Cols)
	}
	if m.Kind != Kind2D || m.HRI != "" {
		t.Errorf("kind = %q and hri = %q, want a 2d matrix with no text", m.Kind, m.HRI)
	}
	if m.QuietZone != pdf417QuietZone {
		t.Errorf("quiet zone = %d, want %d", m.QuietZone, pdf417QuietZone)
	}
	// Each row of codewords starts with the same start pattern, eight dark
	// modules followed by a light one.
	for x := range 8 {
		if !m.At(x, 0) {
			t.Errorf("module %d of the start pattern is light", x)
		}
	}
	if m.At(8, 0) {
		t.Error("the start pattern is not closed by a light module")
	}
}

// TestPDF417RowsAreThreeModulesTall pins the row height. PDF417 rows must be at
// least three times the module width or a scan line crossing at an angle drifts
// out of the row it started in.
func TestPDF417RowsAreThreeModulesTall(t *testing.T) {
	t.Parallel()

	m := encodeOK(t, PDF417, "barqr")
	if m.Rows%pdf417RowHeight != 0 {
		t.Fatalf("rows = %d, which is not a whole number of codeword rows", m.Rows)
	}

	for y := 0; y < m.Rows; y += pdf417RowHeight {
		for x := range m.Cols {
			want := m.At(x, y)
			for i := 1; i < pdf417RowHeight; i++ {
				if m.At(x, y+i) != want {
					t.Fatalf("row %d differs from row %d at column %d", y+i, y, x)
				}
			}
		}
	}
}

func TestPDF417SecurityLevels(t *testing.T) {
	t.Parallel()

	e, err := Get(PDF417)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	sizes := make(map[string]int)
	for _, ecc := range []string{"0", "2", "5"} {
		o := AutoEncodeOpts()
		o.ECC = ecc
		m, err := e.Encode("barqr", o)
		if err != nil {
			t.Fatalf("ecc %q: %v", ecc, err)
		}
		sizes[ecc] = m.Rows * m.Cols
	}

	// More check codewords mean a larger symbol for the same payload.
	if sizes["5"] <= sizes["2"] || sizes["2"] <= sizes["0"] {
		t.Errorf("symbol sizes by level are %v, want them to grow with the level", sizes)
	}
}

func TestPDF417RejectsBadInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		opts     EncodeOpts
		sentinel error
		fragment string
	}{
		{
			name: "empty", data: "", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "empty",
		},
		{
			name: "beyond the byte capacity", data: strings.Repeat("A", pdf417MaxBytes+1),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "1109 bytes",
		},
		{
			// The strongest level spends 512 codewords on error correction,
			// leaving too few of the 928 for a payload this size.
			name: "full payload at the strongest level", data: strings.Repeat("A", pdf417MaxBytes),
			opts: EncodeOpts{ECC: "8", Mask: -1}, sentinel: ErrDataTooLong, fragment: "pdf417",
		},
		{
			name: "unknown ecc level", data: "barqr", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "security level from 0 to 8",
		},
		{
			name: "ecc level out of range", data: "barqr", opts: EncodeOpts{ECC: "9", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "security level from 0 to 8",
		},
		{
			name: "pinned version", data: "barqr", opts: EncodeOpts{Version: 2, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "pinned mask", data: "barqr", opts: EncodeOpts{Mask: 0},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, PDF417, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
