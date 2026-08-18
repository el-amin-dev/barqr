package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/el-amin-dev/barqr/internal/config"
)

// testConfig loads the configuration a bare instance runs with.
func testConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg, _, err := config.Load([]string{"BARQR_AUTH_MODE=open"})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// testDocument renders the OpenAPI document the way a caller receives it —
// through a JSON round trip — because that is what turns every number into a
// float64 and is where a comparison against a Go value goes wrong if it is
// done naively.
func testDocument(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()

	s := &Server{cfg: cfg}
	raw, err := json.Marshal(s.openAPIDocument())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

// schemaAt walks the Request body schema down a dotted path and returns the
// leaf property.
func schemaAt(t *testing.T, doc map[string]any, path string) map[string]any {
	t.Helper()

	node, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)["Request"].(map[string]any)
	if !ok {
		t.Fatal("no Request schema in the document")
	}
	for _, part := range splitPath(path) {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return nil
		}
		child, ok := props[part].(map[string]any)
		if !ok {
			return nil
		}
		node = child
	}
	return node
}

func splitPath(path string) []string {
	var out []string
	start := 0
	for i := range path {
		if path[i] == '.' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}

// TestOpenAPIPublishesEveryDefaultFromTheCode is the fix for what a user
// reported: they read the spec, saw style.hri typed only as a boolean, assumed
// the default was off, and got a caption they had not asked for.
//
// It asserts the direction that matters to a caller — everything the code
// defaults, the document states.
func TestOpenAPIPublishesEveryDefaultFromTheCode(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	doc := testDocument(t, cfg)

	for path, want := range Defaults(cfg) {
		node := schemaAt(t, doc, path)
		if node == nil {
			t.Errorf("%s: not described in the Request schema at all", path)
			continue
		}
		got, has := node["default"]
		if !has {
			t.Errorf("%s: the code defaults it to %v, the document says nothing",
				path, want)
			continue
		}
		if defaultString(got) != defaultString(want) {
			t.Errorf("%s: document says %v, the code applies %v",
				path, got, want)
		}
	}
}

// TestOpenAPIDefaultsAreNeverHandTyped is the other direction: every default
// the document states must come from the map, so nobody can type one into the
// schema literal and have it quietly rot.
func TestOpenAPIDefaultsAreNeverHandTyped(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	doc := testDocument(t, cfg)
	known := Defaults(cfg)

	req, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)["Request"].(map[string]any)
	if !ok {
		t.Fatal("no Request schema")
	}

	var walk func(node map[string]any, prefix string)
	walk = func(node map[string]any, prefix string) {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if got, has := child["default"]; has {
				want, known := known[path]
				switch {
				case !known:
					t.Errorf("%s: the document states a default of %v that no "+
						"code path produces; defaults belong in Defaults()", path, got)
				case defaultString(got) != defaultString(want):
					t.Errorf("%s: document %v, code %v", path, got, want)
				}
			}
			walk(child, path)
		}
	}
	walk(req, "")
}

// TestOpenAPIDescribesEverySettablePath is the generalised guard.
//
// pathIndex is reflected off the Request struct, so it is the complete list of
// what a caller can set. Every one of those must appear in the document, and
// must either have a real default or be listed as having a prose one. That is
// what makes it impossible to add a field and forget the spec — the failure
// that let style.hri go undocumented in the first place.
func TestOpenAPIDescribesEverySettablePath(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	doc := testDocument(t, cfg)
	defaults, prose := Defaults(cfg), defaultsProse()

	// Paths that genuinely have no default: unset means the feature is off or
	// absent, and there is nothing to state.
	noDefault := map[string]bool{
		"type": true, "data": true, "symbology": true, "renderer": true,
		"style.eye_fg": true, "style.gradient": true, "style.logo": true,
		"style.caption": true, "style.frame": true, "meta.filename": true,
		"output.size": true,
	}

	for path := range pathIndex {
		if schemaAt(t, doc, path) == nil {
			t.Errorf("%s: settable on Request but absent from the document", path)
			continue
		}
		_, hasValue := defaults[path]
		_, hasProse := prose[path]
		if !hasValue && !hasProse && !noDefault[path] {
			t.Errorf("%s: settable but classified nowhere — add it to "+
				"Defaults, defaultsProse, or the noDefault set", path)
		}
		if hasValue && hasProse {
			t.Errorf("%s: has both a value default and a prose one", path)
		}
	}
}

// TestOpenAPIDefaultsFollowTheConfiguration pins the property ADR-013 rests
// on: the document describes THIS instance, not a snapshot taken elsewhere.
func TestOpenAPIDefaultsFollowTheConfiguration(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.DefaultFormat = "svg"
	cfg.DefaultECC = "H"

	doc := testDocument(t, cfg)

	if got := schemaAt(t, doc, "output.format")["default"]; got != "svg" {
		t.Errorf("output.format default = %v, want svg from the configuration", got)
	}
	if got := schemaAt(t, doc, "encode.ecc")["default"]; got != "H" {
		t.Errorf("encode.ecc default = %v, want H from the configuration", got)
	}
}
