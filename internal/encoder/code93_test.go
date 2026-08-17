package encoder

import "testing"

func TestCode93EncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		wantCols int
	}{
		// Nine modules per character, plus the start and stop characters, the
		// mandatory C and K check characters, and the single termination bar.
		{name: "text", data: "BARQR93", wantCols: 100},
		{name: "digits", data: "123", wantCols: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, Code93, tt.data)
			if m.Cols != tt.wantCols {
				t.Errorf("cols = %d, want %d", m.Cols, tt.wantCols)
			}
			if m.HRI != tt.data {
				t.Errorf("hri = %q, want %q", m.HRI, tt.data)
			}
			if m.QuietZone != code93QuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, code93QuietZone)
			}
		})
	}
}

// TestCode93IncludesBothCheckCharacters pins the choice to ask the library for
// its optional checksum: Code 93 defines C and K as mandatory, and the library
// adds K only when asked. Two characters' worth of width is the evidence.
func TestCode93IncludesBothCheckCharacters(t *testing.T) {
	t.Parallel()

	short := encodeOK(t, Code93, "1")
	long := encodeOK(t, Code93, "12")
	if got := long.Cols - short.Cols; got != 9 {
		t.Fatalf("one more data character widened the symbol by %d modules, want 9", got)
	}
	// start + data + C + K + stop, times nine modules, plus the termination bar.
	if want := (1+1+2+1)*9 + 1; short.Cols != want {
		t.Errorf("cols = %d, want %d", short.Cols, want)
	}
}

func TestCode93RejectsBadInput(t *testing.T) {
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
			sentinel: ErrInvalidData, fragment: "character set",
		},
		{
			name: "the start and stop character is reserved", data: "A*B", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "'*'",
		},
		{
			name: "ecc level", data: "AB", opts: EncodeOpts{ECC: "L", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
		{
			name: "pinned mask", data: "AB", opts: EncodeOpts{Mask: 2},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, Code93, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
