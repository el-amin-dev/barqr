package encoder

import "testing"

func TestCodabarEncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		wantCols int
	}{
		// Start, stop and the four wide punctuation characters are ten modules;
		// digits, the dash and the dollar are nine. One module separates each.
		{name: "digits", data: "A12345B", wantCols: 71},
		{name: "shortest symbol", data: "A0B", wantCols: 31},
		{name: "every punctuation character", data: "A-$:/.+B", wantCols: 85},
		{name: "other start and stop letters", data: "C9D", wantCols: 31},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, Codabar, tt.data)
			if m.Cols != tt.wantCols {
				t.Errorf("cols = %d, want %d", m.Cols, tt.wantCols)
			}
			if m.HRI != tt.data {
				t.Errorf("hri = %q, want %q", m.HRI, tt.data)
			}
			if m.QuietZone != codabarQuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, codabarQuietZone)
			}
		})
	}
}

func TestCodabarRejectsBadInput(t *testing.T) {
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
			name: "no start or stop character", data: "12345", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "must start and stop with A, B, C or D",
		},
		{
			name: "start character only", data: "A12345", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "must start and stop with A, B, C or D",
		},
		{
			name: "nothing between the guards", data: "AB", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "too short",
		},
		{
			name: "letter outside the alphabet", data: "A12X45B", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "'X'",
		},
		{
			name: "guard letter in the middle", data: "A1B2C", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "start or stop character",
		},
		{
			name: "ecc level", data: "A1B", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
		{
			name: "pinned version", data: "A1B", opts: EncodeOpts{Version: 2, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, Codabar, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
