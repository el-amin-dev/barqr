package builder

import "testing"

func TestGeoBuild(t *testing.T) {
	t.Parallel()

	b := geoBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "a plain coordinate",
			payload: map[string]any{"latitude": "51.50722", "longitude": "-0.1275"},
			want:    "geo:51.50722,-0.1275",
		},
		{
			name: "altitude and query",
			payload: map[string]any{
				"latitude": 51.50722, "longitude": -0.1275,
				"altitude": 11, "query": "Nelson's Column",
			},
			want: "geo:51.50722,-0.1275,11?q=Nelson%27s%20Column",
		},
		{
			name:    "a zero altitude is kept, being different from no altitude",
			payload: map[string]any{"latitude": 0, "longitude": 0, "altitude": 0},
			want:    "geo:0,0,0",
		},
		{
			name:    "a latitude beyond the pole is rejected",
			payload: map[string]any{"latitude": 91, "longitude": 0},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a longitude beyond the meridian is rejected",
			payload: map[string]any{"latitude": 0, "longitude": -181},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a non-numeric coordinate is rejected",
			payload: map[string]any{"latitude": "north", "longitude": 0},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing longitude is rejected",
			payload: map[string]any{"latitude": 51.5},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"latitude": 0, "longitude": 0, "latitide": 1},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestGeoEscapesAHostileQuery(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, geoBuilder{}, map[string]any{
		"latitude": 51.5, "longitude": -0.12, "query": hostileInput,
	}, "query")
}

func TestGeoParse(t *testing.T) {
	t.Parallel()

	b := geoBuilder{}
	assertParsed(t, b, "GEO:51.5,-0.12", map[string]any{"latitude": 51.5, "longitude": -0.12})
	assertParsed(t, b, "geo:51.5,-0.12,11",
		map[string]any{"latitude": 51.5, "longitude": -0.12, "altitude": 11.0})

	assertNotParsed(t, b,
		"geo:51.5",                // one coordinate
		"geo:51.5,-0.12,1,2",      // too many
		"geo:51.5,-0.12;u=35",     // an RFC 5870 parameter this builder cannot rebuild
		"geo:91,0",                // out of range
		"geo:51.50,-0.12",         // not in the shortest form Build emits
		"geo:north,west",
	)
}
