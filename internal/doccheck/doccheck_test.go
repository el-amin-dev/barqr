// Package doccheck holds no code. It exists for one test.
//
// The test reads the project's own documentation and asserts it against the
// live registries, so a claim in README.md or docs/ cannot drift away from
// what the binary actually does. Adding a symbology without listing it, or
// listing one that was never registered, fails the build.
//
// This is the counterpart to the generated documentation the service serves at
// runtime: /v1/symbologies and /v1/openapi.json cannot drift because they are
// built from the registries, and the hand-written markdown cannot drift
// because of this file.
package doccheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/httpapi"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// repoRoot is two levels up from this package.
const repoRoot = "../.."

// read loads a documentation file relative to the repository root.
func read(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

// backticked pulls every `code span` out of a line.
var backticked = regexp.MustCompile("`([^`]+)`")

// listedIn returns the code spans on the first line of doc that starts with
// the given table-row prefix, e.g. "| **2D** |".
func listedIn(t *testing.T, doc, prefix string) []string {
	t.Helper()

	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var out []string
		for _, m := range backticked.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
		if len(out) == 0 {
			t.Fatalf("the row %q lists nothing", prefix)
		}
		sort.Strings(out)
		return out
	}

	t.Fatalf("no line in the document starts with %q", prefix)
	return nil
}

// diff reports what each side has that the other does not.
func diff(t *testing.T, what string, documented, actual []string) {
	t.Helper()

	have := make(map[string]bool, len(actual))
	for _, a := range actual {
		have[a] = true
	}
	said := make(map[string]bool, len(documented))
	for _, d := range documented {
		said[d] = true
	}

	for _, d := range documented {
		if !have[d] {
			t.Errorf("%s: the documentation lists %q, but nothing registers it", what, d)
		}
	}
	for _, a := range actual {
		if !said[a] {
			t.Errorf("%s: %q is registered but the documentation does not list it", what, a)
		}
	}
}

// availableSymbologies returns the symbologies this build can actually encode.
func availableSymbologies() (available, unavailable []string) {
	for _, c := range encoder.All() {
		if c.Available {
			available = append(available, c.Name)
		} else {
			unavailable = append(unavailable, c.Name)
		}
	}
	sort.Strings(available)
	sort.Strings(unavailable)
	return available, unavailable
}

// TestReadmeListsEverySymbology is the check that matters most: the README's
// capability table is the first thing anyone reads, and a symbology missing
// from it is a feature nobody knows exists.
func TestReadmeListsEverySymbology(t *testing.T) {
	t.Parallel()

	doc := read(t, "README.md")
	available, _ := availableSymbologies()

	documented := append(listedIn(t, doc, "| **2D** |"), listedIn(t, doc, "| **1D** |")...)
	sort.Strings(documented)

	diff(t, "symbologies", documented, available)
}

// TestReadmeSplitsSymbologiesByKind guards the rows themselves: a 1D code
// listed under 2D would pass the combined check above.
func TestReadmeSplitsSymbologiesByKind(t *testing.T) {
	t.Parallel()

	doc := read(t, "README.md")

	kinds := map[string]encoder.Kind{
		"| **2D** |": encoder.Kind2D,
		"| **1D** |": encoder.Kind1D,
	}
	caps := make(map[string]encoder.Capabilities, len(encoder.All()))
	for _, c := range encoder.All() {
		caps[c.Name] = c
	}

	for prefix, want := range kinds {
		for _, name := range listedIn(t, doc, prefix) {
			c, ok := caps[name]
			if !ok {
				continue // the missing-symbology test reports this
			}
			if c.Kind != want {
				t.Errorf("%s is listed under %q but registers as %q", name, prefix, c.Kind)
			}
		}
	}
}

func TestReadmeListsEveryBuilder(t *testing.T) {
	t.Parallel()

	diff(t, "builders",
		listedIn(t, read(t, "README.md"), "| **Payloads** |"),
		sortedCopy(builder.Names()))
}

func TestReadmeListsEveryOutputFormat(t *testing.T) {
	t.Parallel()

	diff(t, "output formats",
		listedIn(t, read(t, "README.md"), "| **Output** |"),
		sortedCopy(writer.Formats()))
}

