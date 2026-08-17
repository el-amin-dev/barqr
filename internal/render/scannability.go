package render

import (
	"fmt"
	"strings"
)

// Severity ranks a scannability finding.
type Severity string

// Finding severities.
const (
	// SeverityInfo is worth knowing but not worth changing.
	SeverityInfo Severity = "info"
	// SeverityWarn means the code will probably scan, but not on every
	// device, in every light, at every size.
	SeverityWarn Severity = "warn"
	// SeverityError means the code is unlikely to scan at all. Strict mode
	// rejects the request rather than rendering it.
	SeverityError Severity = "error"
)

// Grade is the overall verdict, derived from the worst finding.
type Grade string

// Overall grades.
const (
	GradeExcellent   Grade = "excellent"
	GradeGood        Grade = "good"
	GradeRisky       Grade = "risky"
	GradeUnscannable Grade = "unscannable"
)

// Issue is one scannability finding.
type Issue struct {
	// Code is a stable identifier, so a client can suppress a known one.
	Code string `json:"code"`
	// Severity ranks it.
	Severity Severity `json:"severity"`
	// Message states the problem in the terms a designer thinks in.
	Message string `json:"message"`
	// Hint is the fix.
	Hint string `json:"hint,omitempty"`
}

// Report is the scannability verdict for a canvas.
//
// The checks encode the failure modes that actually happen in the field:
// a designer picks brand colours that look fine on a monitor and fail on a
// phone camera in a shop, or strips the quiet zone to fit a layout. None of
// these produce an invalid code — they produce a valid code that no scanner
// can read, which is a much more expensive kind of wrong.
type Report struct {
	// Score is 0..100, worst finding dominating.
	Score int `json:"score"`
	// Grade is the human summary of Score.
	Grade Grade `json:"grade"`
	// ContrastRatio is the WCAG ratio between module and background colour.
	ContrastRatio float64 `json:"contrast_ratio"`
	// QuietZone is the margin actually applied, in modules.
	QuietZone int `json:"quiet_zone"`
	// Inverted reports light modules on a dark background.
	Inverted bool `json:"inverted"`
	// Issues lists every finding, worst first.
	Issues []Issue `json:"issues,omitempty"`
}

// OK reports whether the canvas passes strict mode.
func (r Report) OK() bool { return r.Grade != GradeUnscannable }

// minContrast values come from scanner behaviour rather than accessibility
// guidance: below 3:1 a camera in poor light cannot separate the modules at
// all, and between 3:1 and 4.5:1 it depends on the device.
const (
	minContrastHard = 3.0
	minContrastSoft = 4.5
)

// specQuietZone is the margin each symbology family requires. A code printed
// flush against other artwork is one of the most common real-world failures.
func specQuietZone(symbology string) int {
	switch symbology {
	case "qr":
		return 4
	case "datamatrix", "aztec":
		return 1
	case "pdf417":
		return 2
	case "":
		return 0
	default:
		// Linear symbologies want a wide margin; ten modules is the common
		// guidance across Code 128, ITF, and the EAN family.
		return 9
	}
}

