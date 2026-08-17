package preset_test

import (
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/preset"
	"github.com/el-amin-dev/barqr/internal/render"
)

// styleFromTheme builds a render.Style from a theme's option map, the way the
// request layer does.
func styleFromTheme(t *testing.T, p preset.Preset) render.Style {
	t.Helper()

	st := render.DefaultStyle()

	str := func(key string) string {
		v, ok := p.Options[key]
		if !ok {
			t.Fatalf("%s: theme does not set %s", p.Name, key)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: %s is %T, want a string", p.Name, key, v)
		}
		return s
	}
	colour := func(key string) render.Style {
		return st // placeholder, replaced below
	}
	_ = colour

	st.Module, st.Eye, st.EyeBall = str("style.module"), str("style.eye"), str("style.eye_ball")

	fg, err := render.ParseColor(str("style.fg"))
	if err != nil {
		t.Fatalf("%s: style.fg: %v", p.Name, err)
	}
	bg, err := render.ParseColor(str("style.bg"))
	if err != nil {
		t.Fatalf("%s: style.bg: %v", p.Name, err)
	}
	eye, err := render.ParseColor(str("style.eye_fg"))
	if err != nil {
		t.Fatalf("%s: style.eye_fg: %v", p.Name, err)
	}
	st.FG, st.BG, st.EyeFG = fg, bg, &eye
	return st
}

// themes returns the built-in themes.
func builtinThemes(t *testing.T) []preset.Preset {
	t.Helper()

	var out []preset.Preset
	for _, p := range preset.Builtin().All() {
		if p.Kind == preset.KindTheme {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatal("no themes are registered")
	}
	return out
}

// TestEveryThemeScans is the check that makes these themes safe to ship as
// ready-made choices.
//
// A theme exists so a caller does not have to reason about contrast, and that
// promise is worthless unless it is enforced: every one is rendered through the
// real encoder and renderer and put through the real scannability report, and
// none may come back unscannable. A colour that looks good and fails here is
// the wrong colour, and this test is where that gets decided rather than in
// somebody's print run.
func TestEveryThemeScans(t *testing.T) {
	t.Parallel()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatal(err)
	}
	m, err := enc.Encode("https://barqr.dev/themes", encoder.AutoEncodeOpts())
	if err != nil {
		t.Fatal(err)
	}
	r, err := render.Get(render.StandardRenderer)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range builtinThemes(t) {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			c, err := r.Render(m, styleFromTheme(t, p))
			if err != nil {
				t.Fatalf("rendering: %v", err)
			}

			rep := render.Scannability(c)
			if !rep.OK() {
				for _, i := range rep.Issues {
					if i.Severity == render.SeverityError {
						t.Errorf("[%s] %s: %s", i.Severity, i.Code, i.Message)
					}
				}
				t.Fatalf("theme %q is %s (score %d, contrast %.2f:1)",
					p.Name, rep.Grade, rep.Score, rep.ContrastRatio)
			}

			// Inverted is expected on a dark theme and is only a warning, but
			// the contrast itself must be comfortable either way: 4.5:1 is the
			// point below which a phone camera in poor light starts to struggle.
			// The report only treats 3:1 as fatal; a shipped theme is held to
			// the higher line, because a caller choosing one by name is trusting
			// it to have been thought about.
			const comfortable = 4.5

			if rep.ContrastRatio < comfortable {
				t.Errorf("theme %q has module contrast %.2f:1, want at least %.1f:1",
					p.Name, rep.ContrastRatio, comfortable)
			}

			st := styleFromTheme(t, p)
			if eye := render.ContrastRatio(*st.EyeFG, st.BG); eye < comfortable {
				t.Errorf("theme %q has finder contrast %.2f:1, want at least %.1f:1 — "+
					"the finders are what a scanner locates first",
					p.Name, eye, comfortable)
			}
		})
	}
}

// TestThemesTouchOnlyAppearance is what lets a theme compose with any layout.
// A theme that set output.format or encode.ecc would silently override the
// caller's choice of where the code is going.
func TestThemesTouchOnlyAppearance(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"style.module": true, "style.eye": true, "style.eye_ball": true,
		"style.fg": true, "style.bg": true, "style.eye_fg": true,
	}

	for _, p := range builtinThemes(t) {
		for key := range p.Options {
			if !allowed[key] {
				t.Errorf("theme %q sets %q; a theme must set appearance only, "+
					"so that it composes with any layout preset", p.Name, key)
			}
		}
		if len(p.Options) != len(allowed) {
			t.Errorf("theme %q sets %d options, want all %d appearance keys",
				p.Name, len(p.Options), len(allowed))
		}
		if p.Description == "" {
			t.Errorf("theme %q has no description; the listing shows it to a caller", p.Name)
		}
	}
}

// TestThemeCountIsStable pins the advertised number so the documentation and
// the registry cannot disagree.
func TestThemeCountIsStable(t *testing.T) {
	t.Parallel()

	if got, want := len(builtinThemes(t)), 30; got != want {
		t.Errorf("there are %d themes, want %d", got, want)
	}
}
