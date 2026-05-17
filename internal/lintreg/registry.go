// Package lintreg is a process-wide registry of analyzer factories. Analyzers
// must be linked into the binary; their YAML import path is the registry key.
package lintreg

import (
	"fmt"
	"sort"
	"sync"

	"golang.org/x/tools/go/analysis"
)

// Factory builds a configured *analysis.Analyzer from a YAML config map.
// Returning a nil analyzer means "no-op for this entry"; returning an error
// surfaces a config problem to the caller of `lint`.
type Factory func(cfg map[string]any) (*analysis.Analyzer, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register links a Factory to an import path. Panics on duplicate keys.
func Register(importPath string, f Factory) {
	if importPath == "" {
		panic("lintreg: empty import path")
	}
	if f == nil {
		panic("lintreg: nil factory for " + importPath)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[importPath]; exists {
		panic("lintreg: duplicate registration: " + importPath)
	}
	registry[importPath] = f
}

// Get returns the factory for importPath, or an error if unregistered.
func Get(importPath string) (Factory, error) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := registry[importPath]
	if !ok {
		return nil, fmt.Errorf("no analyzer registered for %q (known: %v)", importPath, listKeysLocked())
	}
	return f, nil
}

// Keys returns every registered import path, sorted.
func Keys() []string {
	mu.RLock()
	defer mu.RUnlock()
	return listKeysLocked()
}

func listKeysLocked() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Reset clears the registry. Test-only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Factory{}
}
