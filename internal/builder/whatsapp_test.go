package builder

import "testing"

func TestWhatsAppBuild(t *testing.T) {
	t.Parallel()

	b := whatsappBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "number and message",
			payload: map[string]any{"phone": "+44 20 7946 0958", "message": "Hello!"},
			want:    "https://wa.me/442079460958?text=Hello%21",
		},
		{
			name:    "the leading plus is dropped, as wa.me requires",
			payload: map[string]any{"phone": "+442079460958"},
			want:    "https://wa.me/442079460958",
		},
		{
			name:    "a bad number is rejected",
			payload: map[string]any{"phone": "12"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing number is rejected",
			payload: map[string]any{"message": "hi"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"phone": "+442079460958", "text": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestWhatsAppEscapesAHostileMessage(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, whatsappBuilder{}, map[string]any{
		"phone": "+442079460958", "message": hostileInput,
	}, "message")
}

func TestWhatsAppParse(t *testing.T) {
	t.Parallel()

	b := whatsappBuilder{}
	assertParsed(t, b, "https://WA.ME/442079460958",
		map[string]any{"phone": "442079460958"})

	assertNotParsed(t, b,
		"http://wa.me/442079460958",                 // not https
		"https://api.whatsapp.com/send?phone=44207", // the long form is not emitted
		"https://wa.me/+442079460958",               // a plus wa.me would not accept
		"https://wa.me/44 207",                      // not in the normal form
		"https://wa.me/442079460958?text=",          // an empty parameter
	)
}
