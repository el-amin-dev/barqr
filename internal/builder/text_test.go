package builder

import "testing"

func TestTextBuild(t *testing.T) {
	t.Parallel()

	b := textBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "plain text passes through",
			payload: map[string]any{"text": "Hello from barqr"},
			want:    "Hello from barqr",
		},
		{
			name:    "delimiters and unicode are untouched",
			payload: map[string]any{"text": hostileInput},
			want:    hostileInput,
		},
		{
			name:    "an absent payload is a missing field",
			payload: nil,
			wantErr: ErrMissingField,
		},
		{
			name:    "missing text is rejected",
			payload: map[string]any{},
			wantErr: ErrMissingField,
		},
		{
			name:    "a non-text value is rejected",
			payload: map[string]any{"text": []string{"a"}},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"text": "hi", "txet": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestTextBuildAcceptsItsStruct(t *testing.T) {
	t.Parallel()

	got, err := textBuilder{}.Build(TextPayload{Text: "from a struct"})
	if err != nil {
		t.Fatalf("Build(TextPayload) = error %v", err)
	}
	if got != "from a struct" {
		t.Errorf("Build(TextPayload) = %q", got)
	}
}

// TestTextParseDefersToStructuredBuilders pins the rule that makes text usable
// as a fallback when classifying a scanned code.
func TestTextParseDefersToStructuredBuilders(t *testing.T) {
	t.Parallel()

	b := textBuilder{}
	assertNotParsed(t, b,
		"",
		"https://example.com",
		"MAILTO:ada@example.com",
		"WIFI:T:WPA;S:x;P:12345678;;",
		"BEGIN:VCARD\r\nVERSION:3.0\r\nEND:VCARD\r\n",
		"otpauth://totp/a?secret=JBSWY3DPEHPK3PXP",
	)
	assertParsed(t, b, "just some words", map[string]any{"text": "just some words"})
	assertParsed(t, b, hostileInput, map[string]any{"text": hostileInput})
}
