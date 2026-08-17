package batch

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubRender echoes each item's data back as a PNG-shaped body, which is all
// Run needs to package a result.
func stubRender(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
	return Rendered{
		Body:      []byte("body:" + item.Data),
		MIME:      "image/png",
		Extension: "png",
		Data:      item.Data,
	}, nil
}

// items builds n items with predictable ids and data.
func items(n int) []Item {
	out := make([]Item, n)
	for i := range n {
		out[i] = Item{ID: fmt.Sprintf("id-%03d", i), Data: fmt.Sprintf("data-%03d", i)}
	}
	return out
}

func TestRunKeepsResultsInInputOrderDespiteConcurrentRendering(t *testing.T) {
	t.Parallel()

	const n = 60
	// Randomised delays make a scheduling-dependent implementation fail here
	// rather than in production: with these, "append as they finish" produces
	// a different order on almost every run.
	render := func(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
		time.Sleep(time.Duration(rand.IntN(2000)) * time.Microsecond)
		return Rendered{
			Body:      []byte(item.Data),
			MIME:      "image/png",
			Extension: "png",
			Data:      item.Data,
		}, nil
	}

	out, err := Run(context.Background(), Request{Items: items(n), Output: OutputJSON},
		render, RunOptions{Concurrency: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Results) != n {
		t.Fatalf("got %d results, want %d", len(out.Results), n)
	}
	for i, r := range out.Results {
		want := fmt.Sprintf("data-%03d", i)
		if r.Data != want {
			t.Fatalf("result %d has data %q, want %q — results are out of order", i, r.Data, want)
		}
		if r.ID != fmt.Sprintf("id-%03d", i) {
			t.Fatalf("result %d has id %q", i, r.ID)
		}
	}
}

func TestRunHonoursTheConcurrencyBound(t *testing.T) {
	t.Parallel()

	const limit = 3
	var inFlight, peak atomic.Int64

	render := func(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
		now := inFlight.Add(1)
		for {
			high := peak.Load()
			if now <= high || peak.CompareAndSwap(high, now) {
				break
			}
		}
		time.Sleep(500 * time.Microsecond)
		inFlight.Add(-1)
		return Rendered{Body: []byte(item.Data), MIME: "image/png", Extension: "png"}, nil
	}

	if _, err := Run(context.Background(), Request{Items: items(40)},
		render, RunOptions{Concurrency: limit}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := peak.Load(); got > limit {
		t.Fatalf("peak concurrency was %d, want at most %d", got, limit)
	}
	if peak.Load() < 2 {
		t.Fatal("nothing ran concurrently; the bound is not being used as a bound")
	}
}

func TestRunIsolatesAFailingItem(t *testing.T) {
	t.Parallel()

	render := func(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
		if item.ID == "id-002" {
			return Rendered{}, errors.New("data too long for the symbology")
		}
		return stubRender(context.Background(), item, nil)
	}

	out, err := Run(context.Background(), Request{Items: items(5)}, render, RunOptions{})
	if err != nil {
		t.Fatalf("one bad item must not fail the batch, got %v", err)
	}
	if len(out.Results) != 5 {
		t.Fatalf("got %d results, want 5", len(out.Results))
	}
	bad := out.Results[2]
	if bad.OK || !strings.Contains(bad.Error, "too long") || bad.Filename != "" {
		t.Fatalf("failed result = %+v", bad)
	}
	for i, r := range out.Results {
		if i != 2 && !r.OK {
			t.Fatalf("result %d failed alongside the bad one: %+v", i, r)
		}
	}

	// The archive contains the four that worked, plus the manifest.
	names := zipNames(t, out.Body)
	if len(names) != 5 {
		t.Fatalf("zip entries = %v, want four codes and a manifest", names)
	}
}

func TestRunTreatsAnEmptyBodyAsAFailure(t *testing.T) {
	t.Parallel()

	render := func(_ context.Context, _ Item, _ map[string]any) (Rendered, error) {
		return Rendered{MIME: "image/png", Extension: "png"}, nil
	}

	out, err := Run(context.Background(), Request{Items: items(1)}, render, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Results[0].OK || !strings.Contains(out.Results[0].Error, "no bytes") {
		t.Fatalf("result = %+v", out.Results[0])
	}
}

func TestRunTurnsARendererPanicIntoAFailedItem(t *testing.T) {
	t.Parallel()

	// A panic on a goroutine this package spawns would bypass the HTTP layer's
	// recovery middleware and take the process with it.
	render := func(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
		if item.ID == "id-001" {
			panic("renderer bug")
		}
		return stubRender(context.Background(), item, nil)
	}

	out, err := Run(context.Background(), Request{Items: items(3)}, render, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Results[1].OK || out.Results[1].Error == "" {
		t.Fatalf("panicking item = %+v", out.Results[1])
	}
	// The panic value must not reach the caller: it is an internal detail and
	// may carry anything.
	if strings.Contains(out.Results[1].Error, "renderer bug") {
		t.Fatalf("the panic value leaked into %q", out.Results[1].Error)
	}
	if !out.Results[0].OK || !out.Results[2].OK {
		t.Fatal("a panic in one item stopped the others")
	}
}

func TestRunTruncatesARunawayErrorMessage(t *testing.T) {
	t.Parallel()

	render := func(_ context.Context, _ Item, _ map[string]any) (Rendered, error) {
		return Rendered{}, errors.New(strings.Repeat("x", 10_000))
	}

	out, err := Run(context.Background(), Request{Items: items(1)}, render, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len([]rune(out.Results[0].Error)); n > maxErrorChars+1 {
		t.Fatalf("error is %d runes, want at most %d", n, maxErrorChars+1)
	}
}

func TestRunRejectsStructuralFaultsBeforeRendering(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	counting := func(ctx context.Context, item Item, d map[string]any) (Rendered, error) {
		calls.Add(1)
		return stubRender(ctx, item, d)
	}

	cases := []struct {
		name    string
		req     Request
		opts    RunOptions
		wantErr error
		wants   string
	}{
		{
			"no items and no csv",
			Request{},
			RunOptions{},
			ErrEmptyBatch,
			"supply items or csv",
		},
		{
			"csv with only a header",
			Request{CSV: "id,data\n"},
			RunOptions{},
			ErrEmptyBatch,
			"no rows",
		},
		{
			"both items and csv",
			Request{Items: items(1), CSV: "id,data\na,one\n"},
			RunOptions{},
			ErrBadCSV,
			"not both",
		},
		{
			"unparseable csv",
			Request{CSV: "id,colour\na,red\n"},
			RunOptions{},
			ErrBadCSV,
			"unknown column",
		},
		{
			"over the item cap",
			Request{Items: items(11)},
			RunOptions{MaxItems: 10},
			ErrTooManyItems,
			"11 items exceeds the limit of 10",
		},
		{
			"pdf output",
			Request{Items: items(1), Output: "pdf"},
			RunOptions{},
			ErrUnsupportedOutput,
			"/v1/sheet",
		},
		{
			"unknown output",
			Request{Items: items(1), Output: "tiff"},
			RunOptions{},
			ErrUnsupportedOutput,
			"expected zip or json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			_, err := Run(context.Background(), tc.req, counting, tc.opts)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.wants)
			}
			// A structural fault must cost nothing: a batch of a million items
			// over the cap should not render one of them.
			if calls.Load() != before {
				t.Fatalf("the renderer ran %d times for a rejected batch",
					calls.Load()-before)
			}
		})
	}
}

func TestRunAcceptsTheItemCapExactly(t *testing.T) {
	t.Parallel()

	out, err := Run(context.Background(), Request{Items: items(10)},
		stubRender, RunOptions{MaxItems: 10})
	if err != nil {
		t.Fatalf("a batch of exactly MaxItems must be accepted, got %v", err)
	}
	if len(out.Results) != 10 {
		t.Fatalf("got %d results, want 10", len(out.Results))
	}
}

func TestRunRequiresARenderFunction(t *testing.T) {
	t.Parallel()

	if _, err := Run(context.Background(), Request{Items: items(1)}, nil, RunOptions{}); !errors.Is(
		err, ErrNoRenderer) {
		t.Fatalf("err = %v, want ErrNoRenderer", err)
	}
}

func TestRunStopsOnACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, Request{Items: items(50)}, stubRender, RunOptions{Concurrency: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "cancelled after dispatching") {
		t.Fatalf("err = %q, want it to report the progress made", err)
	}
}

func TestRunStopsPromptlyWhenCancelledMidRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var started atomic.Int64
	render := func(ctx context.Context, item Item, _ map[string]any) (Rendered, error) {
		// Concurrency is 4, so the fourth start is the last one that can
		// happen before every worker is blocked here.
		if started.Add(1) == 4 {
			cancel()
		}
		<-ctx.Done()
		return Rendered{}, ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Request{Items: items(10_000)}, render, RunOptions{Concurrency: 4})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within five seconds of cancellation")
	}
	// Ten thousand items were queued; a prompt stop means almost none ran.
	if got := started.Load(); got > 1000 {
		t.Fatalf("%d items started after cancellation; dispatch is not checking ctx", got)
	}
}

