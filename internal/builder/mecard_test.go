package builder

import "testing"

func TestMeCardBuild(t *testing.T) {
	t.Parallel()

	b := mecardBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "a name and a number",
			payload: map[string]any{"last_name": "Lovelace", "first_name": "Ada", "phone": "+44 20 7946 0958"},
			want:    "MECARD:N:Lovelace,Ada;TEL:+442079460958;;",
		},
		{
			name:    "the colon in a url is escaped, unlike in a vCard",
			payload: map[string]any{"first_name": "Ada", "url": "https://example.com"},
			want:    `MECARD:N:,Ada;URL:https\://example.com;;`,
		},
		{
			name: "every modelled field",
			payload: map[string]any{
				"last_name": "Lovelace", "first_name": "Ada", "phone": "+442079460958",
				"email": "ada@example.com", "url": "https://example.com",
				"address": "12 Bishopsgate", "note": "Met at the fair",
			},
			want: `MECARD:N:Lovelace,Ada;TEL:+442079460958;EMAIL:ada@example.com;` +
				`URL:https\://example.com;ADR:12 Bishopsgate;NOTE:Met at the fair;;`,
		},
		{
			name:    "a card with no name is rejected",
			payload: map[string]any{"phone": "+442079460958"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a bad email is rejected",
			payload: map[string]any{"first_name": "Ada", "email": "not an address"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a bad phone is rejected",
			payload: map[string]any{"first_name": "Ada", "phone": "call me"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"first_name": "Ada", "first_nam": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestMeCardEscapesHostileText(t *testing.T) {
	t.Parallel()

	b := mecardBuilder{}
	for _, field := range []string{"note", "address", "last_name"} {
		assertHostileRoundTrip(t, b, map[string]any{"first_name": "Ada", field: hostileInput}, field)
	}
}

func TestMeCardParse(t *testing.T) {
	t.Parallel()

	b := mecardBuilder{}
	assertParsed(t, b, "mecard:N:Lovelace,Ada;;",
		map[string]any{"last_name": "Lovelace", "first_name": "Ada"})

	assertNotParsed(t, b,
		"MECARD:N:Lovelace,Ada;",                // one terminator, not two
		"MECARD:N:Lovelace,Ada;BDAY:19151210;;", // a field this builder cannot rebuild
		"MECARD:N:Lovelace;;",                   // the name is not two components
		"MECARD:NOTE:hello;;",                   // no name at all
		"MECARD:N:Lovelace,Ada;TEL:call me;;",   // a number Build would reject
		"MECARD:;;",
	)
}

func TestDocomoEscaping(t *testing.T) {
	t.Parallel()

	// The DoCoMo set is backslash, semicolon, colon and comma. Unlike vCard,
	// the colon is in it.
	const input = `a\b;c:d,e`
	const want = `a\\b\;c\:d\,e`

	if got := docomoEscape(input); got != want {
		t.Fatalf("docomoEscape(%q) = %q, want %q", input, got, want)
	}
	if got := docomoUnescape(want); got != input {
		t.Errorf("docomoUnescape(%q) = %q, want %q", want, got, input)
	}
	// A field split must ignore escaped separators.
	if parts := docomoSplit(`a\;b;c`, ';'); len(parts) != 2 {
		t.Errorf("docomoSplit split at an escaped separator: %q", parts)
	}
	if got := docomoUnescape(`trailing\`); got != `trailing\` {
		t.Errorf("a dangling backslash was swallowed: %q", got)
	}
}
