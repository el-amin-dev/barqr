package preset

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidNameAcceptsSlugsAndRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"lowercase word", "print", true},
		{"digits", "300dpi", true},
		{"hyphen and underscore", "a-b_c", true},
		{"single character", "x", true},
		{"exactly 64 characters", strings.Repeat("a", 64), true},
		{"65 characters", strings.Repeat("a", 65), false},
		{"empty", "", false},
		{"uppercase", "Print", false},
		{"leading hyphen", "-print", false},
		{"leading underscore", "_print", false},
		{"dot", "print.v2", false},
		{"slash", "a/b", false},
		{"parent traversal", "..", false},
		{"space", "a b", false},
		{"nul byte", "a\x00b", false},
		{"newline", "a\nb", false},
		{"non-ascii", "impressão", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidName(tc.in); got != tc.want {
				t.Fatalf("ValidName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuiltinShipsTheDocumentedPresets(t *testing.T) {
	t.Parallel()

	set := Builtin()

	// The layouts are pinned by name: each exists because a real caller would
	// otherwise repeat the same options, and removing one is a breaking change
	// to anybody naming it. The themes are checked by kind and count instead —
	// they are an open set that is expected to grow, and themes_test.go holds
	// them to the rules that actually matter.
	wantLayouts := []string{
		"dark", "default", "label", "print", "sticker", "terminal", "ticket", "web",
	}

	var layouts, themeNames []string
	for _, p := range set.All() {
		switch p.Kind {
		case KindTheme:
			themeNames = append(themeNames, p.Name)
		default:
			layouts = append(layouts, p.Name)
		}
	}
	slices.Sort(layouts)

	if !slices.Equal(layouts, wantLayouts) {
		t.Fatalf("layout presets = %v, want %v", layouts, wantLayouts)
	}
	if len(themeNames) == 0 {
		t.Fatal("no themes are registered")
	}
	if got, want := set.Len(), len(layouts)+len(themeNames); got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	// A theme and a layout sharing a name would make one of them unreachable.
	seen := make(map[string]bool, set.Len())
	for _, n := range set.Names() {
		if seen[n] {
			t.Errorf("preset %q is registered twice", n)
		}
		seen[n] = true
	}
}

func TestBuiltinPresetsSatisfyTheSameRulesAsFiles(t *testing.T) {
	t.Parallel()

	for _, p := range Builtin().All() {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()
			if msg := validate(p); msg != "" {
				t.Fatalf("built-in %q is invalid: %s", p.Name, msg)
			}
			if p.Description == "" {
				t.Fatalf("built-in %q has no description", p.Name)
			}
		})
	}
}

func TestBuiltinPresetsCarryTheirDefiningOption(t *testing.T) {
	t.Parallel()

	// Each built-in exists for one headline reason; if that option ever
	// disappears the preset has silently stopped meaning what it is named for.
	cases := []struct {
		preset string
		key    string
		want   any
	}{
		{"print", "output.dpi", 300},
		{"print", "encode.ecc", "H"},
		{"print", "encode.quiet_zone", 6},
		{"print", "output.unit", "mm"},
		{"terminal", "output.format", "ansi"},
		{"web", "output.format", "svg"},
		{"ticket", "encode.ecc", "H"},
		{"label", "style.hri", true},
		{"dark", "style.bg", "#121212"},
		{"sticker", "style.module", "rounded"},
		{"sticker", "style.eye_ball", "dot"},
	}

	set := Builtin()
	for _, tc := range cases {
		t.Run(tc.preset+"/"+tc.key, func(t *testing.T) {
			t.Parallel()
			p, ok := set.Get(tc.preset)
			if !ok {
				t.Fatalf("built-in %q is missing", tc.preset)
			}
			if got := p.Options[tc.key]; got != tc.want {
				t.Fatalf("%s[%s] = %v, want %v", tc.preset, tc.key, got, tc.want)
			}
		})
	}
}

func TestGetReturnsACopyThatCannotCorruptTheSet(t *testing.T) {
	t.Parallel()

	set := Builtin()
	first, ok := set.Get("print")
	if !ok {
		t.Fatal("print is missing")
	}
	first.Options["output.dpi"] = 1
	delete(first.Options, "encode.ecc")

	second, _ := set.Get("print")
	if second.Options["output.dpi"] != 300 {
		t.Fatalf("mutating a returned preset changed the set: dpi = %v",
			second.Options["output.dpi"])
	}
	if second.Options["encode.ecc"] != "H" {
		t.Fatal("mutating a returned preset deleted a key from the set")
	}
}

func TestGetNormalisesTheRequestedName(t *testing.T) {
	t.Parallel()

	set := Builtin()
	for _, name := range []string{"print", "PRINT", "  Print  ", "pRiNt"} {
		if _, ok := set.Get(name); !ok {
			t.Fatalf("Get(%q) did not find the print preset", name)
		}
	}
	if _, ok := set.Get("no-such-preset"); ok {
		t.Fatal("Get returned an unknown preset")
	}
}

