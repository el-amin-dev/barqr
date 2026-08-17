package encoder

import (
	"strings"
	"testing"
)

func TestCode39EncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		wantCols int
	}{
		// Every character is twelve modules and a one-module gap separates
		// them, with the start and stop character adding two more characters.
		{name: "text with a space", data: "BARQR 39", wantCols: 129},
		{name: "digits", data: "123", wantCols: 64},
		{name: "punctuation", data: "A-1.$/+%", wantCols: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, Code39, tt.data)
			if m.Cols != tt.wantCols {
				t.Errorf("cols = %d, want %d", m.Cols, tt.wantCols)
			}
			if m.HRI != tt.data {
				t.Errorf("hri = %q, want %q", m.HRI, tt.data)
			}
			if m.QuietZone != code39QuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, code39QuietZone)
			}
		})
	}
}

func TestCode39RejectsBadInput(t *testing.T) {
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
			name: "lowercase is outside the base alphabet", data: "barqr", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "'b'",
		},
		{
			name: "the start and stop character is reserved", data: "A*B", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "'*'",
		},
		{
			name: "too long", data: strings.Repeat("A", linearMaxLength+1), opts: AutoEncodeOpts(),
			sentinel: ErrDataTooLong, fragment: "129 characters",
		},
		{
			name: "ecc level", data: "AB", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
		{
			name: "pinned version", data: "AB", opts: EncodeOpts{Version: 1, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, Code39, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
