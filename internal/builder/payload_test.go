package builder

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestToMapAcceptsEveryPayloadShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payload any
		want    string
	}{
		{"a decoded json body", map[string]any{"text": "hi"}, "hi"},
		{"a form value map", map[string]string{"text": "hi"}, "hi"},
		{"parsed query values", map[string][]string{"text": {"hi"}}, "hi"},
		{"a struct", TextPayload{Text: "hi"}, "hi"},
		{"a pointer to a struct", &TextPayload{Text: "hi"}, "hi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := textBuilder{}.Build(tc.payload)
			if err != nil {
				t.Fatalf("Build = error %v", err)
			}
			if got != tc.want {
				t.Errorf("Build = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToMapRejectsWhatIsNotAnObject(t *testing.T) {
	t.Parallel()

	b := textBuilder{}
	for _, payload := range []any{nil, "a string", 42, []string{"a"}, (*TextPayload)(nil)} {
		if _, err := b.Build(payload); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("Build(%#v) = %v, want ErrInvalidPayload", payload, err)
		}
	}
}

func TestQueryValuesTakeTheFirstNonEmpty(t *testing.T) {
	t.Parallel()

	// A repeated parameter is a caller mistake rather than a list; the first
	// value that says something wins.
	got, err := textBuilder{}.Build(map[string][]string{"text": {"", "second", "third"}})
	if err != nil {
		t.Fatalf("Build = error %v", err)
	}
	if got != "second" {
		t.Errorf("Build = %q, want %q", got, "second")
	}
}

func TestUnknownFieldSuggestsTheClosestName(t *testing.T) {
	t.Parallel()

	fields := []Field{{Name: "subject"}, {Name: "body"}, {Name: "email"}}
	for _, tc := range []struct {
		key      string
		want     string
		wantAny  bool
		contains string
	}{
		{key: "sbuject", want: "subject", wantAny: true},
		{key: "bdoy", want: "body", wantAny: true},
		{key: "x", wantAny: false, contains: "expected one of"},
		{key: "completely-different", wantAny: false, contains: "expected one of"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			err := checkFields(map[string]any{tc.key: "v"}, fields)
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("checkFields = %v, want ErrInvalidPayload", err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q does not name the offending key", err)
			}
			if tc.wantAny && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not suggest %q", err, tc.want)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %q does not contain %q", err, tc.contains)
			}
		})
	}
}

func TestUnknownFieldErrorIsDeterministic(t *testing.T) {
	t.Parallel()

	// Two unknown keys must always produce the same message, whatever order
	// the map happens to range in.
	m := map[string]any{"aaa": 1, "zzz": 2, "text": "x"}
	first := checkFields(m, textBuilder{}.Fields()).Error()
	for range 20 {
		if got := checkFields(m, textBuilder{}.Fields()).Error(); got != first {
			t.Fatalf("message varies between calls: %q then %q", first, got)
		}
	}
	if !strings.Contains(first, "aaa") {
		t.Errorf("message %q does not report the first key in sorted order", first)
	}
}

func TestLevenshtein(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"subject", "sbuject", 2},
		{"é☃", "é☃", 0},
	} {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestBooleanCoercion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value   any
		want    bool
		wantErr bool
	}{
		{value: true, want: true},
		{value: "TRUE", want: true},
		{value: "yes", want: true},
		{value: "on", want: true},
		{value: 1, want: true},
		{value: "false", want: false},
		{value: "", want: false},
		{value: nil, want: false},
		{value: 1.0, want: true},
		{value: "ture", wantErr: true},
		{value: 7, wantErr: true},
		{value: []string{"true"}, wantErr: true},
	} {
		got, err := boolean(map[string]any{"k": tc.value}, "k")
		switch {
		case tc.wantErr && !errors.Is(err, ErrInvalidPayload):
			t.Errorf("boolean(%#v) = %t, %v; want ErrInvalidPayload", tc.value, got, err)
		case !tc.wantErr && err != nil:
			t.Errorf("boolean(%#v) = error %v", tc.value, err)
		case !tc.wantErr && got != tc.want:
			t.Errorf("boolean(%#v) = %t, want %t", tc.value, got, tc.want)
		}
	}
}

func TestNumberCoercion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value       any
		want        float64
		wantPresent bool
		wantErr     bool
	}{
		{value: 1.5, want: 1.5, wantPresent: true},
		{value: 2, want: 2, wantPresent: true},
		{value: uint8(3), want: 3, wantPresent: true},
		{value: "4.25", want: 4.25, wantPresent: true},
		{value: json.Number("5"), want: 5, wantPresent: true},
		{value: "", wantPresent: false},
		{value: nil, wantPresent: false},
		{value: "not a number", wantErr: true},
		{value: true, wantErr: true},
	} {
		got, present, err := number(map[string]any{"k": tc.value}, "k")
		switch {
		case tc.wantErr && !errors.Is(err, ErrInvalidPayload):
			t.Errorf("number(%#v) = %v, %t, %v; want ErrInvalidPayload", tc.value, got, present, err)
		case !tc.wantErr && err != nil:
			t.Errorf("number(%#v) = error %v", tc.value, err)
		case !tc.wantErr && (got != tc.want || present != tc.wantPresent):
			t.Errorf("number(%#v) = %v, %t; want %v, %t", tc.value, got, present, tc.want, tc.wantPresent)
		}
	}
}

func TestNumberRejectsWhatCannotBeWrittenDown(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"NaN", "Inf", "-Inf"} {
		if _, _, err := number(map[string]any{"k": value}, "k"); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("number(%q) = %v, want ErrInvalidPayload", value, err)
		}
	}
}

func TestStringCoercion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value   any
		want    string
		wantErr bool
	}{
		{value: "text", want: "text"},
		{value: 42, want: "42"},
		{value: uint16(7), want: "7"},
		{value: 1.5, want: "1.5"},
		{value: json.Number("8"), want: "8"},
		{value: nil, want: ""},
		{value: true, wantErr: true},
		{value: map[string]any{}, wantErr: true},
	} {
		got, err := str(map[string]any{"k": tc.value}, "k")
		switch {
		case tc.wantErr && !errors.Is(err, ErrInvalidPayload):
			t.Errorf("str(%#v) = %q, %v; want ErrInvalidPayload", tc.value, got, err)
		case !tc.wantErr && err != nil:
			t.Errorf("str(%#v) = error %v", tc.value, err)
		case !tc.wantErr && got != tc.want:
			t.Errorf("str(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestPctEscapeLeavesOnlyTheUnreservedSet(t *testing.T) {
	t.Parallel()

	// The unreserved set of RFC 3986 is the only thing that may survive; a
	// space must become %20 rather than the "+" of a form body.
	if got := pctEscape("a-b_c.d~e"); got != "a-b_c.d~e" {
		t.Errorf("pctEscape mangled the unreserved set: %q", got)
	}
	if got := pctEscape("a b+c&d"); got != "a%20b%2Bc%26d" {
		t.Errorf("pctEscape(%q) = %q", "a b+c&d", got)
	}
	if got := pctEscape("é"); got != "%C3%A9" {
		t.Errorf("pctEscape of a non-ASCII rune = %q, want the UTF-8 bytes", got)
	}
}
