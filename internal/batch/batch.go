// Package batch renders many codes from one request.
//
// The unit of work is an Item: either raw Data, or a builder Type with a
// Payload, plus per-item option overrides in the same dot notation the request
// layer uses everywhere else. Items arrive either as a JSON array or as a CSV
// document, which is what a warehouse or an events team actually has.
//
// batch does not know how to render anything. The caller supplies a RenderFunc
// and batch supplies ordering, concurrency, failure isolation and packaging.
// That inversion is deliberate: it keeps the HTTP layer out of this package's
// imports, so the interesting behaviour — ordering under concurrency, one bad
// item not sinking the run, zip entry naming — is testable against a stub in
// microseconds instead of against a real encoder.
package batch

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Sentinel errors for the batch package.
//
// Every one of these is a structural fault: the request could not be started
// or could not be finished. A single item failing to render is never an error
// here — it is a Result with OK false.
var (
	// ErrEmptyBatch means the request carried no items at all.
	ErrEmptyBatch = errors.New("batch is empty")
	// ErrTooManyItems means the request exceeded the configured item cap.
	ErrTooManyItems = errors.New("too many items")
	// ErrBadCSV means the CSV document could not be turned into items.
	ErrBadCSV = errors.New("invalid csv")
	// ErrUnsupportedOutput means the requested output format is not one batch
	// can produce.
	ErrUnsupportedOutput = errors.New("unsupported batch output")
	// ErrNoRenderer means Run was called without a RenderFunc, which is a
	// wiring mistake in the caller rather than anything a request can cause.
	ErrNoRenderer = errors.New("no render function supplied")
)

// The output formats Run can produce.
const (
	// OutputZIP packages one file per successful item plus a manifest.
	OutputZIP = "zip"
	// OutputJSON returns the results inline, bodies base64-encoded.
	OutputJSON = "json"
	// OutputPDF is not produced here; see Run for why.
	OutputPDF = "pdf"
)

// defaultConcurrency is used when RunOptions leaves it unset. Rendering is CPU
// bound — encode, paint, compress — so the core count is the ceiling that
// helps; beyond it the work only gets slower and the memory high-water mark
// higher.
var defaultConcurrency = min(runtime.NumCPU(), 8)

// maxErrorChars caps the error text copied into a Result. A manifest with a
// thousand failures must stay a manifest, and no useful diagnostic needs more
// than a line.
const maxErrorChars = 300

// Item is one code to render in a batch.
type Item struct {
	// ID names the item. It is echoed in the results and used as the file
	// stem in a zip, after sanitising. Empty means "use the position".
	ID string `json:"id,omitempty"`
	// Data is the raw string to encode, when no Type is given.
	Data string `json:"data,omitempty"`
	// Type names a builder; Payload is that builder's input.
	Type string `json:"type,omitempty"`
	// Payload holds the builder's fields.
	Payload map[string]any `json:"payload,omitempty"`
	// Options are dot-notation overrides for this item alone. They sit on top
	// of the batch's Defaults.
	Options map[string]any `json:"options,omitempty"`
}

// Request is a whole batch.
type Request struct {
	// Items lists the codes to render.
	Items []Item `json:"items,omitempty"`
	// CSV is an alternative to Items: a header row plus one row per code.
	// Setting both is rejected rather than guessed at.
	CSV string `json:"csv,omitempty"`
	// Defaults are dot-notation options applied to every item.
	Defaults map[string]any `json:"defaults,omitempty"`
	// Output selects the packaging: zip, json, or pdf. Empty means zip.
	Output string `json:"output"`
}

// Rendered is what a RenderFunc produces for one item.
type Rendered struct {
	// Body is the encoded file.
	Body []byte
	// MIME is its media type, used to choose zip compression and to fill the
	// manifest.
	MIME string
	// Extension is the file extension without the dot.
	Extension string
	// Data is the string that was ultimately encoded, which for a built
	// payload is not what the caller sent and is worth echoing back.
	Data string
}

