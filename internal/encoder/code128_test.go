package encoder

import (
	"strings"
	"testing"
)

func TestCode128EncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		wantCols int
	}{
		// Eight digits pack two to a symbol in character set C: start, four
		// data symbols, the check symbol and the 13-module stop pattern.
		{name: "digits use set C", data: "12345670", wantCols: 79},
		{name: "mixed text uses set B", data: "BARQR-128", wantCols: 134},
		{name: "single character", data: "A", wantCols: 46},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, Code128, tt.data)
			if m.Cols != tt.wantCols {
				t.Errorf("cols = %d, want %d", m.Cols, tt.wantCols)
			}
			if m.Rows != 1 || m.Kind != Kind1D {
				t.Errorf("matrix is %dx%d of kind %q, want one row of 1d", m.Cols, m.Rows, m.Kind)
			}
			if m.HRI != tt.data {
				t.Errorf("hri = %q, want %q", m.HRI, tt.data)
			}
			if m.QuietZone != code128QuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, code128QuietZone)
			}
		})
	}
}

func TestCode128RejectsBadInput(t *testing.T) {
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
			name: "beyond the library limit", data: strings.Repeat("A", code128MaxLength+1),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "81 characters",
		},
		{
			name: "outside ascii", data: "café", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "outside ASCII",
		},
		{
			name: "pinned version", data: "OK", opts: EncodeOpts{Version: 3, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "pinned mask", data: "OK", opts: EncodeOpts{Mask: 0},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
		{
			name: "ecc level", data: "OK", opts: EncodeOpts{ECC: "H", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, Code128, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
