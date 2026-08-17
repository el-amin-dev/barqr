package builder

import "testing"

func TestRawBuild(t *testing.T) {
	t.Parallel()

	b := rawBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "data passes through unvalidated",
			payload: map[string]any{"data": "ANYTHING::;;\\"},
			want:    "ANYTHING::;;\\",
		},
		{
			name:    "delimiters and unicode are untouched",
			payload: map[string]any{"data": hostileInput},
			want:    hostileInput,
		},
		{
			name:    "whitespace is content, not emptiness",
			payload: map[string]any{"data": "   "},
			want:    "   ",
		},
		{
			name:    "missing data is rejected",
			payload: map[string]any{},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"data": "x", "date": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

// TestRawParseClaimsEverything documents the property that makes raw the
// catch-all: it must be tried last when classifying a scanned code.
func TestRawParseClaimsEverything(t *testing.T) {
	t.Parallel()

	b := rawBuilder{}
	for _, raw := range []string{"x", "https://example.com", "WIFI:T:WPA;;", hostileInput} {
		if _, ok := b.Parse(raw); !ok {
			t.Errorf("Parse(%q) = not-my-form, want a payload", raw)
		}
	}
	assertNotParsed(t, b, "")
}

func TestRawBuildAcceptsItsStruct(t *testing.T) {
	t.Parallel()

	got, err := rawBuilder{}.Build(&RawPayload{Data: "via a pointer"})
	if err != nil {
		t.Fatalf("Build(*RawPayload) = error %v", err)
	}
	if got != "via a pointer" {
		t.Errorf("Build(*RawPayload) = %q", got)
	}
}
