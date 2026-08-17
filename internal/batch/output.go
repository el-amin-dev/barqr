package batch

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// manifestName is the zip entry describing the run. It lists every item,
// failures included, so a caller who unpacks the archive and finds 97 files
// for 100 items can see which three failed and why without a second request.
const manifestName = "results.json"

// Naming limits for zip entries. Both are about the machine that unpacks the
// archive, not about us: path components over 255 bytes are rejected by most
// filesystems, and a name longer than this is never information anyway.
const (
	maxStemChars = 64
	maxExtChars  = 8
)

// assignFilenames gives every successful item a unique, safe zip entry name.
//
// Zip-slip is an extraction bug, not a compression bug: the archive stores
// whatever name it is given, and the tool that unpacks it happily writes to
// `../../etc/cron.d/x` if that is what the entry says. barqr writes these
// archives from caller-supplied IDs, so the sanitising has to happen here —
// being the producer is not a defence, it is the reason.
func assignFilenames(slots []slot) {
	// Seeded with the manifest so an item whose ID is "results" cannot
	// overwrite the run's own report.
	taken := map[string]bool{manifestName: true}

	for i := range slots {
		if !slots[i].result.OK {
			// A failed item contributes no file, so naming it would promise an
			// entry the archive does not contain.
			continue
		}
		name := safeStem(slots[i].result.ID, i) + "." + safeExt(slots[i].ext)
		name = unique(name, taken)
		taken[name] = true
		slots[i].result.Filename = name
	}
}

// safeStem reduces a caller-supplied ID to a single path component.
//
// The rule is an allowlist rather than a blocklist: every rune outside
// [A-Za-z0-9._-] becomes a hyphen. That disposes of `/` and `\` (traversal and
// the Windows separator), `:` (drive letters and NTFS alternate data streams),
// leading `~`, control characters, and anything that would need quoting in a
// shell — without a list of dangerous inputs that has to stay complete.
func safeStem(id string, index int) string {
	var b strings.Builder
	b.Grow(len(id))

	var prev rune
	for _, r := range id {
		c := r
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			c = '-'
		}
		// A single dot is fine — "2026.014" is a real order reference — but two
		// in a row are the traversal token, so the second becomes a separator.
		if c == '.' && prev == '.' {
			c = '-'
		}
		// Collapse separator runs, so a path-shaped ID does not turn into a
		// name that is mostly hyphens.
		if c == '-' && prev == '-' {
			continue
		}
		b.WriteRune(c)
		prev = c
	}

	// Trimming leading separators and dots is what removes the last of the
	// traversal shape and stops a dotfile the person unpacking never sees.
	out := strings.Trim(b.String(), "-.")
	if len(out) > maxStemChars {
		out = strings.TrimRight(out[:maxStemChars], "-.")
	}
	// Nothing survived — the ID was punctuation. Fall back to the position,
	// which at least matches what the manifest calls the item.
	if out == "" {
		return "item-" + strconv.Itoa(index+1)
	}
	return out
}

// safeExt normalises the writer's extension. It is not caller-supplied today,
// but it lands in the same file name, so it goes through the same allowlist.
func safeExt(ext string) string {
	ext = strings.ToLower(strings.TrimLeft(ext, "."))
	var b strings.Builder
	for _, r := range ext {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "bin"
	}
	if len(out) > maxExtChars {
		out = out[:maxExtChars]
	}
	return out
}

// unique suffixes a name until it is free, so two items sharing an ID produce
// two files instead of one silently overwriting the other.
func unique(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	// Split at the last dot so an ID that legitimately contains one — an
	// order reference like "2026.014" — keeps its shape.
	stem, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		stem, ext = name[:dot], name[dot:]
	}
	for n := 2; ; n++ {
		candidate := stem + "-" + strconv.Itoa(n) + ext
		if !taken[candidate] {
			return candidate
		}
	}
}

// storedMIMEs are the media types whose bytes are already compressed. Running
// deflate over a PNG or a JPEG costs CPU across the whole batch and reliably
// makes the archive slightly larger, so those entries are stored verbatim.
var storedMIMEs = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"application/pdf": true,
}

// zipOutput packages the successful items plus the manifest.
func zipOutput(slots []slot) (*Output, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, s := range slots {
		if !s.result.OK {
			continue
		}
		method := zip.Deflate
		if storedMIMEs[s.result.MIME] {
			method = zip.Store
		}
		// Modified is left at its zero value on purpose: the same batch must
		// produce byte-identical archives so a caller can cache or diff them.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: s.result.Filename, Method: method})
		if err != nil {
			return nil, fmt.Errorf("packaging %s: %w", s.result.Filename, err)
		}
		if _, err := w.Write(s.body); err != nil {
			return nil, fmt.Errorf("packaging %s: %w", s.result.Filename, err)
		}
	}

	manifest, err := json.Marshal(results(slots))
	if err != nil {
		return nil, fmt.Errorf("building the manifest: %w", err)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Deflate})
	if err != nil {
		return nil, fmt.Errorf("packaging the manifest: %w", err)
	}
	if _, err := w.Write(manifest); err != nil {
		return nil, fmt.Errorf("packaging the manifest: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalising the archive: %w", err)
	}

	return &Output{
		Body:     buf.Bytes(),
		MIME:     "application/zip",
		Filename: "barqr-batch.zip",
		Results:  results(slots),
	}, nil
}

// jsonOutput returns the results inline, each body base64-encoded.
//
// base64 costs a third more bytes than the archive does, which is the price of
// a response a caller can hand straight to a JSON parser. It is the right
// default for a handful of codes and the wrong one for a thousand; the zip
// output exists for the latter.
func jsonOutput(slots []slot) (*Output, error) {
	out := results(slots)
	for i, s := range slots {
		if s.result.OK {
			out[i].Body = base64.StdEncoding.EncodeToString(s.body)
		}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding the results: %w", err)
	}

	return &Output{
		Body:     body,
		MIME:     "application/json",
		Filename: "barqr-batch.json",
		Results:  out,
	}, nil
}

// results extracts the manifest view: outcomes without bodies, in input order.
func results(slots []slot) []Result {
	out := make([]Result, len(slots))
	for i, s := range slots {
		out[i] = s.result
	}
	return out
}
