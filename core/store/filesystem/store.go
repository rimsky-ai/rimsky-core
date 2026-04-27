// Direct-mode filesystem store. Implements the five-verb store.Store
// interface (spec §11.5). The selector is a path-glob list (post-
// substitution); region_data is the canonical glob list serialised as
// JSON; address is the resolved absolute path on disk.
//
// Per spec §22 the filesystem store stays direct in v1 — staged_blocking
// via atomic-rename is a stretch goal not committed to. Commit and
// Abandon are honest no-ops; Delete invokes os.RemoveAll on the resolved
// region path.

package filesystem

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the direct-mode filesystem store. State held by Store itself
// is immutable after construction (name + root). Lock state lives in
// postgres; the store does not cache or persist any acquisition data.
//
// Thread-safety: all methods are safe for concurrent use because they
// touch only immutable state and the live filesystem (whose concurrency
// model is the OS's).
type Store struct {
	name string
	root string
}

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Name returns the operator-configured store name (matches stores.<name>
// in YAML).
func (s *Store) Name() string { return s.name }

// Kind returns the canonical store kind, "filesystem".
func (s *Store) Kind() string { return "filesystem" }

// Root returns the root directory path the store was configured with.
// Exposed primarily for tests.
func (s *Store) Root() string { return s.root }

// Capabilities reports the direct-mode filesystem store's
// write_semantics. Always WriteSemanticsDirect for v1.
func (s *Store) Capabilities() store.Capabilities {
	return store.Capabilities{WriteSemantics: store.WriteSemanticsDirect}
}

// RegionsConflict delegates to the package-level pure helper. Inputs are
// expected to be JSON-encoded []string of path globs (the store's
// region grammar). Inputs of an unexpected shape are treated as
// conflicting — the supervisor must never silently admit an acquisition
// whose region we cannot interpret.
//
// @blessed-invariant 14: pure; no side effects; deterministic on inputs.
func (s *Store) RegionsConflict(a, b []byte) bool {
	ga, errA := decodeGlobs(a)
	gb, errB := decodeGlobs(b)
	if errA != nil || errB != nil {
		return true
	}
	return RegionsConflict(ga, gb)
}

// UnmarshalRegion verifies the bytes are a JSON []string and returns
// them as canonical bytes for use by RegionsConflict.
//
// @blessed-invariant 14: pure.
func (s *Store) UnmarshalRegion(raw []byte) ([]byte, error) {
	if _, err := decodeGlobs(raw); err != nil {
		return nil, fmt.Errorf("filesystem store %q: unmarshal region: %w", s.name, err)
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

// Open resolves the selector to a concrete directory path under the
// store's root and returns a ClaimResult whose Address is the resolved
// path and whose Region is the canonical glob list.
//
// The selector grammar accepts a single string (one glob), a JSON array
// of strings (a glob list), or comma-separated globs in a single string.
// All forms resolve to a single Address — the store's root joined to
// the first glob's fixed prefix. Substrate-side state: none for direct
// mode.
func (s *Store) Open(_ context.Context, spec store.ClaimSpec) (store.ClaimResult, error) {
	globs := parseSelector(spec.Selector)
	if len(globs) == 0 {
		return store.ClaimResult{}, fmt.Errorf("filesystem store %q: Open: empty selector", s.name)
	}
	regionBytes, err := json.Marshal(globs)
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("filesystem store %q: Open: marshal region: %w", s.name, err)
	}
	// Resolve the address: root joined to the first glob's fixed prefix.
	// Direct mode hands the executor the live path; reads/writes happen
	// in place.
	prefix := fixedPrefix(globs[0])
	addrPath := filepath.Join(s.root, prefix)
	addrBytes, err := json.Marshal(addrPath)
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("filesystem store %q: Open: marshal address: %w", s.name, err)
	}
	return store.ClaimResult{
		Address: json.RawMessage(addrBytes),
		Region:  json.RawMessage(regionBytes),
	}, nil
}

// Commit is a substrate no-op for direct mode (writes already on disk
// per spec §6.2 / §8.4). policyOverride is ignored — pick policies are
// not configurable on the filesystem store.
func (s *Store) Commit(_ context.Context, _ []byte, _ []byte, _ string) error {
	return nil
}

// Abandon is degenerate for direct mode (cannot undo direct writes per
// spec §6.2). Logs an honest no-op via the store-author guide; returns
// nil to allow the supervisor to proceed with terminal cleanup.
func (s *Store) Abandon(_ context.Context, _ []byte, _ []byte, _ string) error {
	return nil
}

// Delete removes the live region. Resolves the region's first glob's
// fixed prefix to an absolute path and runs os.RemoveAll. Returns any
// I/O error.
func (s *Store) Delete(_ context.Context, region []byte) error {
	globs, err := decodeGlobs(region)
	if err != nil {
		return fmt.Errorf("filesystem store %q: Delete: decode region: %w", s.name, err)
	}
	if len(globs) == 0 {
		return nil
	}
	target := filepath.Join(s.root, fixedPrefix(globs[0]))
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("filesystem store %q: Delete %q: %w", s.name, target, err)
	}
	return nil
}

// Release is a no-op: direct mode never registers substrate-side read
// state at Open.
func (s *Store) Release(_ context.Context, _ []byte, _ []byte) error {
	return nil
}

// decodeGlobs parses region bytes into []string. Accepts a JSON array
// of strings, a JSON single string, or returns an error on any other
// shape.
func decodeGlobs(b []byte) ([]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		return arr, nil
	}
	var single string
	if err := json.Unmarshal(b, &single); err != nil {
		return nil, err
	}
	return []string{single}, nil
}

// parseSelector accepts the graph-author's selector (a single string)
// and splits it into globs. Single-form selectors return [s]; comma-
// separated forms split on commas (with whitespace trimmed). Empty
// returns nil.
func parseSelector(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !strings.Contains(s, ",") {
		return []string{s}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
