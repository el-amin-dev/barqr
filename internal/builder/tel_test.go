package builder

import "testing"

func TestTelBuild(t *testing.T) {
	t.Parallel()

	b := telBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "formatting is stripped and the plus kept",
			payload: map[string]any{"phone": "+44 (0)20 7946-0958"},
			want:    "tel:+4402079460958",
		},
		{
			name:    "a national number keeps no plus",
			payload: map[string]any{"phone": "020 7946 0958"},
			want:    "tel:02079460958",
		},
		{
			name:    "a number given as JSON digits is accepted",
			payload: map[string]any{"phone": 442079460958},
			want:    "tel:442079460958",
		},
		{
			name:    "letters are rejected",
			payload: map[string]any{"phone": "0800-FLOWERS"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "too few digits is rejected",
			payload: map[string]any{"phone": "12"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "more than fifteen digits is rejected",
			payload: map[string]any{"phone": "+1234567890123456"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "delimiters and unicode are rejected as a number",
			payload: map[string]any{"phone": hostileInput},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing number is rejected",
			payload: map[string]any{},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"phone": "+442079460958", "phon": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestTelParse(t *testing.T) {
	t.Parallel()

	b := telBuilder{}
	assertParsed(t, b, "TEL:+442079460958", map[string]any{"phone": "+442079460958"})

	assertNotParsed(t, b,
		"+442079460958",           // no scheme
		"tel:+44 20 7946 0958",    // not in the normal form Build emits
		"tel:+442079460958;ext=9", // a parameter this builder cannot rebuild
		"tel:",
	)
}
