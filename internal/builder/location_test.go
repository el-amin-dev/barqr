package builder

import (
	"strings"
	"testing"
)

func TestLocationBuild(t *testing.T) {
	t.Parallel()

	b := locationBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "a plain address is searched as typed",
			payload: map[string]any{"location": "45 Rue Didouche Mourad, Alger"},
			want: "https://www.google.com/maps/search/?api=1&query=" +
				"45%20Rue%20Didouche%20Mourad%2C%20Alger",
		},
		{
			// The raw text is sent rather than the normalised form, so an
			// address in a non-Latin script reaches Google unmangled.
			name:    "an Arabic address keeps its own script",
			payload: map[string]any{"location": "45 شارع ديدوش مراد، الجزائر العاصمة"},
			want: "https://www.google.com/maps/search/?api=1&query=" +
				"45%20%D8%B4%D8%A7%D8%B1%D8%B9%20%D8%AF%D9%8A%D8%AF%D9%88%D8%B4%20" +
				"%D9%85%D8%B1%D8%A7%D8%AF%D8%8C%20%D8%A7%D9%84%D8%AC%D8%B2%D8%A7%D8%A6%D8%B1%20" +
				"%D8%A7%D9%84%D8%B9%D8%A7%D8%B5%D9%85%D8%A9",
		},
		{
			name:    "coordinates are normalised to seven decimal places",
			payload: map[string]any{"location": "35.95, 5.53"},
			want:    "https://www.google.com/maps/search/?api=1&query=35.9500000%2C5.5300000",
		},
		{
			// Arabic-Indic digits fold to ASCII before detection, and a zoom
			// switches the output to the legacy form that can carry one.
			name:    "Arabic-Indic digits with a zoom use the q form",
			payload: map[string]any{"location": "٣٥.٩٥، ٥.٥٣", "zoom": 18},
			want:    "https://www.google.com/maps?q=35.9500000,5.5300000&z=18",
		},
		{
			name:    "a plus code survives as %2B rather than a space",
			payload: map[string]any{"location": "GQ62+CW Quero, Spain"},
			want: "https://www.google.com/maps/search/?api=1&query=" +
				"GQ62%2BCW%20Quero%2C%20Spain",
		},
		{
			name: "an origin turns the location into a destination",
			payload: map[string]any{
				"location": "Algiers", "origin": "Setif", "mode": "driving",
			},
			want: "https://www.google.com/maps/dir/?api=1&destination=Algiers" +
				"&origin=Setif&travelmode=driving",
		},
		{
			// "from A to B" names both ends itself, so no origin field is
			// needed and the parsed payload comes back in the explicit form.
			name:    "from A to B is read as a route",
			payload: map[string]any{"location": "from Setif to Algiers"},
			want:    "https://www.google.com/maps/dir/?api=1&destination=Algiers&origin=Setif",
		},
		{
			name:    "an arrow is read as a route",
			payload: map[string]any{"location": "Setif -> Algiers"},
			want:    "https://www.google.com/maps/dir/?api=1&destination=Algiers&origin=Setif",
		},
		{
			name:    "language and region are appended",
			payload: map[string]any{"location": "Setif", "language": "ar", "region": "dz"},
			want:    "https://www.google.com/maps/search/?api=1&query=Setif&hl=ar&gl=dz",
		},
		{
			name:    "a missing location is rejected",
			payload: map[string]any{"origin": "Setif"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a blank location is rejected",
			payload: map[string]any{"location": "   "},
			wantErr: ErrMissingField,
		},
		{
			// Bidi marks are not whitespace, so they survive the required-field
			// check and only vanish during normalisation.
			name:    "a location of nothing but bidi marks is rejected",
			payload: map[string]any{"location": "\u200f\u200e\ufeff"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown travel mode is rejected",
			payload: map[string]any{"location": "Algiers", "origin": "Setif", "mode": "flying"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a zoom below the scale is rejected",
			payload: map[string]any{"location": "35.95, 5.53", "zoom": 0},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a zoom beyond the scale is rejected",
			payload: map[string]any{"location": "35.95, 5.53", "zoom": 22},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a fractional zoom is rejected",
			payload: map[string]any{"location": "35.95, 5.53", "zoom": 18.5},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a non-numeric zoom is rejected",
			payload: map[string]any{"location": "35.95, 5.53", "zoom": "close"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"location": "Setif", "locatoin": "Algiers"},
			wantErr: ErrInvalidPayload,
		},
	})
}

// TestLocationErrorNamesTheValidModes keeps the rejection actionable: a caller
// who sent "cycling" has to be able to read "bicycling" out of the message.
func TestLocationErrorNamesTheValidModes(t *testing.T) {
	t.Parallel()

	_, err := locationBuilder{}.Build(map[string]any{
		"location": "Algiers", "origin": "Setif", "mode": "cycling",
	})
	if err == nil {
		t.Fatal("Build accepted an unknown travel mode")
	}
	for _, mode := range locationModes {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("error %q does not name the valid mode %q", err, mode)
		}
	}
}

func TestLocationEscapesAHostileLocation(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, locationBuilder{},
		map[string]any{"location": hostileInput}, "location")
}

func TestLocationParse(t *testing.T) {
	t.Parallel()

	b := locationBuilder{}
	assertParsed(t, b, "https://www.google.com/maps/search/?api=1&query=Setif",
		map[string]any{"location": "Setif"})
	assertParsed(t, b,
		"https://www.google.com/maps/dir/?api=1&destination=Algiers&origin=Setif"+
			"&travelmode=transit",
		map[string]any{"location": "Algiers", "origin": "Setif", "mode": "transit"})
	assertParsed(t, b, "https://www.google.com/maps?q=35.9500000,5.5300000&z=12",
		map[string]any{"location": "35.9500000,5.5300000", "zoom": 12.0})
	assertParsed(t, b, "https://www.google.com/maps/search/?api=1&query=Setif&hl=ar&gl=dz",
		map[string]any{"location": "Setif", "language": "ar", "region": "dz"})

	assertNotParsed(t, b,
		// Payloads belonging to other builders. Parse is how a scanned code is
		// classified, so claiming one of these would misroute it.
		"https://example.com/menu",
		"geo:51.5,-0.12",
		"tel:+441234567890",
		"MECARD:N:Lovelace,Ada;;",
		// A Google link Build passes through untouched: nothing in it names the
		// payload that produced it.
		"https://maps.app.goo.gl/6Xz63Mu1nqHgc1o89",
		"https://www.google.com/maps/@35.95,5.53,17z",
		// A query Build would have renormalised to 35.9500000,5.5300000.
		"https://www.google.com/maps/search/?api=1&query=35.95%2C%205.53",
		// The right shape with the wrong details.
		"https://www.google.com/maps/search/?query=Setif",                   // no api=1
		"http://www.google.com/maps/search/?api=1&query=Setif",              // not https
		"https://maps.google.com/maps/search/?api=1&query=Setif",            // another host
		"https://www.google.com/maps/search/?api=1&query=",                  // no query
		"https://www.google.com/maps?q=35.9500000,5.5300000",                // no zoom
		"https://www.google.com/maps?q=35.9500000,5.5300000&z=99",           // zoom off the scale
		"https://www.google.com/maps/dir/?api=1&origin=Setif&destination=A", // parameters reordered
		"https://www.google.com/maps/dir/?api=1&destination=A&travelmode=flying",
		"https://www.google.com/maps/search/?api=1&query=Setif&utm_source=x", // an extra parameter
	)
}
