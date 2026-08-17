package builder

import "testing"

func TestAppBuild(t *testing.T) {
	t.Parallel()

	b := appBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "an App Store link",
			payload: map[string]any{"platform": "ios", "ios_id": "310633997"},
			want:    "https://apps.apple.com/app/id310633997",
		},
		{
			name:    "the id may be pasted with its store prefix",
			payload: map[string]any{"platform": "ios", "ios_id": "id310633997"},
			want:    "https://apps.apple.com/app/id310633997",
		},
		{
			name:    "a Play Store link",
			payload: map[string]any{"platform": "android", "android_package": "com.example.app"},
			want:    "https://play.google.com/store/apps/details?id=com.example.app",
		},
		{
			name: "both platforms use an intent url with an App Store fallback",
			payload: map[string]any{
				"platform": "both", "ios_id": "310633997", "android_package": "com.example.app",
			},
			want: "intent://details?id=com.example.app#Intent;scheme=market;" +
				"package=com.android.vending;S.browser_fallback_url=" +
				"https%3A%2F%2Fapps.apple.com%2Fapp%2Fid310633997;end",
		},
		{
			name:    "an ios id that is not numeric is rejected",
			payload: map[string]any{"platform": "ios", "ios_id": "com.example.app"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a package name without a dot is rejected",
			payload: map[string]any{"platform": "android", "android_package": "example"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an ios id alongside platform android is rejected",
			payload: map[string]any{"platform": "android", "android_package": "com.a.b", "ios_id": "1"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a package alongside platform ios is rejected",
			payload: map[string]any{"platform": "ios", "ios_id": "310633997", "android_package": "com.a.b"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "both without an ios id is rejected",
			payload: map[string]any{"platform": "both", "android_package": "com.example.app"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown platform is rejected",
			payload: map[string]any{"platform": "windows", "ios_id": "310633997"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing platform is rejected",
			payload: map[string]any{"ios_id": "310633997"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"platform": "ios", "ios_id": "310633997", "platfor": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestAppParse(t *testing.T) {
	t.Parallel()

	b := appBuilder{}
	assertParsed(t, b, "https://apps.apple.com/app/id310633997",
		map[string]any{"platform": "ios", "ios_id": "310633997"})
	assertParsed(t, b, "https://play.google.com/store/apps/details?id=com.example.app",
		map[string]any{"platform": "android", "android_package": "com.example.app"})

	assertNotParsed(t, b,
		// A localised store URL this builder does not emit and could not rebuild.
		"https://apps.apple.com/gb/app/example/id310633997",
		// A Play URL with extra parameters.
		"https://play.google.com/store/apps/details?id=com.example.app&hl=en",
		// An intent URL aimed somewhere else.
		"intent://details?id=com.example.app#Intent;scheme=http;end",
		// An intent URL with no App Store fallback to recover the ios id from.
		"intent://details?id=com.example.app#Intent;scheme=market;package=com.android.vending;end",
		"https://example.com",
	)
}
