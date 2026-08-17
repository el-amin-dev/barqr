package builder

import (
	"errors"
	"strings"
	"testing"
)

func TestURLBuild(t *testing.T) {
	t.Parallel()

	b := urlBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "https is added when no scheme is given",
			payload: map[string]any{"url": "example.com/menu"},
			want:    "https://example.com/menu",
		},
		{
			name:    "scheme and host are lower-cased, the path is not",
			payload: map[string]any{"url": "HTTPS://EXAMPLE.COM/Menu?Item=1"},
			want:    "https://example.com/Menu?Item=1",
		},
		{
			name:    "mailto and tel are safe schemes",
			payload: map[string]any{"url": "mailto:ada@example.com"},
			want:    "mailto:ada@example.com",
		},
		{
			name:    "surrounding whitespace is trimmed",
			payload: map[string]any{"url": "  https://example.com  "},
			want:    "https://example.com",
		},
		{
			name:    "an unsafe scheme is rejected",
			payload: map[string]any{"url": "javascript:alert(1)"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an http url with no host is rejected",
			payload: map[string]any{"url": "http://"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an embedded space is rejected rather than escaped",
			payload: map[string]any{"url": "https://example.com/a b"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "delimiters and unicode are rejected as a link",
			payload: map[string]any{"url": hostileInput},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing url is rejected",
			payload: map[string]any{"allow_any": "true"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a non-boolean allow_any is rejected",
			payload: map[string]any{"url": "https://example.com", "allow_any": "maybe"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"url": "https://example.com", "urls": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

// TestURLAllowAnyIsBuildOnly pins the documented asymmetry: Build will emit an
// unusual scheme on request, but Parse never claims one.
func TestURLAllowAnyIsBuildOnly(t *testing.T) {
	t.Parallel()

	b := urlBuilder{}
	got, err := b.Build(URLPayload{URL: "ftp://files.example.com/x", AllowAny: true})
	if err != nil {
		t.Fatalf("Build with allow_any = error %v", err)
	}
	if got != "ftp://files.example.com/x" {
		t.Fatalf("Build with allow_any = %q", got)
	}
	assertNotParsed(t, b, got)
}

func TestURLParseRequiresAScheme(t *testing.T) {
	t.Parallel()

	b := urlBuilder{}
	// Without this rule the url builder would claim every bare word and the
	// text builder would never see one.
	assertNotParsed(t, b, "example.com", "", "not a url at all")
	assertParsed(t, b, "HTTP://Example.COM/x", map[string]any{"url": "http://example.com/x"})
}

func TestURLErrorNamesTheOffendingScheme(t *testing.T) {
	t.Parallel()

	_, err := urlBuilder{}.Build(map[string]any{"url": "javascript:alert(1)"})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Build = %v, want ErrInvalidPayload", err)
	}
	if want := "javascript"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the scheme %q", err, want)
	}
	if !strings.Contains(err.Error(), "allow_any") {
		t.Errorf("error %q does not say how to override the check", err)
	}
}
