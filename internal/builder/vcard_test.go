package builder

import (
	"strings"
	"testing"
)

func TestVCardBuild(t *testing.T) {
	t.Parallel()

	b := vcardBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "a name alone is a valid card",
			payload: map[string]any{"first_name": "Ada", "last_name": "Lovelace"},
			want: "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada;;;\r\n" +
				"FN:Ada Lovelace\r\nEND:VCARD\r\n",
		},
		{
			name:    "an organisation alone fills the mandatory FN",
			payload: map[string]any{"org": "Analytical Engines Ltd"},
			want: "BEGIN:VCARD\r\nVERSION:3.0\r\nN:;;;;\r\n" +
				"FN:Analytical Engines Ltd\r\nORG:Analytical Engines Ltd\r\nEND:VCARD\r\n",
		},
		{
			name: "phones are typed and normalised",
			payload: map[string]any{
				"first_name": "Ada", "phone": "+44 20 7946 0958", "mobile": "+44 7700 900123",
			},
			want: "BEGIN:VCARD\r\nVERSION:3.0\r\nN:;Ada;;;\r\nFN:Ada\r\n" +
				"TEL;TYPE=WORK,VOICE:+442079460958\r\nTEL;TYPE=CELL,VOICE:+447700900123\r\n" +
				"END:VCARD\r\n",
		},
		{
			name: "an address is a structured value",
			payload: map[string]any{
				"first_name": "Ada", "street": "12 Bishopsgate", "city": "London",
				"postal_code": "EC2N 3AR", "country": "United Kingdom",
			},
			want: "BEGIN:VCARD\r\nVERSION:3.0\r\nN:;Ada;;;\r\nFN:Ada\r\n" +
				"ADR;TYPE=WORK:;;12 Bishopsgate;London;;EC2N 3AR;United Kingdom\r\n" +
				"END:VCARD\r\n",
		},
		{
			name:    "a card with nothing to name it by is rejected",
			payload: map[string]any{"note": "who?"},
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
			name:    "a bad url is rejected",
			payload: map[string]any{"first_name": "Ada", "url": "javascript:alert(1)"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"first_name": "Ada", "firstname": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestVCardEscapesHostileText(t *testing.T) {
	t.Parallel()

	b := vcardBuilder{}
	for _, field := range []string{"note", "org", "last_name", "city"} {
		payload := map[string]any{"first_name": "Ada", field: hostileInput}
		assertHostileRoundTrip(t, b, payload, field)
	}

	raw, err := b.Build(map[string]any{"first_name": "Ada", "note": hostileInput})
	if err != nil {
		t.Fatalf("Build = error %v", err)
	}
	// Every physical line ends with CRLF, so a newline that escaped its
	// encoding would show up as an LF with no CR in front of it.
	if strings.Count(raw, "\n") != strings.Count(raw, "\r\n") {
		t.Errorf("a raw newline leaked into %q", raw)
	}
}

func TestVCardParse(t *testing.T) {
	t.Parallel()

	b := vcardBuilder{}
	assertParsed(t, b,
		"BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada;;;\r\nFN:Ada Lovelace\r\nEND:VCARD\r\n",
		map[string]any{"first_name": "Ada", "last_name": "Lovelace"})

	assertNotParsed(t, b,
		// 4.0 escapes differently and is not what this builder emits.
		"BEGIN:VCARD\r\nVERSION:4.0\r\nN:Lovelace;Ada;;;\r\nFN:Ada\r\nEND:VCARD\r\n",
		// A property that would be lost on a rebuild.
		"BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada;;;\r\nBDAY:19151210\r\nEND:VCARD\r\n",
		// Bare LF instead of CRLF.
		"BEGIN:VCARD\nVERSION:3.0\nN:Lovelace;Ada;;;\nEND:VCARD\n",
		// Nothing to name the card by.
		"BEGIN:VCARD\r\nVERSION:3.0\r\nNOTE:who?\r\nEND:VCARD\r\n",
		// A structured value with the wrong number of components.
		"BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada\r\nEND:VCARD\r\n",
		"BEGIN:VCARD\r\nEND:VCARD\r\n",
		"not a card",
	)
}

func TestVCardTextEscaping(t *testing.T) {
	t.Parallel()

	// The rule under test is RFC 6350 §3.4: backslash, comma, semicolon and
	// newline are escaped, and the colon is not.
	const input = `a\b,c;d:e` + "\r\n" + "f"
	const want = `a\\b\,c\;d:e\nf`

	if got := escapeTextValue(input); got != want {
		t.Fatalf("escapeTextValue(%q) = %q, want %q", input, got, want)
	}
	if got := unescapeTextValue(want); got != "a\\b,c;d:e\nf" {
		t.Errorf("unescapeTextValue(%q) = %q", want, got)
	}
	// An unknown escape yields the character it precedes, and a dangling
	// backslash is kept rather than swallowed.
	if got := unescapeTextValue(`a\qb\`); got != `aqb\` {
		t.Errorf("unescapeTextValue of an odd input = %q", got)
	}
}
