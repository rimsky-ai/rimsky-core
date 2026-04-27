// Package filesystem implements the direct-mode filesystem store
// described in spec §22 / §11.1. Direct mode means the executor is
// handed an absolute path to the live region; reads and writes happen
// in-place. There is no staging, no atomic-swap. The store is a thin
// shell around the live directory: lock state lives entirely in
// postgres (rimsky_lock_holders), and Commit / Abandon are honest
// no-ops; Delete invokes os.RemoveAll.
//
// Region values are []string of path globs. A region "covers" any path
// that matches any of its globs. Glob semantics extend stdlib
// path/filepath.Match with a manual "**" expansion: a glob containing
// "**" matches any path under its prefix.
//
// This package imports core/store/ and stdlib only.
package filesystem

import (
	"fmt"

	"github.com/fallguy/rimsky/core/store"
)

// Factory builds direct-mode filesystem stores from per-store YAML
// config. Registered with a *store.Registry under kind "filesystem".
type Factory struct{}

// Kind returns the canonical store kind, "filesystem".
func (Factory) Kind() string { return "filesystem" }

// MaxWriteSemantics returns WriteSemanticsDirect — the v1 filesystem
// store does not support staging. Operators cannot upgrade past this
// (per spec §8.1).
func (Factory) MaxWriteSemantics() store.WriteSemantics { return store.WriteSemanticsDirect }

// Build constructs a *Store from operator-supplied config. Required keys:
//
//   - root: absolute directory path; the store's namespace is rooted here.
//
// Optional keys (recognized but not enforced beyond shape):
//
//   - write_semantics (string): must be "direct" if set; the registry's
//     ceiling check rejects upgrades. Defaults to "direct".
//
// Returns an error if root is missing, the wrong type, or empty.
func (Factory) Build(name string, cfg map[string]any) (store.Store, error) {
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