// RenderFunc is how batch renders one item.
//
// The HTTP layer supplies it, so batch never imports httpapi. It is called
// from several goroutines at once and must be safe for concurrent use.
// defaults are the batch-wide options; merging them with the item's own
// overrides is the caller's job, because only the caller knows the precedence
// rules of the request layer.
type RenderFunc func(ctx context.Context, item Item, defaults map[string]any) (Rendered, error)

// Result is the outcome for one item, in input order.
type Result struct {
	// ID is the item's ID, or its position when it had none.
	ID string `json:"id"`
	// OK reports whether the item rendered.
	OK bool `json:"ok"`
	// Data is the string that was encoded.
	Data string `json:"data,omitempty"`
	// Error is the failure reason when OK is false.
	Error string `json:"error,omitempty"`
	// Bytes is the size of the rendered file.
	Bytes int `json:"bytes,omitempty"`
	// Filename is the name this item takes inside a zip.
	Filename string `json:"filename,omitempty"`
	// MIME is the rendered file's media type.
	MIME string `json:"mime,omitempty"`
	// Body is the base64-encoded file, present only in the json output where
	// there is no envelope to carry the bytes separately.
	Body string `json:"body,omitempty"`
}

// RunOptions bounds a run.
type RunOptions struct {
	// MaxItems caps the batch size. Zero or negative means no cap, which is
	// only appropriate for a trusted caller.
	MaxItems int
	// Concurrency bounds how many items render at once. Zero picks a default
	// from the core count.
	Concurrency int
}

// Output is the packaged result of a run.
type Output struct {
	// Body is the packaged bytes: a zip archive or a JSON document.
	Body []byte
	// MIME is Body's media type.
	MIME string
	// Filename is a suggested download name.
	Filename string
	// Results is the per-item outcome, in input order, for a caller that wants
	// to reshape the response itself.
	Results []Result
}

// Run renders every item and packages the results.
//
// Items render concurrently, bounded by RunOptions.Concurrency, but Results is
// always in input order: each worker writes into its own slot, so ordering
// costs nothing and cannot drift with scheduling.
//
// One item failing does not fail the batch. Its Result carries OK false and
// the reason, the rest continue, and the archive simply does not contain that
// file. Run returns an error only for a structural fault: an empty or
// oversized batch, an unparseable CSV, an output format it cannot produce, or
// a cancelled context.
//
// Output "pdf" is deliberately not implemented here. A batch is a bag of
// independent codes with no page geometry; laying codes out on a sheet is a
// different job with a different request shape, and it lives at /v1/sheet.
// Asking for it here returns ErrUnsupportedOutput.
func Run(ctx context.Context, req Request, render RenderFunc, o RunOptions) (*Output, error) {
	if render == nil {
		return nil, ErrNoRenderer
	}

	// Everything that can be rejected without doing work is rejected first, so
	// an oversized or misdirected batch costs one allocation rather than a
	// thousand renders.
	format, err := outputFormat(req.Output)
	if err != nil {
		return nil, err
	}

	items, err := resolveItems(req)
	if err != nil {
		return nil, err
	}
	if o.MaxItems > 0 && len(items) > o.MaxItems {
		return nil, fmt.Errorf("%w: %d items exceeds the limit of %d",
			ErrTooManyItems, len(items), o.MaxItems)
	}

	rendered, err := renderAll(ctx, items, render, req.Defaults, o.Concurrency)
	if err != nil {
		return nil, err
	}

	if format == OutputJSON {
		return jsonOutput(rendered)
	}
	return zipOutput(rendered)
}

// slot is one item's outcome plus the bytes and extension the packagers need
// but the manifest does not carry.
type slot struct {
	result Result
	body   []byte
	ext    string
}

// outputFormat normalises and validates the requested packaging.
func outputFormat(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", OutputZIP:
		return OutputZIP, nil
	case OutputJSON:
		return OutputJSON, nil
	case OutputPDF:
		return "", fmt.Errorf(
			"%w: pdf: a batch has no page geometry; use /v1/sheet to lay codes out for print",
			ErrUnsupportedOutput)
	default:
		return "", fmt.Errorf("%w: %q: expected zip or json", ErrUnsupportedOutput, name)
	}
}

