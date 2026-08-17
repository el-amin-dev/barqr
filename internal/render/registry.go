package render

import (
	"fmt"
	"sort"
	"sync"
)

// Registries for renderers and for the two shape families. All three are
// populated from init functions and read-only afterwards.
var (
	renderers = struct {
		sync.RWMutex
		byName map[string]Renderer
	}{byName: make(map[string]Renderer)}

	modulePainters = struct {
		sync.RWMutex
		byName map[string]ModulePainter
	}{byName: make(map[string]ModulePainter)}

	eyePainters = struct {
		sync.RWMutex
		byName map[string]EyePainter
	}{byName: make(map[string]EyePainter)}
)

// Register adds a renderer under its own name. It panics on a duplicate.
func Register(r Renderer) {
	renderers.Lock()
	defer renderers.Unlock()

	if _, dup := renderers.byName[r.Name()]; dup {
		panic(fmt.Sprintf("render: renderer %q registered twice", r.Name()))
	}
	renderers.byName[r.Name()] = r
}

// Get returns the renderer registered under name.
func Get(name string) (Renderer, error) {
	renderers.RLock()
	defer renderers.RUnlock()

	r, ok := renderers.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRenderer, name)
	}
	return r, nil
}

// RegisterModuleShape adds a module painter. It panics on a duplicate.
func RegisterModuleShape(p ModulePainter) {
	modulePainters.Lock()
	defer modulePainters.Unlock()

	if _, dup := modulePainters.byName[p.Name()]; dup {
		panic(fmt.Sprintf("render: module shape %q registered twice", p.Name()))
	}
	modulePainters.byName[p.Name()] = p
}

// ModuleShape returns the painter for a module shape.
func ModuleShape(name string) (ModulePainter, error) {
	modulePainters.RLock()
	defer modulePainters.RUnlock()

	p, ok := modulePainters.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: module shape %q", ErrUnknownShape, name)
	}
	return p, nil
}

// ModuleShapes lists every registered module shape, sorted.
func ModuleShapes() []string {
	modulePainters.RLock()
	defer modulePainters.RUnlock()
	return sortedKeys(modulePainters.byName)
}

// RegisterEyeShape adds an eye painter. It panics on a duplicate.
func RegisterEyeShape(p EyePainter) {
	eyePainters.Lock()
	defer eyePainters.Unlock()

	if _, dup := eyePainters.byName[p.Name()]; dup {
		panic(fmt.Sprintf("render: eye shape %q registered twice", p.Name()))
	}
	eyePainters.byName[p.Name()] = p
}

// EyeShape returns the painter for an eye shape.
func EyeShape(name string) (EyePainter, error) {
	eyePainters.RLock()
	defer eyePainters.RUnlock()

	p, ok := eyePainters.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: eye shape %q", ErrUnknownShape, name)
	}
	return p, nil
}

// EyeShapes lists every registered eye shape, sorted.
func EyeShapes() []string {
	eyePainters.RLock()
	defer eyePainters.RUnlock()
	return sortedKeys(eyePainters.byName)
}

// sortedKeys returns the map's keys in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