func TestRunRendersACSVBatch(t *testing.T) {
	t.Parallel()

	req := Request{
		CSV:      "id,data,style.module\nalpha,one,dot\nbeta,two,\n",
		Defaults: map[string]any{"output.format": "png"},
		Output:   OutputJSON,
	}

	var seen []map[string]any
	render := func(ctx context.Context, item Item, defaults map[string]any) (Rendered, error) {
		seen = append(seen, defaults)
		return stubRender(ctx, item, defaults)
	}

	out, err := Run(context.Background(), req, render, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Results) != 2 || out.Results[0].ID != "alpha" || out.Results[1].ID != "beta" {
		t.Fatalf("results = %+v", out.Results)
	}
	// Defaults reach the renderer untouched; merging them is the caller's job.
	for _, d := range seen {
		if d["output.format"] != "png" {
			t.Fatalf("defaults = %v", d)
		}
	}
}

func TestRunNumbersItemsWithoutAnID(t *testing.T) {
	t.Parallel()

	req := Request{Items: []Item{{Data: "a"}, {ID: "  ", Data: "b"}, {ID: "given", Data: "c"}}}
	out, err := Run(context.Background(), req, stubRender, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1-based, so the number in a result lines up with the CSV row a human is
	// looking at.
	want := []string{"1", "2", "given"}
	for i, r := range out.Results {
		if r.ID != want[i] {
			t.Fatalf("result %d id = %q, want %q", i, r.ID, want[i])
		}
	}
}

func TestRunZipOutputIsAReadableArchiveWithAManifest(t *testing.T) {
	t.Parallel()

	out, err := Run(context.Background(), Request{Items: items(3), Output: OutputZIP},
		stubRender, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.MIME != "application/zip" || out.Filename != "barqr-batch.zip" {
		t.Fatalf("output = %+v", out)
	}

	zr, err := zip.NewReader(bytes.NewReader(out.Body), int64(len(out.Body)))
	if err != nil {
		t.Fatalf("the archive does not parse: %v", err)
	}

	var manifest []Result
	found := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name, err)
		}
		if f.Name == manifestName {
			if err := json.Unmarshal(body, &manifest); err != nil {
				t.Fatalf("the manifest is not JSON: %v", err)
			}
			continue
		}
		found[f.Name] = string(body)
		// An already-compressed body is stored, not deflated.
		if f.Method != zip.Store {
			t.Fatalf("%s used method %d, want Store for a PNG", f.Name, f.Method)
		}
	}

	if len(found) != 3 {
		t.Fatalf("archive entries = %v, want three codes", found)
	}
	for i := range 3 {
		name := fmt.Sprintf("id-%03d.png", i)
		if found[name] != fmt.Sprintf("body:data-%03d", i) {
			t.Fatalf("entry %s = %q", name, found[name])
		}
	}
	if len(manifest) != 3 || manifest[0].Filename != "id-000.png" || !manifest[0].OK {
		t.Fatalf("manifest = %+v", manifest)
	}
	// The manifest describes the run, not the payload; it must not carry the
	// bodies that are already in the archive.
	if manifest[0].Body != "" {
		t.Fatal("the manifest duplicated the file bodies")
	}
}

