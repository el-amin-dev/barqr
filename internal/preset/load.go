package preset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// jsonExt is the only extension Load reads. Everything else in the directory —
// a README, an editor swap file, a half-written .json.tmp — is ignored in
// silence, because an operator keeping notes beside their presets is not an
// error worth a boot warning.
const jsonExt = ".json"

// Load returns the built-in presets overlaid with the JSON files in dir.
//
// A user preset whose name matches a built-in **overrides** it: the file wins,
// completely, and the built-in's options are not merged underneath. That is
// the point — an operator who wants "print" to mean 600 dpi in their shop
// should not have to fight the value compiled into the binary.
//
// An empty dir means built-ins only, which is the default configuration.
//
// A malformed, oversized or badly named file is a warning, not a failure: one
// bad file in a mounted directory must not stop the service booting. The
// warnings are returned so the caller can log them at boot; they name the file
// but never its path. An error is returned only when the directory itself
// cannot be used, which is a configuration fault the operator must see.
//
// dir is operator-controlled input and is treated as untrusted. See the guards
// inline below: extension, file type, containment, name shape, size, and count.
func Load(dir string) (*Set, []string, error) {
	set := Builtin()
	if strings.TrimSpace(dir) == "" {
		return set, nil, nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnreadable, cause(err))
	}
	if !info.IsDir() {
		return nil, nil, ErrNotADirectory
	}

	// os.Root confines every subsequent open to this directory at the syscall
	// level: a path element that resolves outside it — including through a
	// symlink planted after this listing — is refused by the kernel-side check
	// rather than by string comparison, which is the only version of this
	// guard that has no TOCTOU window.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnreadable, cause(err))
	}
	defer func() { _ = root.Close() }()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnreadable, cause(err))
	}

	var warnings []string
	for _, entry := range entries {
		name := entry.Name()

		// Guard 1: extension. Only .json is a preset; anything else is not our
		// file and is skipped without comment.
		if !strings.EqualFold(filepath.Ext(name), jsonExt) {
			continue
		}

		// Guard 2: file type. A regular file only — this rejects directories,
		// devices, sockets, and every symlink, whether or not it escapes. A
		// symlinked preset is never legitimate here and allowing it would mean
		// reasoning about where it points.
		if !entry.Type().IsRegular() {
			warnings = append(warnings, warn(name, "not a regular file"))
			continue
		}

		// Guard 3: containment. ReadDir yields base names, so this cannot fail
		// today; it is kept as a tripwire so that a future change which feeds
		// Load a caller-supplied name cannot quietly become a traversal.
		if !within(dir, name) {
			warnings = append(warnings, warn(name, "resolves outside the presets directory"))
			continue
		}

		// Guard 4: name shape. The stem becomes the preset name, which is
		// echoed in JSON and used as a map key, so it must be a plain slug.
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !ValidName(stem) {
			warnings = append(warnings, warn(name,
				"file name is not a valid preset slug; expected lowercase letters, "+
					"digits, - and _"))
			continue
		}

		// Guard 5: size. Checked from the directory entry first so an enormous
		// file is never opened, and again while reading because the file may
		// have grown between the two.
		if fi, err := entry.Info(); err == nil && fi.Size() > MaxFileBytes {
			warnings = append(warnings, warn(name,
				fmt.Sprintf("larger than the %d-byte limit for a preset file", MaxFileBytes)))
			continue
		}

		p, msg := readPreset(root, name, stem)
		if msg != "" {
			warnings = append(warnings, warn(name, msg))
			continue
		}

		// Guard 6: count. Overriding a built-in does not consume a slot, so a
		// directory that only redefines the shipped presets always loads.
		if !set.add(p) {
			warnings = append(warnings, warn(name,
				fmt.Sprintf("skipped: the %d-preset limit is already reached", MaxPresets)))
			// Every later entry would hit the same wall; one warning is the
			// useful signal, a hundred is noise in the boot log.
			break
		}
	}

	return set, warnings, nil
}

// readPreset reads and decodes one file. It returns a warning message instead
// of an error because every failure here is per-file and non-fatal.
func readPreset(root *os.Root, name, stem string) (Preset, string) {
	f, err := root.Open(name)
	if err != nil {
		return Preset{}, "could not be opened: " + cause(err)
	}
	defer func() { _ = f.Close() }()

	// One byte over the limit so an oversized file is detected rather than
	// silently truncated into JSON that happens to parse.
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Preset{}, "could not be read: " + cause(err)
	}
	if len(data) > MaxFileBytes {
		return Preset{}, fmt.Sprintf("larger than the %d-byte limit for a preset file", MaxFileBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	// An unknown field is almost always a typo — "option" for "options" — and
	// silently ignoring it yields a preset that does nothing with no clue why.
	dec.DisallowUnknownFields()

	var p Preset
	if err := dec.Decode(&p); err != nil {
		return Preset{}, "is not a valid preset document: " + jsonCause(err)
	}
	if dec.More() {
		return Preset{}, "contains more than one JSON value"
	}

	// The file stem is authoritative: it is what the caller types and what the
	// filesystem enforces uniqueness on. A "name" in the body is optional, but
	// if present it must agree, or Get would answer to a name nobody can see.
	switch {
	case p.Name == "":
		p.Name = stem
	case p.Name != stem:
		return Preset{}, fmt.Sprintf("declares name %q but the file stem is %q", p.Name, stem)
	}

	if msg := validate(p); msg != "" {
		return Preset{}, msg
	}
	return p, ""
}

// within reports whether joining name onto dir stays inside dir.
func within(dir, name string) bool {
	base := filepath.Clean(dir)
	full := filepath.Clean(filepath.Join(base, name))
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// warn formats a per-file warning. It carries the file's base name only:
// boot logs are shipped off the host, and the presets path may name a
// customer, a mount point, or a home directory.
func warn(name, msg string) string {
	return fmt.Sprintf("preset file %q %s", name, msg)
}

// cause reduces a filesystem error to its underlying reason, dropping the
// operation and path os wraps around it so no absolute path reaches a log.
func cause(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// jsonCause turns a decoder error into something an operator can act on
// without echoing the offending bytes, which may be someone's data.
func jsonCause(err error) string {
	// A truncated file — a write interrupted by a deploy, a half-copied
	// mount — hits EOF rather than a syntax error, and "unexpected end of
	// JSON" is the one message that tells the operator to look at the writer.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "is truncated: the JSON ends mid-document"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("malformed JSON at byte %d", syntaxErr.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf("field %s expects %s", typeErr.Field, typeErr.Type)
	}
	if strings.Contains(err.Error(), "unknown field") {
		return "contains an unknown field; presets accept name, description and options"
	}
	return "could not be decoded"
}
