package encoder

import (
	"strings"
	"testing"
)

func TestAztecEncodesKnownSizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		ecc      string
		wantSide int
	}{
		{name: "short text", data: "barqr", ecc: "", wantSide: 15},
		{name: "a short payload fits the smallest symbol whatever the level", data: "barqr", ecc: "H", wantSide: 15},
		{name: "longer text", data: strings.Repeat("barqr ", 8), ecc: "", wantSide: 23},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := Get(Aztec)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			o := AutoEncodeOpts()
			o.ECC = tt.ecc
			m, err := e.Encode(tt.data, o)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			if m.Cols != tt.wantSide || m.Rows != tt.wantSide {
				t.Errorf("matrix is %dx%d, want %dx%d", m.Cols, m.Rows, tt.wantSide, tt.wantSide)
			}
			if m.Kind != Kind2D || m.HRI != "" {
				t.Errorf("kind = %q and hri = %q, want a 2d matrix with no text", m.Kind, m.HRI)
			}
			if m.QuietZone != aztecQuietZone {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, aztecQuietZone)
			}
		})
	}
}

// TestAztecHasItsBullseye checks the alternating rings at the centre of the
// symbol, which is what makes Aztec locatable without a quiet zone.
func TestAztecHasItsBullseye(t *testing.T) {
	t.Parallel()

	m := encodeOK(t, Aztec, "barqr")
	centre := m.Cols / 2
	for ring, wantDark := range map[int]bool{0: true, 1: false, 2: true, 3: false} {
		if got := m.At(centre+ring, centre); got != wantDark {
			t.Errorf("ring %d at the centre is dark=%v, want %v", ring, got, wantDark)
		}
	}
}

// TestAztecECCLevelsGrowTheSymbol checks that the level names are wired to
// increasing redundancy, and that they are accepted in either case with
// surrounding space, as qr does.
func TestAztecECCLevelsGrowTheSymbol(t *testing.T) {
	t.Parallel()

	e, err := Get(Aztec)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	data := strings.Repeat("barqr ", 40)
	sides := make(map[string]int)
	for _, ecc := range []string{" l ", "M", "q", "H"} {
		o := AutoEncodeOpts()
		o.ECC = ecc
		m, err := e.Encode(data, o)
		if err != nil {
			t.Fatalf("ecc %q: %v", ecc, err)
		}
		sides[strings.ToUpper(strings.TrimSpace(ecc))] = m.Cols
	}

	if sides["L"] > sides["M"] || sides["M"] > sides["Q"] || sides["Q"] > sides["H"] {
		t.Errorf("symbol sides by level are %v, want them not to shrink as correction grows", sides)
	}
	if sides["H"] <= sides["L"] {
		t.Errorf("the strongest level produced a %d-module symbol, no larger than the weakest at %d",
			sides["H"], sides["L"])
	}
}

func TestAztecRejectsBadInput(t *testing.T) {
	t.Parallel()

	// Bytes above 127 cannot be packed into Aztec's text modes, so they force
	// the binary shift and a payload of them overruns the largest symbol once
	// half of it is given over to error correction.
	binary := make([]byte, aztecMaxBytes)
	for i := range binary {
		binary[i] = byte(128 + i%128)
	}

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
			name: "beyond the byte capacity", data: strings.Repeat("A", aztecMaxBytes+1),
			opts: AutoEncodeOpts(), sentinel: ErrDataTooLong, fragment: "1915 bytes",
		},
		{
			name: "binary data at the strongest correction", data: string(binary),
			opts: EncodeOpts{ECC: "H", Mask: -1}, sentinel: ErrDataTooLong, fragment: "aztec",
		},
		{
			name: "unknown ecc level", data: "barqr", opts: EncodeOpts{ECC: "X", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "expected L, M, Q, or H",
		},
		{
			name: "pinned version", data: "barqr", opts: EncodeOpts{Version: 4, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "pinned mask", data: "barqr", opts: EncodeOpts{Mask: 1},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, Aztec, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}
