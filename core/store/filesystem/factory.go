// Package filesystem implements the direct-mode filesystem store described
// in the stores-redesign spec §6.1.
//
// Direct-mode means the executor is handed an absolute path to the live
// region; reads and writes happen in-place. There is no sidecar, no
// staging, no atomic-swap. The store is a thin shell around the live
// directory: lock state lives entirely in postgres
// (`rimsky_lock_holders`), and Commit / ReleaseLock are no-ops.
//
// Region values are []string of path globs. A region "covers" any path
// that matches any of its globs. Glob semantics extend stdlib
// `path/filepath.Match` with a manual "**" expansion: a glob containing
// "**" matches any path under its prefix.
//
// This package imports `core/store/` and stdlib only (per spec §8.1).
package filesystem

import (
	"fmt"

	"github.com/fallguy/rimsky/core/store"
)

// Factory builds direct-mode filesystem stores from per-store YAML config.
// Registered with a *store.Registry under kind "filesystem".
type Factory struct{}

// Kind returns the canonical store kind, "filesystem".
func (Factory) Kind() string { return "filesystem" }

// Build constructs a *Store from operator-supplied config. Required keys:
//
//   - mode: must be "direct" (sidecar / versioned are post-v1).
//   - root: absolute directory path; the store's namespace is rooted here.
//
// Returns an error if either is missing, the wrong type, or invalid.
func (Factory) Build(name string, cfg map[string]any) (store.Store, error) {
	modeRaw, ok := cfg["mode"]
	if !ok {
		return nil, fmt.Errorf("filesystem store %q: missing 'mode' field", name)
	}
	mode, ok := modeRaw.(string)
	if !ok {
		return nil, fmt.Errorf("filesystem store %q: 'mode' must be string, got %T", name, modeRaw)
	}
	if mode != "direct" {
		return nil, fmt.Errorf("filesystem store %q: only mode=direct is supported in v1, got %q", name, mode)
	}

	rootRaw, ok := cfg["root"]
	if !ok {
		return nil, fmt.Errorf("filesystem store %q: missing 'root' field", name)
	}
	root, ok := rootRaw.(string)
	if !ok {
		return nil, fmt.Errorf("filesystem store %q: 'root' must be string, got %T", name, rootRaw)
	}
	if root == "" {
		return nil, fmt.Errorf("filesystem store %q: 'root' must not be empty", name)
	}

	return &Store{name: name, root: root}, nil
}
