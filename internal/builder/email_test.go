package builder

import "testing"

func TestEmailBuild(t *testing.T) {
	t.Parallel()

	b := emailBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name: "address, subject and body",
			payload: map[string]any{
				"email": "hello@example.com", "subject": "Table for two",
				"body": "Friday at 8pm, please.",
			},
			want: "mailto:hello@example.com?subject=Table%20for%20two" +
				"&body=Friday%20at%208pm%2C%20please.",
		},
		{
			name:    "address alone carries no query",
			payload: map[string]any{"email": "hello@example.com"},
			want:    "mailto:hello@example.com",
		},
		{
			name:    "a plus is escaped rather than left to mean a space",
			payload: map[string]any{"email": "a@example.com", "subject": "1 + 1"},
			want:    "mailto:a@example.com?subject=1%20%2B%201",
		},
		{
			name:    "an address without a domain dot is rejected",
			payload: map[string]any{"email": "ada@localhost"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an address with a reserved character is rejected",
			payload: map[string]any{"email": "ada?x@example.com"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing address is rejected",
			payload: map[string]any{"subject": "hi"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"email": "a@example.com", "sbuject": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestEmailEscapesAHostileSubject(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, emailBuilder{}, map[string]any{
		"email": "ada@example.com", "subject": hostileInput,
	}, "subject")
	assertHostileRoundTrip(t, emailBuilder{}, map[string]any{
		"email": "ada@example.com", "body": hostileInput,
	}, "body")
}

func TestEmailParse(t *testing.T) {
	t.Parallel()

	b := emailBuilder{}
	assertParsed(t, b, "MAILTO:ada@example.com", map[string]any{"email": "ada@example.com"})
	assertParsed(t, b, "mailto:ada@example.com?subject=Hi%20there",
		map[string]any{"email": "ada@example.com", "subject": "Hi there"})

	assertNotParsed(t, b,
		"ada@example.com",                    // no scheme
		"mailto:not-an-address",              // not an address
		"mailto:a@example.com?cc=b@x.com",    // a parameter this builder drops
		"mailto:a@example.com?subject",       // a pair with no value
		"mailto:a@example.com?subject=&body=x", // an empty parameter
	)
}
