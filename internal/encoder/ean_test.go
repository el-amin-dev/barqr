package encoder

import "testing"

func TestEANEncodesKnownWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sym      string
		data     string
		wantCols int
		wantHRI  string
		wantQZ   int
	}{
		{
			name: "ean13 computes the check digit", sym: EAN13, data: "590123412345",
			wantCols: 95, wantHRI: "5901234123457", wantQZ: ean13QuietZone,
		},
		{
			name: "ean13 verifies the check digit", sym: EAN13, data: "5901234123457",
			wantCols: 95, wantHRI: "5901234123457", wantQZ: ean13QuietZone,
		},
		{
			name: "ean8 computes the check digit", sym: EAN8, data: "9638507",
			wantCols: 67, wantHRI: "96385074", wantQZ: ean8QuietZone,
		},
		{
			name: "ean8 verifies the check digit", sym: EAN8, data: "96385074",
			wantCols: 67, wantHRI: "96385074", wantQZ: ean8QuietZone,
		},
		{
			// UPC-A is EAN-13 with a leading zero, so it is the same 95 modules
			// wide, but its human-readable text keeps the twelve digits.
			name: "upca computes the check digit", sym: UPCA, data: "03600029145",
			wantCols: 95, wantHRI: "036000291452", wantQZ: upcQuietZone,
		},
		{
			name: "upca verifies the check digit", sym: UPCA, data: "036000291452",
			wantCols: 95, wantHRI: "036000291452", wantQZ: upcQuietZone,
		},
		{
			name: "upce computes the check digit", sym: UPCE, data: "0123456",
			wantCols: 51, wantHRI: "01234565", wantQZ: upcQuietZone,
		},
		{
			name: "upce verifies the check digit", sym: UPCE, data: "04252614",
			wantCols: 51, wantHRI: "04252614", wantQZ: upcQuietZone,
		},
		{
			name: "upce in number system 1", sym: UPCE, data: "1123456",
			wantCols: 51, wantHRI: "11234562", wantQZ: upcQuietZone,
		},
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
			if m.QuietZone != tt.wantQZ {
				t.Errorf("quiet zone = %d, want %d", m.QuietZone, tt.wantQZ)
			}
			if m.Rows != 1 || m.Kind != Kind1D {
				t.Errorf("matrix is %dx%d of kind %q", m.Cols, m.Rows, m.Kind)
			}
		})
	}
}

// TestEANStartsAndEndsWithAGuardBar checks the one structural property every
// EAN/UPC symbol shares, which catches a matrix read at the wrong offset.
func TestEANStartsAndEndsWithAGuardBar(t *testing.T) {
	t.Parallel()

	for sym, data := range map[string]string{
		EAN13: "590123412345", EAN8: "9638507", UPCA: "03600029145", UPCE: "0123456",
	} {
		t.Run(sym, func(t *testing.T) {
			t.Parallel()

			m := encodeOK(t, sym, data)
			if !m.At(0, 0) || m.At(1, 0) || !m.At(2, 0) {
				t.Errorf("left guard is %v %v %v, want dark light dark",
					m.At(0, 0), m.At(1, 0), m.At(2, 0))
			}
			if !m.At(m.Cols-1, 0) {
				t.Error("the last module is light, want the closing guard bar")
			}
		})
	}
}

func TestEANRejectsBadCheckDigit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sym      string
		data     string
		fragment string
	}{
		{name: "ean13", sym: EAN13, data: "5901234123450", fragment: "check digit 0 is wrong, expected 7"},
		{name: "ean8", sym: EAN8, data: "96385071", fragment: "check digit 1 is wrong, expected 4"},
		{name: "upca", sym: UPCA, data: "036000291450", fragment: "check digit 0 is wrong, expected 2"},
		{name: "upce", sym: UPCE, data: "01234560", fragment: "check digit 0 is wrong, expected 5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, tt.sym, tt.data, AutoEncodeOpts()), ErrInvalidData, tt.fragment)
		})
	}
}

func TestEANRejectsBadInput(t *testing.T) {
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
			name: "ean13 too short", sym: EAN13, data: "59012341234", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected 12 digits, or 13 including the check digit, got 11",
		},
		{
			name: "ean13 too long", sym: EAN13, data: "59012341234567", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "got 14",
		},
		{
			name: "ean13 with a letter", sym: EAN13, data: "59012341234X", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected a digit",
		},
		{
			name: "ean8 empty", sym: EAN8, data: "", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected 7 digits",
		},
		{
			name: "upca too short", sym: UPCA, data: "0360002914", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected 11 digits",
		},
		{
			name: "upce number system 2", sym: UPCE, data: "2123456", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "number system and must be 0 or 1",
		},
		{
			name: "upce wrong length", sym: UPCE, data: "012345", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected 7 digits",
		},
		{
			name: "upce with a letter", sym: UPCE, data: "01234X6", opts: AutoEncodeOpts(),
			sentinel: ErrInvalidData, fragment: "expected a digit",
		},
		{
			name: "ean13 ecc level", sym: EAN13, data: "590123412345", opts: EncodeOpts{ECC: "M", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
		{
			name: "ean8 pinned version", sym: EAN8, data: "9638507", opts: EncodeOpts{Version: 1, Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "version",
		},
		{
			name: "upca pinned mask", sym: UPCA, data: "03600029145", opts: EncodeOpts{Mask: 1},
			sentinel: ErrUnsupportedOption, fragment: "mask",
		},
		{
			name: "upce ecc level", sym: UPCE, data: "0123456", opts: EncodeOpts{ECC: "L", Mask: -1},
			sentinel: ErrUnsupportedOption, fragment: "error-correction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantErr(t, encodeErr(t, tt.sym, tt.data, tt.opts), tt.sentinel, tt.fragment)
		})
	}
}

// TestUPCEExpansion pins the zero-suppression rules against published
// UPC-E/UPC-A pairs. Getting these wrong would produce a symbol that scans
// cleanly and reports the wrong product.
func TestUPCEExpansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		short     string
		wantUPCA  string
		wantCheck byte
	}{
		{name: "last digit 1", short: "0425261", wantUPCA: "04210000526", wantCheck: '4'},
		{name: "last digit 0", short: "0425260", wantUPCA: "04200000526", wantCheck: '5'},
		{name: "last digit 3", short: "0123453", wantUPCA: "01230000045", wantCheck: '1'},
		{name: "last digit 4", short: "0123454", wantUPCA: "01234000005", wantCheck: '3'},
		{name: "last digit 6", short: "0123456", wantUPCA: "01234500006", wantCheck: '5'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := upceExpand(tt.short)
			if got != tt.wantUPCA {
				t.Fatalf("upceExpand(%q) = %q, want %q", tt.short, got, tt.wantUPCA)
			}
			if c := gs1CheckDigit(got); c != tt.wantCheck {
				t.Errorf("check digit = %c, want %c", c, tt.wantCheck)
			}
		})
	}
}
