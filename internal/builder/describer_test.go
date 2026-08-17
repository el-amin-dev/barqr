package builder_test

import (
	"testing"

	"github.com/el-amin-dev/barqr/internal/builder"
)

// TestLocationDescribe covers the optional Describer interface: a caller that
// asks what the resolver made of an input has to get an answer it can act on,
// not just the URL.
func TestLocationDescribe(t *testing.T) {
	t.Parallel()

	b, err := builder.Get("location")
	if err != nil {
		t.Fatalf("Get(location) returned error: %v", err)
	}

	d, ok := b.(builder.Describer)
	if !ok {
		t.Fatal("the location builder no longer implements Describer")
	}

	tests := []struct {
		name      string
		payload   map[string]any
		wantKind  string
		wantCoord bool
		wantLat   float64
		wantLng   float64
	}{
		{
			name:      "decimal coordinates",
			payload:   map[string]any{"location": "35.95277, 5.53753"},
			wantKind:  "coordinates",
			wantCoord: true, wantLat: 35.95277, wantLng: 5.53753,
		},
		{
			name:     "a street address falls through to a search",
			payload:  map[string]any{"location": "45 Rue Didouche Mourad, Alger"},
			wantKind: "address",
		},
		{
			name:     "an address in another script is still an address",
			payload:  map[string]any{"location": "45 شارع ديدوش مراد، الجزائر العاصمة"},
			wantKind: "address",
		},
		{
			name:     "an open location code",
			payload:  map[string]any{"location": "GQ62+CW Quero, Spain"},
			wantKind: "plus_code",
		},
		{
			name:      "a geo URI carries its coordinates",
			payload:   map[string]any{"location": "geo:35.95,5.53"},
			wantKind:  "geo_uri",
			wantCoord: true, wantLat: 35.95, wantLng: 5.53,
		},
		{
			name:      "coordinates recovered from another provider's link",
			payload:   map[string]any{"location": "https://www.openstreetmap.org/#map=17/35.95/5.53"},
			wantKind:  "map_link",
			wantCoord: true, wantLat: 35.95, wantLng: 5.53,
		},
		{
			name:     "a route",
			payload:  map[string]any{"location": "from Setif to Algiers"},
			wantKind: "directions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := d.Describe(tt.payload)
			if err != nil {
				t.Fatalf("Describe() returned error: %v", err)
			}

			if kind, _ := got["kind"].(string); kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", kind, tt.wantKind)
			}
			if conf, _ := got["confidence"].(float64); conf <= 0 || conf > 1 {
				t.Errorf("confidence = %v, want a value in (0, 1]", got["confidence"])
			}
			if url, _ := got["url"].(string); url == "" {
				t.Error("url is empty")
			}

			lat, hasLat := got["lat"].(float64)
			lng, hasLng := got["lng"].(float64)
			if hasLat != tt.wantCoord || hasLng != tt.wantCoord {
				t.Fatalf("coordinates present = %v, want %v (got %+v)",
					hasLat && hasLng, tt.wantCoord, got)
			}
			if !tt.wantCoord {
				// 0,0 is a real place in the Atlantic, so an absent coordinate
				// must be absent rather than zero.
				return
			}
			if diff := lat - tt.wantLat; diff > 1e-4 || diff < -1e-4 {
				t.Errorf("lat = %v, want %v", lat, tt.wantLat)
			}
			if diff := lng - tt.wantLng; diff > 1e-4 || diff < -1e-4 {
				t.Errorf("lng = %v, want %v", lng, tt.wantLng)
			}
		})
	}
}

func TestLocationDescribeRejections(t *testing.T) {
	t.Parallel()

	d, ok := mustGet(t, "location").(builder.Describer)
	if !ok {
		t.Fatal("the location builder no longer implements Describer")
	}

	tests := []struct {
		name    string
		payload map[string]any
	}{
		{"no location at all", map[string]any{}},
		{"an unknown field", map[string]any{"location": "Setif", "nonsense": "x"}},
		{"an invalid travel mode", map[string]any{"location": "Setif", "mode": "teleport"}},
		{"a zoom outside the range", map[string]any{"location": "35.95,5.53", "zoom": "99"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := d.Describe(tt.payload); err == nil {
				t.Fatal("Describe() succeeded, want an error")
			}
		})
	}
}

// TestOnlyLocationDescribes documents the design: Describer is optional, and a
// builder whose payload is the whole story does not implement it.
func TestOnlyLocationDescribes(t *testing.T) {
	t.Parallel()

	for _, b := range builder.All() {
		_, implements := b.(builder.Describer)
		if want := b.Name() == "location"; implements != want {
			t.Errorf("%s implements Describer = %v, want %v", b.Name(), implements, want)
		}
	}
}

// mustGet fetches a builder or fails the test.
func mustGet(t *testing.T, name string) builder.Builder {
	t.Helper()

	b, err := builder.Get(name)
	if err != nil {
		t.Fatalf("Get(%q) returned error: %v", name, err)
	}
	return b
}
