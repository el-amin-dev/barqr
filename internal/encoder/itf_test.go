package encoder

import (
	"strings"
	"testing"
)

func TestITFEncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sym      string
		data     string
		wantCols int
		wantHRI  string
	}{
		// Interleaved: nine modules of guard patterns plus eighteen per pair.
		{name: "itf five pairs", sym: ITF, data: "1234567890", wantCols: 99, wantHRI: "1234567890"},
		{name: "itf one pair", sym: ITF, data: "12", wantCols: 27, wantHRI: "12"},
		{
			name: "itf14 computes the check digit", sym: ITF14, data: "1540141453247",
			wantCols: 135, wantHRI: "15401414532477",
		},
		{
			name: "itf14 verifies the check digit", sym: ITF14, data: "15401414532477",
			wantCols: 135, wantHRI: "15401414532477",
		},
		// Standard 2 of 5 leaves the spaces empty, so it needs fourteen modules
		// per digit against the interleaved form's nine.
		{name: "2of5", sym: TwoOfFive, data: "12345670", wantCols: 127, wantHRI: "12345670"},
		{name: "2of5 accepts an odd count", sym: TwoOfFive, data: "12", wantCols: 43, wantHRI: "12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, tt.sym, tt.data)
			if m.Cols != tt.wantCols {
				t.Errorf("cols = %d, want %d", m.Cols, tt.wantCols)
			}
			if m.HRI != tt.wantHRI {
				t.Errorf("hri = %q, want %q", m.HRI, tt.wantHRI)
			}
			if m.QuietZone != itfQuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, itfQuietZone)
			}
			if m.Rows != 1 || m.Kind != Kind1D {
				t.Errorf("matrix is %dx%d of kind %q", m.Cols, m.Rows, m.Kind)
			}
		})
	}
}

// TestITFRejectsAnOddDigitCount covers the trap the symbology is known for:
// digits are carried in pairs, so an odd count has nowhere to go.
func TestITFRejectsAnOddDigitCount(t *testing.T) {
	t.Parallel()

	for _, data := range []string{"1", "12345", "1234567"} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()

			err := encodeErr(t, ITF, data, AutoEncodeOpts())
			wantErr(t, err, ErrInvalidData, "odd")
			if !strings.Contains(err.Error(), "leading zero") {
				t.Errorf("error %q does not suggest padding with a leading zero", err)
			}
		})
	}
}

func TestITF14RejectsBadCheckDigit(t *testing.T) {
	t.Parallel()

	err := encodeErr(t, ITF14, "15401414532470", AutoEncodeOpts())
	wantErr(t, err, ErrInvalidData, "check digit 0 is wrong, expected 7")
}

func TestITFRejectsBadInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sym      string
		data     string
		opts     EncodeOpts
		sentinel error
		fragment string
	}{
		{
			name: "itf empty", sym: ITF, data: "", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "empty",
		},
		{
			name: "itf with a letter", sym: ITF, data: "12X4", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected a digit",
		},
		{
			name: "itf too long", sym: ITF, data: strings.Repeat("1", linearMaxLength+2),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "130 characters",
		},
		{
			name: "itf14 wrong length", sym: ITF14, data: "154014145324", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected 13 digits, or 14 including the check digit, got 12",
		},
		{
			name: "itf14 with a letter", sym: ITF14, data: "154014145324X", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected a digit",
		},
		{
			name: "2of5 empty", sym: TwoOfFive, data: "", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "empty",
		},
		{
			name: "2of5 with a letter", sym: TwoOfFive, data: "12X", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected a digit",
		},
		{
			name: "itf ecc level", sym: ITF, data: "12", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
		{
			name: "itf14 pinned version", sym: ITF14, data: "1540141453247",
			opts: EncodeOpts{Version: 1, Mask: -1}, sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "2of5 pinned mask", sym: TwoOfFive, data: "12", opts: EncodeOpts{Mask: 0},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, tt.sym, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