// resolveItems picks the item source and rejects an ambiguous request.
func resolveItems(req Request) ([]Item, error) {
	hasCSV := strings.TrimSpace(req.CSV) != ""
	switch {
	case hasCSV && len(req.Items) > 0:
		// Merging the two would need a documented precedence nobody would
		// remember; refusing costs the caller one edit and no surprises.
		return nil, fmt.Errorf("%w: set either items or csv, not both", ErrBadCSV)
	case hasCSV:
		items, err := ParseCSV(req.CSV)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("%w: the csv has a header but no rows", ErrEmptyBatch)
		}
		return items, nil
	case len(req.Items) > 0:
		return req.Items, nil
	default:
		return nil, fmt.Errorf("%w: supply items or csv", ErrEmptyBatch)
	}
}

// renderAll runs the render function over every item under a bounded number of
// goroutines, returning the outcomes in input order.
func renderAll(
	ctx context.Context,
	items []Item,
	render RenderFunc,
	defaults map[string]any,
	concurrency int,
) ([]slot, error) {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	concurrency = min(concurrency, len(items))

	// Each worker owns exactly one index of this slice, so the results come
	// out in input order for free and no mutex is needed to keep them there.
	out := make([]slot, len(items))

	// A buffered channel is the whole scheduler: a send blocks once
	// `concurrency` renders are in flight, and the receive in each worker's
	// defer releases the ticket.
	tickets := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := range items {
		select {
		case <-ctx.Done():
			// Stop dispatching immediately rather than queueing work whose
			// deadline has already passed.
			wg.Wait()
			return nil, cancelled(ctx, i)
		case tickets <- struct{}{}:
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-tickets }()
			out[i] = renderOne(ctx, items[i], i, render, defaults)
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, cancelled(ctx, len(items))
	}

	// Entry names are assigned after every render, walking the slice in input
	// order, because de-duplication must be deterministic and assigning names
	// inside the workers would make the suffixes depend on scheduling.
	assignFilenames(out)
	return out, nil
}

// cancelled reports a context failure with the progress made, which is the
// difference between "raise the timeout" and "the very first item hangs".
func cancelled(ctx context.Context, done int) error {
	return fmt.Errorf("batch cancelled after dispatching %d items: %w", done, ctx.Err())
}

// renderOne renders a single item and never returns an error: an item's
// failure is data, not control flow.
func renderOne(
	ctx context.Context,
	item Item,
	index int,
	render RenderFunc,
	defaults map[string]any,
) (s slot) {
	s.result = Result{ID: itemID(item, index)}

	// A RenderFunc is supplied by another package and runs on a goroutine this
	// package owns, where a panic would take the whole process down past the
	// HTTP layer's recovery middleware. Converting it into a failed item keeps
	// one malformed payload from being a denial of service.
	defer func() {
		if r := recover(); r != nil {
			s.result.OK = false
			s.result.Error = "renderer failed unexpectedly"
			s.body, s.ext = nil, ""
		}
	}()

	if ctx.Err() != nil {
		s.result.Error = "cancelled before rendering"
		return s
	}

	out, err := render(ctx, item, defaults)
	switch {
	case err != nil:
		s.result.Error = truncate(err.Error(), maxErrorChars)
		return s
	case len(out.Body) == 0:
		s.result.Error = "renderer produced no bytes"
		return s
	}

	s.result.OK = true
	s.result.Bytes = len(out.Body)
	s.result.MIME = out.MIME
	s.result.Data = out.Data
	s.body, s.ext = out.Body, out.Extension
	return s
}

// itemID resolves the identity used in results and file names. Positions are
// 1-based because they line up with the CSV row a human is looking at.
func itemID(item Item, index int) string {
	if id := strings.TrimSpace(item.ID); id != "" {
		return id
	}
	return strconv.Itoa(index + 1)
}

// truncate clips text to n characters, marking that it was clipped.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
