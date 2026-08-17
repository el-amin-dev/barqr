package render_test

import (
	"image/color"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

// hasIssue reports whether the report contains a finding with that code.
func hasIssue(rep render.Report, code string) bool {
	for _, i := range rep.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// severityOf returns the severity of a finding, or empty if absent.
func severityOf(rep render.Report, code string) render.Severity {
	for _, i := range rep.Issues {
		if i.Code == code {
			return i.Severity
		}
	}
	return ""
}

// canvasWith renders a QR symbol under a mutated style.
func canvasWith(t *testing.T, mutate func(render.Style) render.Style) render.Canvas {
	t.Helper()

	m := qrMatrix(t, "https://example.com/scannability")
	c, err := standard(t).Render(m, mutate(render.DefaultStyle()))
	if err != nil {
		t.Fatalf("Render(): %v", err)
	}
	return c
}

// TestScannabilityPassesTheDefault is the baseline: the plain black-on-white
// code barqr produces with no styling must be flawless, or every other
// threshold in this file is measured against the wrong zero.
func TestScannabilityPassesTheDefault(t *testing.T) {
	t.Parallel()

	rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style { return s }))

	if got, want := rep.Grade, render.GradeExcellent; got != want {
		t.Errorf("Grade = %q, want %q (issues: %+v)", got, want, rep.Issues)
	}
	if got, want := rep.Score, 100; got != want {
		t.Errorf("Score = %d, want %d", got, want)
	}
	if len(rep.Issues) != 0 {
		t.Errorf("Issues = %+v, want none for a default code", rep.Issues)
	}
	if !rep.OK() {
		t.Error("OK() = false for the default style")
	}
	if rep.Inverted {
		t.Error("Inverted = true for dark-on-light")
	}
	if rep.ContrastRatio < 20 {
		t.Errorf("ContrastRatio = %.1f, want ~21 for black on white", rep.ContrastRatio)
	}
}

func TestScannabilityContrast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fg, bg   color.NRGBA
		wantCode string
		wantSev  render.Severity
	}{
		{
			name:     "near-identical colours are unreadable",
			fg:       color.NRGBA{R: 0xEE, G: 0xEE, B: 0xEE, A: 0xFF},
			bg:       color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			wantCode: "LOW_CONTRAST",
			wantSev:  render.SeverityError,
		},
		{
			name:     "mid-grey on white is marginal",
			fg:       color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xFF},
			bg:       color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
			wantCode: "MARGINAL_CONTRAST",
			wantSev:  render.SeverityWarn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
				s.FG, s.BG = tt.fg, tt.bg
				return s
			}))

			if got := severityOf(rep, tt.wantCode); got != tt.wantSev {
				t.Fatalf("%s severity = %q, want %q (issues: %+v)",
					tt.wantCode, got, tt.wantSev, rep.Issues)
			}
			if tt.wantSev == render.SeverityError && rep.OK() {
				t.Error("OK() = true despite an error-severity finding")
			}
		})
	}
}

func TestScannabilityInverted(t *testing.T) {
	t.Parallel()

	rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
		s.FG = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
		s.BG = color.NRGBA{A: 0xFF}
		return s
	}))

	if !rep.Inverted {
		t.Error("Inverted = false for light modules on a dark background")
	}
	if !hasIssue(rep, "INVERTED") {
		t.Errorf("no INVERTED finding: %+v", rep.Issues)
	}
	// Inverted is a warning, not a failure: plenty of scanners cope.
	if !rep.OK() {
		t.Error("an inverted code should still be renderable")
	}
}

func TestScannabilityTransparentBackground(t *testing.T) {
	t.Parallel()

	rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
		s.BG = render.Transparent
		return s
	}))

	if !hasIssue(rep, "TRANSPARENT_BACKGROUND") {
		t.Errorf("no TRANSPARENT_BACKGROUND finding: %+v", rep.Issues)
	}
}

func TestScannabilityQuietZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		quiet   int
		wantSev render.Severity
	}{
		{"no margin at all is fatal", 0, render.SeverityError},
		{"a short margin is a warning", 2, render.SeverityWarn},
		{"the specified margin is fine", 4, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
				s.QuietZone = tt.quiet
				return s
			}))

			if got := severityOf(rep, "QUIET_ZONE_TOO_SMALL"); got != tt.wantSev {
				t.Fatalf("QUIET_ZONE_TOO_SMALL severity = %q, want %q (issues: %+v)",
					got, tt.wantSev, rep.Issues)
			}
		})
	}
}

func TestScannabilityEyeContrast(t *testing.T) {
	t.Parallel()

	faint := color.NRGBA{R: 0xF8, G: 0xF8, B: 0xF8, A: 0xFF}
	rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
		s.EyeFG = &faint
		return s
	}))

	// The finder patterns are what a scanner locates first, so washing them
	// out is fatal even when the data modules are perfectly readable.
	if got, want := severityOf(rep, "LOW_EYE_CONTRAST"), render.SeverityError; got != want {
		t.Fatalf("LOW_EYE_CONTRAST severity = %q, want %q (issues: %+v)", got, want, rep.Issues)
	}
	if rep.OK() {
		t.Error("OK() = true despite unreadable finder patterns")
	}
}

func TestScannabilityDegenerateMatrix(t *testing.T) {
	t.Parallel()

	// An all-light canvas is what a broken encoder produces; the report must
	// call it out rather than grading it as a perfect code.
	c := render.Canvas{Cols: 10, Rows: 10, Symbology: "qr", QuietZone: 4,
		Style: render.DefaultStyle()}
	rep := render.Scannability(c)

	if !hasIssue(rep, "DEGENERATE_MATRIX") {
		t.Errorf("no DEGENERATE_MATRIX finding: %+v", rep.Issues)
	}
	if rep.OK() {
		t.Error("OK() = true for an empty matrix")
	}
}

// TestScannabilityOrdersWorstFirst asserts the ordering a client relies on
// when it shows only the top finding.
func TestScannabilityOrdersWorstFirst(t *testing.T) {
	t.Parallel()

	rep := render.Scannability(canvasWith(t, func(s render.Style) render.Style {
		s.FG = color.NRGBA{R: 0xEE, G: 0xEE, B: 0xEE, A: 0xFF} // error
		s.BG = render.Transparent                              // warn
		return s
	}))

	if len(rep.Issues) < 2 {
		t.Fatalf("Issues = %+v, want at least two findings", rep.Issues)
	}
	if got, want := rep.Issues[0].Severity, render.SeverityError; got != want {
		t.Errorf("first issue severity = %q, want %q", got, want)
	}
	if got, want := rep.Grade, render.GradeUnscannable; got != want {
		t.Errorf("Grade = %q, want %q", got, want)
	}
	if rep.Score < 0 {
		t.Errorf("Score = %d, want it floored at zero", rep.Score)
	}
	// Every finding must carry a fix, or the report is a complaint rather
	// than a diagnosis.
	for _, i := range rep.Issues {
		if i.Message == "" {
			t.Errorf("issue %s has no message", i.Code)
		}
		if i.Hint == "" {
			t.Errorf("issue %s has no hint", i.Code)
		}
	}
}
