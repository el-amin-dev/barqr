package encoder

import (
	"errors"
	"slices"
	"testing"
)

// unavailableNames lists the symbologies the default build declares but cannot
// encode. It is written out rather than derived from zintOnly so that dropping
// one by accident fails a test.
func unavailableNames() []string {
	return []string{
		"code11", "msi", "plessey", "telepen", "pharmacode", "rm4scc", "maxicode", "dotcode",
		"gs1-128", "gs1-datamatrix", "databar", "databar-expanded", "postnet", "planet",
		"japanpost", "kix", "code16k", "codablock-f", "hanxin", "grid-matrix",
	}
}

func TestUnavailableSymbologiesAreRegistered(t *testing.T) {
	t.Parallel()

	declared := make([]string, 0, len(zintOnly()))
	for _, caps := range zintOnly() {
		declared = append(declared, caps.Name)
	}

	for _, name := range unavailableNames() {
		if !slices.Contains(declared, name) {
			t.Errorf("%q is not declared by zintOnly", name)
		}
	}
	if len(declared) != len(unavailableNames()) {
		t.Errorf("zintOnly declares %d symbologies, want %d", len(declared), len(unavailableNames()))
	}
}

func TestUnavailableSymbologiesReportWhyTheyAreMissing(t *testing.T) {
	t.Parallel()

	byName := make(map[string]Capabilities, len(All()))
	for _, caps := range All() {
		byName[caps.Name] = caps
	}

	for _, name := range unavailableNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caps, ok := byName[name]
			if !ok {
				t.Fatalf("%q is missing from All", name)
			}
			if caps.Available {
				t.Error("reported as available")
			}
			if caps.Reason != zintReason {
				t.Errorf("reason = %q, want %q", caps.Reason, zintReason)
			}
			if caps.Title == "" || caps.Charset == "" {
				t.Errorf("capabilities are incomplete: %+v", caps)
			}
			if caps.Kind != Kind1D && caps.Kind != Kind2D {
				t.Errorf("kind = %q, want 1d or 2d", caps.Kind)
			}
			if caps.Kind == Kind2D && caps.HRI {
				t.Error("a 2D symbology should not claim human-readable text")
			}

			_, err := Get(name)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Get returned %v, want %v", err, ErrUnavailable)
			}
		})
	}
}

// TestUnavailableEncodeFails covers the placeholder encoder itself: the
// registry hands it out to anything that walks the registry directly rather
// than going through Get.
func TestUnavailableEncodeFails(t *testing.T) {
	t.Parallel()

	registry.RLock()
	e := registry.byName["maxicode"]
	registry.RUnlock()

	if e == nil {
		t.Fatal("maxicode is not registered")
	}
	if _, err := e.Encode("anything", AutoEncodeOpts()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Encode returned %v, want %v", err, ErrUnavailable)
	}
	if e.Name() != "maxicode" {
		t.Errorf("Name = %q, want %q", e.Name(), "maxicode")
	}
}
