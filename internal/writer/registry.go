package writer

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds every output writer, keyed by format name. It is populated
// from init functions and read-only afterwards.
var registry = struct {
	sync.RWMutex
	byName map[string]Writer
}{byName: make(map[string]Writer)}

// Register adds a writer under its own format name. It panics on a duplicate,
// because two writers claiming one format is a programming error that must
// surface at startup rather than resolve arbitrarily.
//
// Register is intended for init functions.
func Register(w Writer) {
	registry.Lock()
	defer registry.Unlock()

	if w.Name() == "" {
		panic("writer: Register called with an empty name")
	}
	if _, dup := registry.byName[w.Name()]; dup {
		panic(fmt.Sprintf("writer: format %q registered twice", w.Name()))
	}
	registry.byName[w.Name()] = w
}

// Get returns the writer registered for a format.
func Get(format string) (Writer, error) {
	registry.RLock()
	defer registry.RUnlock()

	w, ok := registry.byName[format]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
	}
	return w, nil
}

// Formats lists every registered format, sorted. It backs the format list in
// /v1/symbologies and the closest-match suggestion on an unknown format.
func Formats() []string {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]string, 0, len(registry.byName))
	for name := range registry.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// All returns every registered writer, sorted by format name.
func All() []Writer {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]Writer, 0, len(registry.byName))
	for _, w := range registry.byName {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
