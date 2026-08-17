package encoder

import (
	"strings"
	"testing"
)

func TestDataMatrixEncodesKnownSizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		wantSide int
	}{
		{name: "short text", data: "barqr", wantSide: 12},
		{name: "digits pack two per codeword", data: "1234567890123456", wantSide: 14},
		{name: "longer text needs a bigger symbol", data: strings.Repeat("barqr ", 8), wantSide: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, DataMatrix, tt.data)
			if m.Cols != tt.wantSide || m.Rows != tt.wantSide {
				t.Errorf("matrix is %dx%d, want %dx%d", m.Cols, m.Rows, tt.wantSide, tt.wantSide)
			}
			if m.Kind != Kind2D {
				t.Errorf("kind = %q, want %q", m.Kind, Kind2D)
			}
			if m.HRI != "" {
				t.Errorf("hri = %q, want empty for a 2D symbology", m.HRI)
			}
			if m.QuietZone != dmQuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, dmQuietZone)
			}
		})
	}
}

// TestDataMatrixHasItsFinderPattern checks the L of solid modules down the left
// edge and along the bottom, which is what a reader locates first.
func TestDataMatrixHasItsFinderPattern(t *testing.T) {
	t.Parallel()

	m := encodeOK(t, DataMatrix, "barqr")
	for y := range m.Rows {
		if !m.At(0, y) {
			t.Errorf("left edge module at row %d is light", y)
		}
	}
	for x := range m.Cols {
		if !m.At(x, m.Rows-1) {
			t.Errorf("bottom edge module at column %d is light", x)
		}
	}
}

func TestDataMatrixRejectsBadInput(t *testing.T) {
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
			name: "beyond the byte capacity", data: strings.Repeat("A", dmMaxBytes+1),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "1557 bytes",
		},
		{
			// Every byte above 127 costs two codewords, so a payload that
			// passes the byte guard can still outgrow the largest symbol. That
			// failure comes back from the library and must still read as a
			// capacity problem.
			name: "high bytes double up", data: strings.Repeat("é", 700),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "datamatrix",
		},
		{
			name: "ecc level", data: "barqr", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "fixed error-correction",
		},
		{
			name: "pinned version", data: "barqr", opts: EncodeOpts{Version: 10, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "pinned mask", data: "barqr", opts: EncodeOpts{Mask: 3},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, DataMatrix, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