// TestReadmeHeadlineCountsAreRight checks the line at the top of the README,
// which is the single most-read sentence in the project and the easiest to
// forget when something is added.
func TestReadmeHeadlineCountsAreRight(t *testing.T) {
	t.Parallel()

	doc := read(t, "README.md")
	available, unavailable := availableSymbologies()

	headline := regexp.MustCompile(
		`\*\*(\d+) symbologies · (\d+) payload builders · (\d+) output formats`)
	m := headline.FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("the README headline no longer states the counts in the expected form")
	}

	for _, tc := range []struct {
		what string
		said string
		got  int
	}{
		{"symbologies", m[1], len(available)},
		{"payload builders", m[2], len(builder.Names())},
		{"output formats", m[3], len(writer.Formats())},
	} {
		said, err := strconv.Atoi(tc.said)
		if err != nil {
			t.Fatalf("%s: %q is not a number", tc.what, tc.said)
		}
		if said != tc.got {
			t.Errorf("the README headline claims %d %s; there are %d", said, tc.what, tc.got)
		}
	}

	// The claim about what is deliberately not compiled in.
	if !strings.Contains(doc, fmt.Sprintf("%s more symbologies", spelled(len(unavailable)))) {
		t.Errorf("the README does not say %q more symbologies are registered as unavailable; "+
			"there are %d", spelled(len(unavailable)), len(unavailable))
	}
}

// TestReadmeStyleCountsAreRight covers the shapes row, which counts rather
// than lists.
func TestReadmeStyleCountsAreRight(t *testing.T) {
	t.Parallel()

	doc := read(t, "README.md")

	row := regexp.MustCompile(`\| \*\*Style\*\* \| (\d+) module shapes, (\d+) eye shapes`)
	m := row.FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("the README style row no longer states the shape counts in the expected form")
	}

	for _, tc := range []struct {
		what string
		said string
		got  int
	}{
		{"module shapes", m[1], len(render.ModuleShapes())},
		{"eye shapes", m[2], len(render.EyeShapes())},
	} {
		said, _ := strconv.Atoi(tc.said)
		if said != tc.got {
			t.Errorf("the README claims %d %s; there are %d (%v)",
				said, tc.what, tc.got, registered(tc.what))
		}
	}
}

// registered returns the actual names, for a failure message that says what to
// change rather than only that something is wrong.
func registered(what string) []string {
	if what == "module shapes" {
		return render.ModuleShapes()
	}
	return render.EyeShapes()
}

// TestDeployDocumentsEveryEnvVar guards the reference table an operator
// configures the service from. A variable the binary reads but the table omits
// is a setting nobody can discover.
func TestDeployDocumentsEveryEnvVar(t *testing.T) {
	t.Parallel()

	doc := read(t, "docs/DEPLOY.md")

	for _, key := range config.Keys() {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("docs/DEPLOY.md does not document %s", key)
		}
	}

	// And the reverse: a variable the reference table lists but the binary
	// ignores is worse than an omission, because somebody will set it and
	// expect an effect.
	//
	// Only table rows count. The prose deliberately names BARQR_API_KEY as an
	// example of the sort of typo the unknown-variable warning catches, and
	// that is not a claim that the variable exists.
	known := make(map[string]bool, len(config.Keys()))
	for _, k := range config.Keys() {
		known[k] = true
	}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		for _, m := range regexp.MustCompile("`(BARQR_[A-Z_]+)`").FindAllStringSubmatch(line, -1) {
			if !known[m[1]] {
				t.Errorf("the docs/DEPLOY.md reference table lists %s, "+
					"which the binary does not read", m[1])
			}
		}
	}
}

// TestAPIDocumentsEveryEndpoint checks the reference against the router.
func TestAPIDocumentsEveryEndpoint(t *testing.T) {
	t.Parallel()

	doc := read(t, "docs/API.md")

	// Every route the service answers must appear somewhere in the reference.
	for _, route := range []string{
		"/v1/qr", "/v1/barcode/{symbology}", "/v1/build/{type}",
		"/v1/validate", "/v1/decode", "/v1/batch", "/v1/sheet",
		"/v1/preset", "/v1/symbologies", "/v1/openapi.json",
		"/v1/healthz", "/v1/readyz", "/v1/version", "/v1/docs", "/metrics",
	} {
		if !strings.Contains(doc, route) {
			t.Errorf("docs/API.md does not mention %s", route)
		}
	}
}

