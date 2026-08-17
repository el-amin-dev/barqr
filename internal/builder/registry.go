package builder

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds every known builder, keyed by payload type name.
//
// It is populated from init functions and read-only afterwards, which is the
// single exception the quality bar makes to "no global mutable state". The
// mutex guards against a package registering from a goroutine and against the
// race detector complaining about init ordering; it is uncontended at runtime.
var registry = struct {
	sync.RWMutex
	byName map[string]Builder
}{byName: make(map[string]Builder)}

// Register adds a builder under its own name. It panics on a duplicate,
// because two builders claiming one payload type is a programming error that
// must surface at startup rather than resolve arbitrarily.
//
// Register is intended for init functions.
func Register(b Builder) {
	registry.Lock()
	defer registry.Unlock()

	name := b.Name()
	if name == "" {
		panic("builder: Register called with an empty name")
	}
	if _, dup := registry.byName[name]; dup {
		panic(fmt.Sprintf("builder: %q registered twice", name))
	}
	registry.byName[name] = b
}

// Get returns the builder registered under name.
func Get(name string) (Builder, error) {
	registry.RLock()
	b, ok := registry.byName[name]
	registry.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, name)
	}
	return b, nil
}

// Names lists every registered payload type, sorted.
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]string, 0, len(registry.byName))
	for name := range registry.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// All returns every registered builder, sorted by name. It backs GET /v1/build
// and the package-wide round-trip test, which is what catches a new builder
// added without one.
func All() []Builder {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]Builder, 0, len(registry.byName))
	for _, b := range registry.byName {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
