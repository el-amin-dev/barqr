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
			// The case from the field report: a number held nationally, with
			// nothing in the payload to say which country it belongs to.
			name:    "a region turns a national number into E.164",
			payload: map[string]any{"phone": "0664108852", "phone_region": "DZ"},
			want:    "tel:+213664108852",
		},
		{
			name:    "the region is case-insensitive",
			payload: map[string]any{"phone": "0664108852", "phone_region": "dz"},
			want:    "tel:+213664108852",
		},
		{
			// Rule 2: the caller has already said which country this is.
			// Re-parsing it against another is how a right number goes wrong.
			name:    "an international number ignores the region",
			payload: map[string]any{"phone": "+44 20 7946 0958", "phone_region": "DZ"},
			want:    "tel:+442079460958",
		},
		{
			name:    "an unknown region is rejected",
			payload: map[string]any{"phone": "0664108852", "phone_region": "XX"},
			wantErr: ErrInvalidPayload,
		},
		{
			// Silently emitting a number no dialler can reach is the failure
			// this feature exists to remove, so it is an error, not a passthrough.
			name:    "a number invalid for its region is rejected",
			payload: map[string]any{"phone": "000", "phone_region": "DZ"},
			wantErr: ErrInvalidPayload,
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
