package builder

import "testing"

func TestSMSBuild(t *testing.T) {
	t.Parallel()

	b := smsBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "number and message",
			payload: map[string]any{"phone": "+44 20 7946 0958", "message": "I am outside"},
			want:    "SMSTO:+442079460958:I am outside",
		},
		{
			name:    "no message means no trailing separator",
			payload: map[string]any{"phone": "+442079460958"},
			want:    "SMSTO:+442079460958",
		},
		{
			name:    "a colon in the message is safe, being the tail of the record",
			payload: map[string]any{"phone": "+442079460958", "message": "re: the meeting"},
			want:    "SMSTO:+442079460958:re: the meeting",
		},
		{
			name:    "a bad number is rejected",
			payload: map[string]any{"phone": "not a number", "message": "hi"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing number is rejected",
			payload: map[string]any{"message": "hi"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"phone": "+442079460958", "mesage": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestSMSCarriesAHostileMessageVerbatim(t *testing.T) {
	t.Parallel()

	// SMSTO defines no escaping, so the guarantee is that the message is the
	// untouched tail of the record.
	assertHostileRoundTrip(t, smsBuilder{}, map[string]any{
		"phone": "+442079460958", "message": hostileInput,
	}, "message")
}

func TestSMSParse(t *testing.T) {
	t.Parallel()

	b := smsBuilder{}
	assertParsed(t, b, "smsto:+442079460958", map[string]any{"phone": "+442079460958"})
	assertParsed(t, b, "SMSTO:+442079460958:hi",
		map[string]any{"phone": "+442079460958", "message": "hi"})

	assertNotParsed(t, b,
		"sms:+442079460958?body=hi",  // the RFC form this builder deliberately avoids
		"SMSTO:+442079460958:",       // an empty message it would never emit
		"SMSTO:+44 20 7946 0958:hi",  // a number not in the normal form
		"SMSTO:",
	)
}
