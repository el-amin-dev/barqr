package encoder

import (
	"strings"
	"testing"
)

func TestGS1CheckDigit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    byte
	}{
		{name: "ean13", payload: "590123412345", want: '7'},
		{name: "ean13 landing on zero", payload: "978030640615", want: '7'},
		{name: "ean8", payload: "9638507", want: '4'},
		{name: "upca", payload: "03600029145", want: '2'},
		{name: "gtin14", payload: "1540141453247", want: '7'},
		{name: "all zeroes", payload: "0000000", want: '0'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := gs1CheckDigit(tt.payload); got != tt.want {
				t.Errorf("gs1CheckDigit(%q) = %c, want %c", tt.payload, got, tt.want)
			}
		})
	}
}

func TestWithCheckDigit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		want     string
		wantErr  error
		fragment string
	}{
		{name: "computed", data: "9638507", want: "96385074"},
		{name: "verified", data: "96385074", want: "96385074"},
		{
			name: "wrong check digit", data: "96385070",
			wantErr: ErrInvalidData, fragment: "check digit 0 is wrong, expected 4",
		},
		{
			name: "too short", data: "963850",
			wantErr: ErrInvalidData, fragment: "expected 7 digits, or 8 including the check digit, got 6",
		},
		{
			name: "not a digit", data: "96385O7",
			wantErr: ErrInvalidData, fragment: "character 6 is 'O', expected a digit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := withCheckDigit("ean8", tt.data, 7)
			if tt.wantErr != nil {
				wantErr(t, err, tt.wantErr, tt.fragment)
				return
			}
			if err != nil {
				t.Fatalf("withCheckDigit(%q): %v", tt.data, err)
			}
			if got != tt.want {
				t.Errorf("withCheckDigit(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestRequireHelpersAcceptValidInput(t *testing.T) {
	t.Parallel()

	if err := requireDigits("itf", "0123456789"); err != nil {
		t.Errorf("requireDigits: %v", err)
	}
	if err := requireAlphabet("code39", "AB-1 $", code39Alphabet); err != nil {
		t.Errorf("requireAlphabet: %v", err)
	}
	if err := requireASCII("code128", "plain ~ text\x00"); err != nil {
		t.Errorf("requireASCII: %v", err)
	}
	if err := requireLength("code39", "AB", linearMaxLength); err != nil {
		t.Errorf("requireLength: %v", err)
	}
	if err := requireAutoOpts("code39", AutoEncodeOpts()); err != nil {
		t.Errorf("requireAutoOpts: %v", err)
	}
	if err := requireNoECC("code39", "  "); err != nil {
		t.Errorf("requireNoECC on whitespace: %v", err)
	}
}

func TestRequireHelpersRejectInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		sentinel error
		fragment string
	}{
		{
			name: "digits", err: requireDigits("itf", "12a4"),
			sentinel: ErrInvalidData, fragment: "character 3 is 'a'",
		},
		{
			name: "alphabet", err: requireAlphabet("code39", "AB!", code39Alphabet),
			sentinel: ErrInvalidData, fragment: "character 3 is '!'",
		},
		{
			name: "ascii", err: requireASCII("code128", "aé"),
			sentinel: ErrInvalidData, fragment: "character 2 is 'é'",
		},
		{
			name: "empty", err: requireLength("code39", "", 10),
			sentinel: ErrInvalidData, fragment: "data is empty",
		},
		{
			name: "too long", err: requireLength("code39", strings.Repeat("A", 11), 10),
			sentinel: ErrDataTooLong, fragment: "11 characters exceeds the 10",
		},
		{
			name: "version", err: requireAutoOpts("code39", EncodeOpts{Version: 1, Mask: -1}),
			sentinel: ErrUnsupportedOption, fragment: "version must be automatic",
		},
		{
			name: "mask", err: requireAutoOpts("code39", EncodeOpts{Mask: 0}),
			sentinel: ErrUnsupportedOption, fragment: "mask must be automatic",
		},
		{
			name: "ecc", err: requireNoECC("code39", "H"),
			sentinel: ErrUnsupportedOption, fragment: `ecc "H"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, tt.err, tt.sentinel, tt.fragment)
		})
	}
}

// TestLinearFromBits checks the conversion every linear encoder ends with,
// including that a Matrix is exactly one row tall.
func TestLinearFromBits(t *testing.T) {
	t.Parallel()

	m := linearFromBits("test", []bool{true, false, true, true}, 7, "1234")
	if m.Cols != 4 || m.Rows != 1 {
		t.Fatalf("matrix is %dx%d, want 4x1", m.Cols, m.Rows)
	}
	if m.Symbology != "test" || m.Kind != Kind1D || m.QuietZone != 7 || m.HRI != "1234" {
		t.Errorf("matrix metadata is %+v", m)
	}
	if got, want := m.String(), "#.##\n"; got != want {
		t.Errorf("modules = %q, want %q", got, want)
	}
}
