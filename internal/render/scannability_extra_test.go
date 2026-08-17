package render

import (
	"testing"
)

// findIssue returns the finding with the given code, or false.
func findIssue(r Report, code string) (Issue, bool) {
	for _, i := range r.Issues {
		if i.Code == code {
			return i, true
		}
	}
	return Issue{}, false
}

func TestScannabilityGradientChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     string
		wantCode string
		wantSev  Severity
	}{
		{
			name: "a healthy dark ramp is clean",
			spec: "linear(#000000,#222266)",
		},
		{
			name: "a ramp that fades to near-white warns",
			// The classic unscannable design: elegant on a monitor, half the
			// symbol invisible to a phone camera.
			spec: "linear(#000000,#f4f4f4)", wantCode: "GRADIENT_FADES_OUT",
			wantSev: SeverityWarn,
		},
		{
			name: "a ramp with no dark stop at all is an error",
			spec: "linear(#e8e8e8,#f8f8f8)", wantCode: "GRADIENT_LOW_CONTRAST",
			wantSev: SeverityError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := DefaultStyle()
			s.QuietZone = 4
			g, err := ParseGradient(tt.spec)
			if err != nil {
				t.Fatalf("ParseGradient(%q): %v", tt.spec, err)
			}
			s.Gradient = g

			rep := Scannability(renderQR(t, 25, s))

			if tt.wantCode == "" {
				for _, i := range rep.Issues {
					if i.Code == "GRADIENT_FADES_OUT" || i.Code == "GRADIENT_LOW_CONTRAST" {
						t.Errorf("unexpected gradient finding %+v", i)
					}
				}
				return
			}

			got, ok := findIssue(rep, tt.wantCode)
			if !ok {
				t.Fatalf("no %s finding; got %+v", tt.wantCode, rep.Issues)
			}
			if got.Severity != tt.wantSev {
				t.Errorf("severity = %q, want %q", got.Severity, tt.wantSev)
			}
			if got.Hint == "" {
				t.Error("finding carries no hint")
			}
		})
	}
}

// A gradient whose darkest stop already fails means the whole symbol is
// unreadable, so the fade warning would be noise on top of it.
func TestScannabilityGradientReportsOnlyTheWorstFinding(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	g, err := ParseGradient("linear(#eeeeee,#ffffff)")
	if err != nil {
		t.Fatalf("ParseGradient: %v", err)
	}
	s.Gradient = g

	rep := Scannability(renderQR(t, 25, s))
	if _, ok := findIssue(rep, "GRADIENT_FADES_OUT"); ok {
		t.Error("both gradient findings fired; the error alone is enough")
	}
	if rep.OK() {
		t.Error("an invisible gradient graded as scannable")
	}
}

func TestScannabilityNoGradientFindingWithoutAGradient(t *testing.T) {
	t.Parallel()

	rep := Scannability(renderQR(t, 25, DefaultStyle()))
	for _, i := range rep.Issues {
		if i.Code == "GRADIENT_FADES_OUT" || i.Code == "GRADIENT_LOW_CONTRAST" {
			t.Errorf("gradient finding %+v on a style with no gradient", i)
		}
	}
}

func TestScannabilityLogoThresholds(t *testing.T) {
	t.Parallel()

	// Coverage is (scale*size + 2*padding)^2 / size^2 on a square symbol, so
	// the module counts below are chosen to land either side of 8% and 25%.
	tests := []struct {
		name     string
		size     int
		logo     *Logo
		ecc      string
		wantCode string
		wantSev  Severity
	}{
		{
			name: "a small logo at level M is fine",
			// 5 of 41 modules is 12% of the width and 1.5% of the area.
			size: 41, logo: &Logo{Scale: 0.12}, ecc: "M",
		},
		{
			name: "a mid-sized logo below level H warns",
			size: 41, logo: &Logo{Scale: 0.3}, ecc: "M",
			wantCode: "LOGO_EXCEEDS_ECC", wantSev: SeverityWarn,
		},
		{
			name: "an unknown level is treated as below H",
			size: 41, logo: &Logo{Scale: 0.3},
			wantCode: "LOGO_EXCEEDS_ECC", wantSev: SeverityWarn,
		},
		{
			name: "the same logo at level H is only worth noting",
			size: 41, logo: &Logo{Scale: 0.3}, ecc: "h",
			wantCode: "LOGO_LARGE", wantSev: SeverityInfo,
		},
		{
			name: "padding counts towards the coverage",
			// The scale cap alone cannot reach a quarter of the area — 0.35 of
			// the width is 12% of it — so the error threshold is reachable
			// only through clear space, which destroys just as much data.
			// 0.12 of 41 rounds to 5 modules; eight modules of padding on
			// every side makes it 21 by 21, a quarter of a 41-module symbol.
			size: 41, logo: &Logo{Scale: 0.12, Padding: MaxLogoPadding}, ecc: "H",
			wantCode: "LOGO_TOO_LARGE", wantSev: SeverityError,
		},
		{
			name: "the widest logo allowed is still only a note at level H",
			size: 41, logo: &Logo{Scale: MaxLogoScale}, ecc: "H",
			wantCode: "LOGO_LARGE", wantSev: SeverityInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := DefaultStyle()
			s.QuietZone = 4
			s.Logo = tt.logo
			s.ECC = tt.ecc

			rep := Scannability(renderQR(t, tt.size, s))

			if tt.wantCode == "" {
				for _, i := range rep.Issues {
					if len(i.Code) >= 4 && i.Code[:4] == "LOGO" {
						t.Errorf("unexpected logo finding %+v", i)
					}
				}
				return
			}

			got, ok := findIssue(rep, tt.wantCode)
			if !ok {
				t.Fatalf("no %s finding; got %+v", tt.wantCode, rep.Issues)
			}
			if got.Severity != tt.wantSev {
				t.Errorf("severity = %q, want %q", got.Severity, tt.wantSev)
			}
		})
	}
}

func TestScannabilityLogoErrorMakesTheCodeUnscannable(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{Scale: 0.12, Padding: MaxLogoPadding, Excavate: true}
	s.ECC = "H"

	rep := Scannability(renderQR(t, 41, s))
	if rep.OK() {
		t.Error("a logo covering more than a quarter of the symbol graded as scannable")
	}
	if rep.Grade != GradeUnscannable {
		t.Errorf("grade = %q, want %q", rep.Grade, GradeUnscannable)
	}
}

// An info-level finding costs no score, so a well-judged logo on a level H
// code still grades excellent.
func TestScannabilityLogoInfoCostsNoScore(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Logo = &Logo{Scale: 0.3}
	s.ECC = "H"

	rep := Scannability(renderQR(t, 41, s))
	if rep.Score != 100 {
		t.Errorf("score = %d, want 100 for an informational finding", rep.Score)
	}
	if rep.Grade != GradeExcellent {
		t.Errorf("grade = %q, want %q", rep.Grade, GradeExcellent)
	}
}
