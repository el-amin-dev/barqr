package encoder

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds every known encoder, keyed by symbology name.
//
// It is populated from init functions and read-only afterwards, which is the
// single exception the quality bar makes to "no global mutable state". The
// mutex guards against a package registering from a goroutine and against the
// race detector complaining about init ordering; it is uncontended at runtime.
var registry = struct {
	sync.RWMutex
	byName map[string]Encoder
}{byName: make(map[string]Encoder)}

// Register adds an encoder under its own name. It panics on a duplicate,
// because two encoders claiming one symbology is a programming error that must
// surface at startup rather than resolve arbitrarily.
//
// Register is intended for init functions.
func Register(e Encoder) {
	registry.Lock()
	defer registry.Unlock()

	name := e.Name()
	if name == "" {
		panic("encoder: Register called with an empty name")
	}
	if _, dup := registry.byName[name]; dup {
		panic(fmt.Sprintf("encoder: %q registered twice", name))
	}
	registry.byName[name] = e
}

// Get returns the encoder registered under name.
//
// It reports ErrUnavailable rather than ErrUnknownSymbology for a symbology
// that is known but not compiled into this build, so the caller can explain
// the difference to a user instead of claiming it does not exist.
func Get(name string) (Encoder, error) {
	registry.RLock()
	e, ok := registry.byName[name]
	registry.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSymbology, name)
	}
	if caps := e.Caps(); !caps.Available {
		return nil, fmt.Errorf("%w: %q: %s", ErrUnavailable, name, caps.Reason)
	}
	return e, nil
}

// Names lists every registered symbology, available or not, sorted.
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

// All returns the capabilities of every registered symbology, sorted by name.
// It backs GET /v1/symbologies, which must be honest about what this
// particular build can do.
func All() []Capabilities {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]Capabilities, 0, len(registry.byName))
	for _, e := range registry.byName {
		out = append(out, e.Caps())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RegisterUnavailable records a symbology that this build cannot encode, so
// that /v1/symbologies lists it with an explanation instead of omitting it.
// The optional zint-backed symbologies register through here in the default
// build.
func RegisterUnavailable(caps Capabilities) {
	caps.Available = false
	if caps.Reason == "" {
		caps.Reason = "requires the full build"
	}
	Register(unavailable{caps: caps})
}

// unavailable is the placeholder encoder behind RegisterUnavailable.
type unavailable struct{ caps Capabilities }

func (u unavailable) Name() string       { return u.caps.Name }
func (u unavailable) Caps() Capabilities { return u.caps }

func (u unavailable) Encode(string, EncodeOpts) (Matrix, error) {
	return Matrix{}, fmt.Errorf("%w: %q: %s", ErrUnavailable, u.caps.Name, u.caps.Reason)
}