func TestRunZipEntryNamesCannotEscapeTheArchive(t *testing.T) {
	t.Parallel()

	// Zip-slip: an extraction tool writes whatever name the entry carries.
	// These IDs are what an attacker supplies when the ID is echoed into one.
	hostile := []Item{
		{ID: "../../etc/cron.d/pwn", Data: "a"},
		{ID: "/etc/passwd", Data: "b"},
		{ID: `..\..\windows\system32\evil`, Data: "c"},
		{ID: "C:\\Windows\\Temp\\x", Data: "d"},
		{ID: "..", Data: "e"},
		{ID: ".", Data: "f"},
		{ID: "....//....//x", Data: "g"},
		{ID: ".ssh/authorized_keys", Data: "h"},
		{ID: "with space and \x00nul", Data: "i"},
		{ID: strings.Repeat("l", 500), Data: "j"},
		{ID: "results", Data: "k"},
	}

	out, err := Run(context.Background(), Request{Items: hostile}, stubRender, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	names := zipNames(t, out.Body)
	seen := map[string]bool{}
	for _, name := range names {
		switch {
		case strings.Contains(name, "/"), strings.Contains(name, `\`):
			t.Fatalf("entry %q contains a path separator", name)
		case strings.Contains(name, ".."):
			t.Fatalf("entry %q contains a parent reference", name)
		case strings.HasPrefix(name, "."):
			t.Fatalf("entry %q is a dotfile", name)
		case strings.Contains(name, ":"):
			t.Fatalf("entry %q contains a drive or stream separator", name)
		case strings.ContainsRune(name, 0):
			t.Fatalf("entry %q contains a NUL", name)
		case len(name) > maxStemChars+1+maxExtChars+8:
			t.Fatalf("entry %q is too long for a path component", name)
		case seen[name]:
			t.Fatalf("entry %q appears twice; one file silently overwrites another", name)
		}
		seen[name] = true
	}
	// Eleven codes plus the manifest, all distinct.
	if len(names) != 12 {
		t.Fatalf("entries = %v, want twelve", names)
	}
	// An item id of "results" must not displace the run's own report.
	if !seen[manifestName] {
		t.Fatalf("the manifest is missing from %v", names)
	}
}

func TestRunGivesDuplicateIDsDistinctEntries(t *testing.T) {
	t.Parallel()

	dupes := []Item{
		{ID: "ticket", Data: "a"},
		{ID: "ticket", Data: "b"},
		{ID: "ticket", Data: "c"},
		{ID: "2026.014", Data: "d"},
		{ID: "2026.014", Data: "e"},
	}

	out, err := Run(context.Background(), Request{Items: dupes}, stubRender, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"ticket.png", "ticket-2.png", "ticket-3.png", "2026.014.png", "2026.014-2.png"}
	for i, r := range out.Results {
		if r.Filename != want[i] {
			t.Fatalf("result %d filename = %q, want %q", i, r.Filename, want[i])
		}
	}
	if len(zipNames(t, out.Body)) != 6 {
		t.Fatal("duplicate ids collapsed into one entry")
	}
}

func TestRunJSONOutputBase64EncodesEveryBody(t *testing.T) {
	t.Parallel()

	render := func(_ context.Context, item Item, _ map[string]any) (Rendered, error) {
		if item.ID == "id-001" {
			return Rendered{}, errors.New("nope")
		}
		return Rendered{
			Body:      []byte{0x00, 0xFF, 0x10, byte(len(item.Data))},
			MIME:      "image/png",
			Extension: "png",
			Data:      item.Data,
		}, nil
	}

	out, err := Run(context.Background(), Request{Items: items(3), Output: "JSON"},
		render, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.MIME != "application/json" || out.Filename != "barqr-batch.json" {
		t.Fatalf("output = %+v", out)
	}

	var decoded []Result
	if err := json.Unmarshal(out.Body, &decoded); err != nil {
		t.Fatalf("body is not a JSON array of results: %v", err)
	}
	if len(decoded) != 3 {
		t.Fatalf("decoded %d results, want 3", len(decoded))
	}
	for i, r := range decoded {
		if i == 1 {
			if r.OK || r.Body != "" {
				t.Fatalf("failed result carries a body: %+v", r)
			}
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(r.Body)
		if err != nil {
			t.Fatalf("result %d body is not base64: %v", i, err)
		}
		if !bytes.Equal(raw, []byte{0x00, 0xFF, 0x10, 8}) {
			t.Fatalf("result %d body = %v", i, raw)
		}
	}
}

func TestRunProducesAByteIdenticalArchiveForTheSameBatch(t *testing.T) {
	t.Parallel()

	// Timestamps left at zero: a caller can cache or diff the archive.
	first, err := Run(context.Background(), Request{Items: items(5)}, stubRender, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	second, err := Run(context.Background(), Request{Items: items(5)}, stubRender,
		RunOptions{Concurrency: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Equal(first.Body, second.Body) {
		t.Fatal("the same batch produced two different archives")
	}
}

func TestSafeStemReducesAnIDToOnePathComponent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want string
	}{
		{"plain", "invoice-42", "invoice-42"},
		{"dotted", "2026.014", "2026.014"},
		{"traversal", "../../etc/passwd", "etc-passwd"},
		{"absolute", "/etc/passwd", "etc-passwd"},
		{"windows", `C:\temp\x`, "C-temp-x"},
		{"parent only", "..", "item-1"},
		{"dot only", ".", "item-1"},
		{"empty", "", "item-1"},
		{"hyphens only", "///", "item-1"},
		{"unicode", "naïve", "na-ve"},
		{"nul", "a\x00b", "a-b"},
		{"over-long", strings.Repeat("a", 200), strings.Repeat("a", maxStemChars)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := safeStem(tc.id, 0); got != tc.want {
				t.Fatalf("safeStem(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestSafeExtNormalisesTheWritersExtension(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"png", "png"},
		{".SVG", "svg"},
		{"", "bin"},
		{"../x", "x"},
		{"a b", "ab"},
		{strings.Repeat("z", 40), strings.Repeat("z", maxExtChars)},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := safeExt(tc.in); got != tc.want {
				t.Fatalf("safeExt(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUniqueSuffixesAnExtensionlessName(t *testing.T) {
	t.Parallel()

	taken := map[string]bool{"plain": true}
	if got := unique("plain", taken); got != "plain-2" {
		t.Fatalf("unique = %q, want plain-2", got)
	}
}

// zipNames lists the entry names of an archive, failing the test if it does
// not parse.
func zipNames(t *testing.T, body []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the archive does not parse: %v", err)
	}
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}