// TestAPIDocumentsEveryErrorCode keeps the error catalogue honest.
//
// A client switches on these, so a code that exists in the binary but not in
// the reference is a contract nobody can program against. The list comes from
// httpapi.Codes rather than being repeated here — a second hand-kept list
// would be exactly the drift these tests exist to prevent.
func TestAPIDocumentsEveryErrorCode(t *testing.T) {
	t.Parallel()

	doc := read(t, "docs/API.md")

	for _, code := range httpapi.Codes() {
		if !strings.Contains(doc, code) {
			t.Errorf("docs/API.md does not document the error code %s", code)
		}
	}
}

// TestDockerhubMatchesTheReadme keeps the registry overview from becoming a
// stale copy: it is the page most people read first and the one nobody
// remembers to update.
func TestDockerhubMatchesTheReadme(t *testing.T) {
	t.Parallel()

	doc := read(t, "docs/DOCKERHUB.md")

	for _, must := range []string{
		"BARQR_AUTH_MODE", "BARQR_API_KEYS", "BARQR_BIND",
		"/v1/healthz", "/v1/readyz", "/v1/version",
		"el-amin-dev",
	} {
		if !strings.Contains(doc, must) {
			t.Errorf("docs/DOCKERHUB.md does not mention %s", must)
		}
	}
}

// sortedCopy returns a sorted copy, leaving the registry's slice alone.
func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// spelled renders a small number the way the prose does.
func spelled(n int) string {
	words := map[int]string{
		15: "Fifteen", 16: "Sixteen", 17: "Seventeen", 18: "Eighteen",
		19: "Nineteen", 20: "Twenty", 21: "Twenty-one", 22: "Twenty-two",
		23: "Twenty-three", 24: "Twenty-four", 25: "Twenty-five",
	}
	if w, ok := words[n]; ok {
		return w
	}
	return strconv.Itoa(n)
}

// TestAPIDocumentsEveryDefault holds docs/API.md's Default column to the same
// source the generated OpenAPI document uses.
//
// A user reported reading the spec, seeing style.hri typed only as a boolean,
// assuming the default was off, and getting a caption. The spec now states
// every default; this makes the markdown state the same ones, from the same
// map, so the two cannot disagree with each other or with the code. What the
// service does and what the documentation says is one fact, checked here.
// TestTheImageSizeClaimIsConsistent holds the two published statements of the
// image size to each other.
//
// It is hand-typed in several places and asserted by nothing, which is the same
// duplicated-source-of-truth shape the defaults work exists to remove. This
// cannot check the real image — that needs a build — but it can stop the two
// documents disagreeing, which is how a stale number survives.
func TestTheImageSizeClaimIsConsistent(t *testing.T) {
	t.Parallel()

	size := regexp.MustCompile(`(\d+\.\d+) MB`)

	readme := size.FindStringSubmatch(read(t, "README.md"))
	if readme == nil {
		t.Fatal("README.md no longer states an image size")
	}
	hub := size.FindStringSubmatch(read(t, "docs/DOCKERHUB.md"))
	if hub == nil {
		t.Fatal("docs/DOCKERHUB.md no longer states an image size")
	}
	if readme[1] != hub[1] {
		t.Errorf("README.md says the image is %s MB, docs/DOCKERHUB.md says %s MB",
			readme[1], hub[1])
	}
}

func TestAPIDocumentsEveryDefault(t *testing.T) {
	t.Parallel()

	doc := read(t, "docs/API.md")

	cfg, _, err := config.Load([]string{"BARQR_AUTH_MODE=open"})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	for path, want := range httpapi.Defaults(cfg) {
		// The Default cell of the row whose first cell is this field.
		row := regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta("`"+path+"`") + ` \| ([^|]*) \|`)
		m := row.FindStringSubmatch(doc)
		if m == nil {
			t.Errorf("%s: no row in docs/API.md, but the code gives it a default", path)
			continue
		}

		// Compare against the backticked tokens in the cell rather than by
		// substring: a cell of `22` contains "2" and would satisfy a default
		// of 2. Two rows legitimately name an environment variable alongside
		// the value it defaults to — `BARQR_DEFAULT_ECC` (`M`) — which is why
		// this matches any token in the cell rather than the whole cell.
		stated := strings.TrimSpace(m[1])
		want := fmt.Sprintf("%v", want)
		if !slices.Contains(backticked.FindAllString(stated, -1), "`"+want+"`") {
			t.Errorf("%s: docs/API.md states %q, the code applies %q",
				path, stated, want)
		}
	}
}