func TestNilSetIsSafeToQuery(t *testing.T) {
	t.Parallel()

	var set *Set
	if _, ok := set.Get("print"); ok {
		t.Fatal("nil set answered Get")
	}
	if set.Names() != nil || set.All() != nil || set.Len() != 0 {
		t.Fatal("nil set returned contents")
	}
}

func TestCloneGivesAWritableMapForAPresetWithNoOptions(t *testing.T) {
	t.Parallel()

	// The request layer merges into what Clone hands back, and a nil map
	// panics on write.
	c := Preset{Name: "x"}.Clone()
	c.Options["style.fg"] = "#000"
	if len(c.Options) != 1 {
		t.Fatalf("clone options = %v", c.Options)
	}
}

func TestValidateRejectsMalformedPresets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    Preset
		wants string
	}{
		{
			"bad name",
			Preset{Name: "Bad", Options: map[string]any{"a": 1}},
			"valid slug",
		},
		{
			"no options",
			Preset{Name: "x", Options: map[string]any{}},
			"options is empty",
		},
		{
			"too many options",
			Preset{Name: "x", Options: manyOptions(MaxOptions + 1)},
			"too many options",
		},
		{
			"empty key",
			Preset{Name: "x", Options: map[string]any{"": 1}},
			"is empty",
		},
		{
			"over-long key",
			Preset{Name: "x", Options: map[string]any{strings.Repeat("k", maxKeyBytes+1): 1}},
			"too long",
		},
		{
			"padded key",
			Preset{Name: "x", Options: map[string]any{" style.fg": 1}},
			"space",
		},
		{
			"leading dot",
			Preset{Name: "x", Options: map[string]any{".style.fg": 1}},
			"dot",
		},
		{
			"trailing dot",
			Preset{Name: "x", Options: map[string]any{"style.": 1}},
			"dot",
		},
		{
			"empty segment",
			Preset{Name: "x", Options: map[string]any{"style..fg": 1}},
			"empty path segment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg := validate(tc.in)
			if !strings.Contains(msg, tc.wants) {
				t.Fatalf("validate() = %q, want it to mention %q", msg, tc.wants)
			}
		})
	}
}

func manyOptions(n int) map[string]any {
	out := make(map[string]any, n)
	for i := range n {
		out[string(rune('a'+i%26))+strings.Repeat("x", i/26+1)] = i
	}
	return out
}

func TestLoadWithNoDirectoryReturnsBuiltinsOnly(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", "   "} {
		set, warnings, err := Load(dir)
		if err != nil {
			t.Fatalf("Load(%q) error: %v", dir, err)
		}
		if len(warnings) != 0 {
			t.Fatalf("Load(%q) warnings: %v", dir, warnings)
		}
		if !slices.Equal(set.Names(), Builtin().Names()) {
			t.Fatalf("Load(%q) did not return the built-ins", dir)
		}
	}
}

func TestLoadRejectsAPathThatIsNotADirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "presets")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Load(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Load(file) error = %v, want ErrNotADirectory", err)
	}
}

func TestLoadRejectsAMissingDirectory(t *testing.T) {
	t.Parallel()

	_, _, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("Load(missing) error = %v, want ErrUnreadable", err)
	}
	// The error must not carry the path, which may name a mount or a customer.
	if strings.Contains(err.Error(), "absent") {
		t.Fatalf("Load leaked the path in %q", err)
	}
}

func TestLoadAddsAndOverridesPresets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "shop.json", `{"description":"ours","options":{"output.dpi":600}}`)
	write(t, dir, "print.json", `{"name":"print","options":{"output.dpi":1200}}`)

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	shop, ok := set.Get("shop")
	if !ok {
		t.Fatal("shop preset was not loaded")
	}
	if shop.Description != "ours" || shop.Options["output.dpi"] != 600.0 {
		t.Fatalf("shop = %+v", shop)
	}

	// A user preset replaces the built-in wholesale; nothing is merged under it.
	printing, ok := set.Get("print")
	if !ok {
		t.Fatal("print preset disappeared")
	}
	if printing.Options["output.dpi"] != 1200.0 {
		t.Fatalf("print.output.dpi = %v, want the file's 1200", printing.Options["output.dpi"])
	}
	if _, stillThere := printing.Options["encode.ecc"]; stillThere {
		t.Fatal("override merged the built-in's options underneath the file's")
	}
	if set.Len() != Builtin().Len()+1 {
		t.Fatalf("Len() = %d, want %d", set.Len(), Builtin().Len()+1)
	}
}

func TestLoadWarnsAndSkipsBadFiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		file     string
		body     string
		wantWarn string
	}{
		{"truncated json", "broken.json", `{"options":`, "truncated"},
		{"malformed json", "syntax.json", `{"options":,}`, "malformed JSON"},
		{"not an object", "scalar.json", `42`, "expects"},
		{"unknown field", "typo.json", `{"option":{"a":1}}`, "unknown field"},
		{"two values", "double.json", `{"options":{"a":1}}{"options":{"b":2}}`, "more than one"},
		{"empty options", "hollow.json", `{"options":{}}`, "options is empty"},
		{"name disagrees with stem", "alias.json", `{"name":"other","options":{"a":1}}`,
			"file stem"},
		{"uppercase stem", "Shop.json", `{"options":{"a":1}}`, "valid preset slug"},
		{"dotted stem", "shop.v2.json", `{"options":{"a":1}}`, "valid preset slug"},
		{"leading dash stem", "-shop.json", `{"options":{"a":1}}`, "valid preset slug"},
		{"bad option key", "keys.json", `{"options":{"style..fg":1}}`, "empty path segment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			write(t, dir, tc.file, tc.body)

			set, warnings, err := Load(dir)
			if err != nil {
				t.Fatalf("a bad file must not fail the load, got %v", err)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tc.wantWarn) {
				t.Fatalf("warnings = %v, want one mentioning %q", warnings, tc.wantWarn)
			}
			if !strings.Contains(warnings[0], tc.file) {
				t.Fatalf("warning %q does not name the file", warnings[0])
			}
			// The built-ins must still be there: the service boots.
			if set.Len() != Builtin().Len() {
				t.Fatalf("Len() = %d, want the %d built-ins", set.Len(), Builtin().Len())
			}
		})
	}
}

func TestLoadIgnoresFilesThatAreNotPresets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, "README.md", "notes")
	write(t, dir, "shop.json.tmp", "half written")
	write(t, dir, ".shop.json.swp", "editor")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if set.Len() != Builtin().Len() {
		t.Fatalf("Len() = %d, want the built-ins only", set.Len())
	}
}

func TestLoadWarnsOnADirectoryNamedLikeAPreset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "trap.json"), 0o750); err != nil {
		t.Fatal(err)
	}

	_, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not a regular file") {
		t.Fatalf("warnings = %v, want one about a non-regular file", warnings)
	}
}

func TestLoadRefusesSymlinksOutOfTheDirectory(t *testing.T) {
	t.Parallel()

	// The threat: an operator mounts a presets directory into a container and
	// something drops a symlink in it aimed at a secret. A preset file is read
	// verbatim and its contents are served on /v1/presets, so following that
	// link is an exfiltration primitive.
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"options":{"leaked":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(dir, "stolen.json")); err != nil {
		t.Skipf("symlinks unavailable on this filesystem: %v", err)
	}
	// A link to the parent directory itself must not become a way to walk out.
	if err := os.Symlink(outside, filepath.Join(dir, "up.json")); err != nil {
		t.Fatal(err)
	}

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per symlink", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "not a regular file") {
			t.Fatalf("warning %q does not explain the refusal", w)
		}
	}
	if _, ok := set.Get("stolen"); ok {
		t.Fatal("a symlinked file was loaded as a preset")
	}
	if set.Len() != Builtin().Len() {
		t.Fatalf("Len() = %d, want the built-ins only", set.Len())
	}
}

func TestLoadRejectsAnOversizedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	big := `{"options":{"style.caption":"` + strings.Repeat("x", MaxFileBytes) + `"}}`
	write(t, dir, "huge.json", big)

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "limit for a preset file") {
		t.Fatalf("warnings = %v, want one about the size limit", warnings)
	}
	if _, ok := set.Get("huge"); ok {
		t.Fatal("an oversized preset was loaded")
	}
}

func TestLoadStopsAtThePresetLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// One more than the directory can contribute, so the cap is crossed rather
	// than merely reached.
	for i := range MaxPresets - Builtin().Len() + 1 {
		write(t, dir, "p"+pad(i)+".json", `{"options":{"output.scale":1}}`)
	}

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != MaxPresets {
		t.Fatalf("Len() = %d, want the cap of %d", set.Len(), MaxPresets)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "limit is already reached") {
		t.Fatalf("warnings = %v, want exactly one about the preset limit", warnings)
	}
}

func TestLoadDoesNotSpendTheLimitOnOverrides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range Builtin().Names() {
		write(t, dir, name+".json", `{"options":{"output.scale":3}}`)
	}

	set, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if set.Len() != Builtin().Len() {
		t.Fatalf("Len() = %d, want %d", set.Len(), Builtin().Len())
	}
	p, _ := set.Get("web")
	if p.Options["output.scale"] != 3.0 {
		t.Fatalf("web was not overridden: %+v", p.Options)
	}
}

func TestWithinRejectsEscapingNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "a.json", true},
		{"nested", "sub/a.json", true},
		{"parent", "../a.json", false},
		{"parent alone", "..", false},
		{"deep parent", "sub/../../a.json", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := within("/srv/presets", tc.in); got != tc.want {
				t.Fatalf("within(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// write creates a preset file and fails the test if it cannot.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pad renders an index as a fixed-width, slug-legal suffix.
func pad(i int) string {
	s := "000" + string(rune('0'+i/100%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10))
	return s[len(s)-3:]
}
