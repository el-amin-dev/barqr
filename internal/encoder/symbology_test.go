package encoder

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// samplePayloads is one payload per available symbology that its own
// Capabilities declare valid. The shared test below fails when an encoder is
// registered without an entry here, which is the cheapest way to keep a new
// symbology from arriving untested.
var samplePayloads = map[string]string{
	QR:         "https://example.test/barqr",
	Code128:    "BARQR-128",
	Code39:     "BARQR 39",
	Code93:     "BARQR93",
	Codabar:    "A12345B",
	EAN13:      "590123412345",
	EAN8:       "9638507",
	UPCA:       "03600029145",
	UPCE:       "0123456",
	ITF:        "1234567890",
	ITF14:      "1540141453247",
	TwoOfFive:  "12345670",
	DataMatrix: "barqr data matrix",
	Aztec:      "barqr aztec",
	PDF417:     "barqr pdf417",
}

// encodeOK encodes with the automatic options and fails the test on any error.
func encodeOK(t *testing.T, sym, data string) Matrix {
	t.Helper()

	e, err := Get(sym)
	if err != nil {
		t.Fatalf("Get(%q): %v", sym, err)
	}
	m, err := e.Encode(data, AutoEncodeOpts())
	if err != nil {
		t.Fatalf("%s: Encode(%q): %v", sym, data, err)
	}
	return m
}

// encodeErr expects an encode to fail and returns the error.
func encodeErr(t *testing.T, sym, data string, o EncodeOpts) error {
	t.Helper()

	e, err := Get(sym)
	if err != nil {
		t.Fatalf("Get(%q): %v", sym, err)
	}
	m, err := e.Encode(data, o)
	if err == nil {
		t.Fatalf("%s: Encode(%q) unexpectedly produced a %dx%d matrix", sym, data, m.Cols, m.Rows)
	}
	return err
}

// wantErr asserts the sentinel and, when given, a fragment of the message that
// makes the error actionable.
func wantErr(t *testing.T, err error, sentinel error, fragment string) {
	t.Helper()

	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v: want %v", err, sentinel)
	}
	if fragment != "" && !strings.Contains(err.Error(), fragment) {
		t.Errorf("error %q does not mention %q", err, fragment)
	}
}

func TestEveryAvailableEncoderProducesAUsableMatrix(t *testing.T) {
	t.Parallel()

	for _, caps := range All() {
		t.Run(caps.Name, func(t *testing.T) {
			t.Parallel()

			if caps.Title == "" || caps.Charset == "" || caps.Kind == "" {
				t.Errorf("capabilities are incomplete: %+v", caps)
			}
			if caps.QuietZone < 0 {
				t.Errorf("quiet zone %d is negative", caps.QuietZone)
			}
			if !caps.Available {
				if caps.Reason == "" {
					t.Error("unavailable symbology carries no reason")
				}
				return
			}

			data, ok := samplePayloads[caps.Name]
			if !ok {
				t.Fatalf("no sample payload: add one to samplePayloads")
			}

			m := encodeOK(t, caps.Name, data)
			switch {
			case m.Cols <= 0 || m.Rows <= 0:
				t.Errorf("matrix is %dx%d", m.Cols, m.Rows)
			case m.Dark() == 0:
				t.Error("matrix has no dark modules")
			case m.Symbology != caps.Name:
				t.Errorf("matrix symbology %q, want %q", m.Symbology, caps.Name)
			case m.Kind != caps.Kind:
				t.Errorf("matrix kind %q, want %q", m.Kind, caps.Kind)
			case m.QuietZone < 0:
				t.Errorf("matrix quiet zone %d is negative", m.QuietZone)
			}

			if caps.Kind == Kind1D {
				if m.Rows != 1 {
					t.Errorf("linear matrix has %d rows, want 1", m.Rows)
				}
				if m.HRI == "" {
					t.Error("linear matrix carries no human-readable text")
				}
			} else if m.HRI != "" {
				t.Errorf("2D matrix carries human-readable text %q", m.HRI)
			}
		})
	}
}

func TestNamesListsEverySymbology(t *testing.T) {
	t.Parallel()

	names := Names()
	if !slices.IsSorted(names) {
		t.Error("Names is not sorted")
	}
	if len(names) != len(All()) {
		t.Fatalf("Names has %d entries, All has %d", len(names), len(All()))
	}

	// Every encoder this package registers, available or not.
	want := append([]string{
		QR, Code128, Code39, Code93, Codabar, EAN13, EAN8, UPCA, UPCE,
		ITF, ITF14, TwoOfFive, DataMatrix, Aztec, PDF417,
	}, unavailableNames()...)

	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("%q is missing from Names", name)
		}
	}
}

func TestGetRejectsAnUnknownSymbology(t *testing.T) {
	t.Parallel()

	_, err := Get("not-a-symbology")
	wantErr(t, err, ErrUnknownSymbology, "not-a-symbology")
}