// Scannability analyses a rendered canvas.
//
// It never mutates the canvas and never fails: an unreadable design is a
// report, not an error. Whether a report becomes a rejection is
// BARQR_STRICT_SCANNABILITY's decision, made one layer up.
func Scannability(c Canvas) Report {
	rep := Report{
		Score:         100,
		Grade:         GradeExcellent,
		ContrastRatio: ContrastRatio(c.Style.FG, c.Style.BG),
		QuietZone:     c.QuietZone,
		Inverted:      Luminance(c.Style.FG) > Luminance(c.Style.BG),
	}

	penalty := 0
	add := func(issue Issue, cost int) {
		rep.Issues = append(rep.Issues, issue)
		penalty += cost
	}

	switch {
	case rep.ContrastRatio < minContrastHard:
		add(Issue{
			Code:     "LOW_CONTRAST",
			Severity: SeverityError,
			Message: fmt.Sprintf("contrast between modules and background is %.1f:1",
				rep.ContrastRatio),
			Hint: "use at least 4.5:1; a dark module colour on a light background is safest",
		}, 70)
	case rep.ContrastRatio < minContrastSoft:
		add(Issue{
			Code:     "MARGINAL_CONTRAST",
			Severity: SeverityWarn,
			Message: fmt.Sprintf("contrast is %.1f:1, which some cameras will struggle with",
				rep.ContrastRatio),
			Hint: "aim for 4.5:1 or better",
		}, 25)
	}

	if rep.Inverted {
		add(Issue{
			Code:     "INVERTED",
			Severity: SeverityWarn,
			Message:  "light modules on a dark background",
			Hint: "many scanners assume dark-on-light; invert the colours if the code " +
				"must be read by consumer apps",
		}, 20)
	}

	if c.Style.BG.A < 0xFF {
		sev, cost := SeverityWarn, 15
		if c.Style.BG.A == 0 {
			sev, cost = SeverityWarn, 20
		}
		add(Issue{
			Code:     "TRANSPARENT_BACKGROUND",
			Severity: sev,
			Message:  "the background is not fully opaque, so real contrast depends on what sits behind it",
			Hint:     "set style.bg to an opaque colour, or ensure the surface behind it is light",
		}, cost)
	}

	if want := specQuietZone(c.Symbology); c.QuietZone < want {
		sev, cost := SeverityWarn, 25
		if c.QuietZone == 0 && want > 0 {
			sev, cost = SeverityError, 60
		}
		add(Issue{
			Code:     "QUIET_ZONE_TOO_SMALL",
			Severity: sev,
			Message: fmt.Sprintf("quiet zone is %d modules; %s specifies %d",
				c.QuietZone, c.Symbology, want),
			Hint: fmt.Sprintf("set encode.quiet_zone to %d or more", want),
		}, cost)
	}

	if c.Style.EyeFG != nil {
		if r := ContrastRatio(*c.Style.EyeFG, c.Style.BG); r < minContrastHard {
			add(Issue{
				Code:     "LOW_EYE_CONTRAST",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"finder patterns contrast %.1f:1 with the background", r),
				Hint: "the finder patterns are what a scanner locates first; keep them dark",
			}, 60)
		}
	}

	checkGradient(c, add)
	checkLogo(c, add)

	// A code whose modules are overwhelmingly one colour usually means the
	// grid was built wrong; a healthy symbol sits near an even split.
	if total := c.Cols * c.Rows; total > 0 {
		data := float64(c.Dark()) / float64(total)
		if data < 0.05 || data > 0.95 {
			add(Issue{
				Code:     "DEGENERATE_MATRIX",
				Severity: SeverityError,
				Message:  "almost every module is the same colour",
				Hint:     "this usually means the payload or symbology is wrong",
			}, 90)
		}
	}

	rep.Score = max(0, 100-penalty)
	rep.Grade = gradeFor(rep.Score, rep.Issues)

	// Worst first, so a client that shows only the top issue shows the one
	// that matters.
	slicesSortIssues(rep.Issues)
	return rep
}

// checkGradient judges a gradient fill by its two extreme stops.
//
// A ramp is only as good as its worst point. The darkest stop bounds the best
// the symbol will ever look to a camera, so if even that is washed out the
// code is dead everywhere. The lightest stop matters just as much and is the
// one designers get wrong: a gradient that fades to near-white looks elegant
// on a monitor and makes a whole corner of the symbol vanish under a phone
// camera, because a scanner binarises against a local threshold and modules
// that never cross it simply are not there.
func checkGradient(c Canvas, add func(Issue, int)) {
	g := c.Style.Gradient
	if g == nil || len(g.Stops) == 0 {
		return
	}

	if _, darkest := g.Darkest(c.Style.BG); darkest < minContrastHard {
		add(Issue{
			Code:     "GRADIENT_LOW_CONTRAST",
			Severity: SeverityError,
			Message: fmt.Sprintf(
				"the darkest gradient stop contrasts only %.1f:1 with the background",
				darkest),
			Hint: "give the gradient at least one stop that reaches 4.5:1 against style.bg",
		}, 70)
		return
	}

	if _, lightest := g.Lightest(c.Style.BG); lightest < minContrastHard {
		add(Issue{
			Code:     "GRADIENT_FADES_OUT",
			Severity: SeverityWarn,
			Message: fmt.Sprintf(
				"the lightest gradient stop contrasts %.1f:1 with the background, "+
					"so part of the symbol nearly disappears", lightest),
			Hint: "keep every stop above 4.5:1; fading a code to near-white is the " +
				"commonest cause of a design that scans on screen and not in print",
		}, 30)
	}
}

