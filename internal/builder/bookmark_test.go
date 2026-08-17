package builder

import "testing"

func TestBookmarkBuild(t *testing.T) {
	t.Parallel()

	b := bookmarkBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "title and link",
			payload: map[string]any{"title": "Example: the menu", "url": "https://example.com/menu"},
			want:    `MEBKM:TITLE:Example\: the menu;URL:https\://example.com/menu;;`,
		},
		{
			name:    "a link alone",
			payload: map[string]any{"url": "example.com"},
			want:    `MEBKM:URL:https\://example.com;;`,
		},
		{
			name:    "a missing link is rejected",
			payload: map[string]any{"title": "no link"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unsafe scheme is rejected",
			payload: map[string]any{"url": "javascript:alert(1)"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"url": "https://example.com", "titel": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestBookmarkEscapesAHostileTitle(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, bookmarkBuilder{}, map[string]any{
		"title": hostileInput, "url": "https://example.com",
	}, "title")
}

func TestBookmarkParse(t *testing.T) {
	t.Parallel()

	b := bookmarkBuilder{}
	assertParsed(t, b, `mebkm:URL:https\://example.com;;`,
		map[string]any{"url": "https://example.com"})

	assertNotParsed(t, b,
		`MEBKM:URL:https\://example.com;`, // one terminator, not two
		"MEBKM:TITLE:no link;;",           // no url
		`MEBKM:URL:example.com;;`,         // a link Build would have normalised
		"MEBKM:X:1;;",                     // a field this builder cannot rebuild
	)
}
