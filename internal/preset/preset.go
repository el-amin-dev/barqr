// Package preset holds named bundles of barqr options.
//
// A preset is the answer to "we always send the same fourteen query
// parameters". The caller names one — `?preset=print` — and the service
// expands it into the option keys the request layer already understands, in
// the same dot notation the query string and multipart transports use
// (`style.module`, `output.dpi`, `encode.ecc`). Nothing here knows what those
// keys mean; expansion and validation stay with the request layer, so adding
// an option to Request makes it presettable with no change to this package.
//
// Presets come from two places: the built-ins compiled into the binary, and a
// directory of JSON files named by BARQR_PRESETS_PATH. The directory is
// operator-supplied and therefore untrusted input — see Load for the guards.
package preset

import (
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Sentinel errors for the preset package.
var (
	// ErrNotADirectory means the configured presets path is not a directory.
	ErrNotADirectory = errors.New("presets path is not a directory")
	// ErrUnreadable means the presets directory could not be listed at all,
	// which is a configuration fault rather than a bad individual file.
	ErrUnreadable = errors.New("presets directory could not be read")
)

// Limits on what a presets directory may contain. They exist because the
// directory is mounted by an operator and read at boot: a runaway file or a
// runaway file count must fail loudly and cheaply, not exhaust the process.
const (
	// MaxFileBytes caps one preset file. A preset is a flat map of a few dozen
	// short strings; 64 KiB is two orders of magnitude of headroom, and
	// anything larger is a mistake or an attack rather than a preset.
	MaxFileBytes = 64 << 10

	// MaxPresets caps how many presets a Set may hold, built-ins included.
	// The names are served on /v1/presets and offered as typo suggestions, so
	// an unbounded count would turn a mounted directory into a response-size
	// and CPU problem elsewhere in the service.
	MaxPresets = 256

	// MaxOptions caps the option keys in one preset. The request layer has
	// well under a hundred settable paths, so a preset needing more than this
	// is not describing a barqr request.
	MaxOptions = 128

	// maxKeyBytes caps a single option key. Keys are dot-notation paths into
	// the Request struct; the longest real one is around twenty characters.
	maxKeyBytes = 64
)

// slugPattern is the only shape a preset name may take.
//
// Preset names double as file stems on disk and as values echoed back in JSON
// and error messages, so they are restricted to a conservative slug rather
// than validated case by case at each use. Anything outside this alphabet —
// a dot, a slash, a NUL, a leading dash — is rejected at the boundary and
// never reaches path handling or output encoding.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidName reports whether name is a legal preset name.
//
// The rule is `^[a-z0-9][a-z0-9_-]{0,63}$`: lowercase alphanumerics, hyphens
// and underscores, starting with an alphanumeric, at most 64 characters.
func ValidName(name string) bool { return slugPattern.MatchString(name) }

// Preset is a named bundle of options.
//
// Options are keyed in the request layer's dot notation — "style.module",
// "output.format", "encode.ecc" — and the values are whatever JSON produced:
// string, float64, bool. They are applied as if the caller had sent them, so
// an explicit parameter on the request overrides the preset's value for the
// same key.
type Preset struct {
	// Name is the slug the caller asks for. It matches ValidName.
	Name string `json:"name"`
	// Description is one line of prose for /v1/presets listings.
	Description string `json:"description,omitempty"`
	// Options maps dot-notation option keys to values.
	Options map[string]any `json:"options"`
}

// Clone returns a copy whose Options map shares nothing with the receiver.
//
// Sets are read-only after construction and are shared by every in-flight
// request, so handing out the internal map would let one request's option
// merge corrupt every later request that names the same preset.
func (p Preset) Clone() Preset {
	p.Options = maps.Clone(p.Options)
	if p.Options == nil {
		// A nil map is legal to read but panics on write, and the request
		// layer may want to merge into what it is given.
		p.Options = map[string]any{}
	}
	return p
}

// Set is an immutable collection of presets, looked up by name.
//
// The zero value is not usable; build one with Builtin or Load.
type Set struct {
	byName map[string]Preset
}

// Get returns the preset with the given name.
//
// Lookup is case-insensitive and ignores surrounding space, because a preset
// name arrives from a query string typed by a human.
func (s *Set) Get(name string) (Preset, bool) {
	if s == nil {
		return Preset{}, false
	}
	p, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Preset{}, false
	}
	return p.Clone(), true
}

// Names returns every preset name, sorted, for listings and suggestions.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(s.byName))
}

// All returns every preset, sorted by name. The copies are safe to mutate.
func (s *Set) All() []Preset {
	if s == nil {
		return nil
	}
	out := make([]Preset, 0, len(s.byName))
	for _, name := range s.Names() {
		out = append(out, s.byName[name].Clone())
	}
	return out
}

// Len returns the number of presets in the set.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// newSet builds a set from presets, later entries overriding earlier ones.
func newSet(presets []Preset) *Set {
	byName := make(map[string]Preset, len(presets))
	for _, p := range presets {
		byName[p.Name] = p
	}
	return &Set{byName: byName}
}

// add installs a preset, overriding any existing entry of the same name.
// It reports false when the set is already at MaxPresets and the name is new.
func (s *Set) add(p Preset) bool {
	if _, exists := s.byName[p.Name]; !exists && len(s.byName) >= MaxPresets {
		return false
	}
	s.byName[p.Name] = p
	return true
}

// validate checks a preset decoded from disk against the same rules the
// built-ins satisfy by construction. It returns a message suitable for a
// boot-time warning, or the empty string when the preset is acceptable.
func validate(p Preset) string {
	if !ValidName(p.Name) {
		return "name is not a valid slug; expected lowercase letters, digits, - and _"
	}
	if len(p.Options) == 0 {
		return "options is empty; a preset that sets nothing has no effect"
	}
	if len(p.Options) > MaxOptions {
		return "too many options"
	}
	for k := range p.Options {
		if msg := validateKey(k); msg != "" {
			return msg
		}
	}
	return ""
}

// validateKey rejects option keys that could not be a request path.
//
// The authoritative check lives in the request layer, which knows the real
// field names; this only screens out shapes that are never a path, so that a
// malformed key is caught at boot rather than on the first request to use it.
func validateKey(k string) string {
	switch {
	case k == "":
		return "an option key is empty"
	case len(k) > maxKeyBytes:
		return "an option key is too long"
	case strings.TrimSpace(k) != k:
		return "option key " + k + " has leading or trailing space"
	case strings.HasPrefix(k, ".") || strings.HasSuffix(k, "."):
		return "option key " + k + " starts or ends with a dot"
	case strings.Contains(k, ".."):
		return "option key " + k + " contains an empty path segment"
	}
	return ""
}