// Logo coverage thresholds, as a fraction of the symbol's area.
//
// QR error correction is specified in codewords, not area: level L recovers
// about 7%, M 15%, Q 25% and H 30%. Those figures are the theoretical maximum
// and assume the damage is spread out. A logo is the opposite — one contiguous
// blob — which concentrates the loss into a few Reed-Solomon blocks and can
// exhaust one block's budget while the others sit idle. So the safe fraction
// is well under the headline number: 8% is roughly what level M tolerates once
// that clustering is accounted for, and past 25% not even level H recovers
// reliably.
const (
	logoCoverageWarn  = 0.08
	logoCoverageError = 0.25
)

// checkLogo reports the risk an excavated logo carries.
//
// Only an excavating logo destroys data. A logo drawn on top without
// excavation is just as unreadable to a camera — the modules under it are
// hidden either way — so the check does not distinguish them: what matters is
// how much of the symbol is gone, not how it went.
func checkLogo(c Canvas, add func(Issue, int)) {
	cover := c.logoCoverage()
	if cover <= 0 {
		return
	}

	ecc := strings.ToUpper(strings.TrimSpace(c.Style.ECC))
	switch {
	case cover > logoCoverageError:
		add(Issue{
			Code:     "LOGO_TOO_LARGE",
			Severity: SeverityError,
			Message: fmt.Sprintf("the logo covers %.0f%% of the symbol",
				cover*100),
			Hint: fmt.Sprintf("keep the logo under %.0f%% of the symbol area, or under %.0f%% "+
				"unless the code is encoded at ECC level H",
				logoCoverageError*100, logoCoverageWarn*100),
		}, 80)

	case cover > logoCoverageWarn && ecc != "H":
		level := "unknown"
		if ecc != "" {
			level = ecc
		}
		add(Issue{
			Code:     "LOGO_EXCEEDS_ECC",
			Severity: SeverityWarn,
			Message: fmt.Sprintf(
				"the logo covers %.0f%% of the symbol at error-correction level %s",
				cover*100, level),
			Hint: "re-encode at ECC level H, or shrink the logo below 8% of the symbol area",
		}, 30)

	case cover > logoCoverageWarn:
		add(Issue{
			Code:     "LOGO_LARGE",
			Severity: SeverityInfo,
			Message: fmt.Sprintf("the logo covers %.0f%% of the symbol, within level H's budget",
				cover*100),
			Hint: "verify the code with a real scanner before printing it",
		}, 0)
	}
}

// gradeFor maps a score and its findings onto a verdict. A single error
// finding caps the grade regardless of score, because "mostly fine plus one
// fatal problem" is not a passing code.
func gradeFor(score int, issues []Issue) Grade {
	for _, i := range issues {
		if i.Severity == SeverityError {
			return GradeUnscannable
		}
	}
	switch {
	case score >= 95:
		return GradeExcellent
	case score >= 75:
		return GradeGood
	default:
		return GradeRisky
	}
}

// slicesSortIssues orders findings by descending severity, keeping the
// original order within a severity so the output is stable.
func slicesSortIssues(issues []Issue) {
	rank := map[Severity]int{SeverityError: 0, SeverityWarn: 1, SeverityInfo: 2}
	for i := 1; i < len(issues); i++ {
		for j := i; j > 0 && rank[issues[j].Severity] < rank[issues[j-1].Severity]; j-- {
			issues[j], issues[j-1] = issues[j-1], issues[j]
		}
	}
}
