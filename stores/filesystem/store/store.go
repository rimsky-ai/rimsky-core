// Package store is the substrate-internal logic for the standard
// filesystem store-service. Per spec §8.1 / §7.7: direct mode only,
// concrete-paths only (no globs). Two claims on the same path conflict;
// two claims on different paths do not.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the in-process substrate. State held by Store itself is the
// configured root plus a small map keyed by claim_id used to identify
// orphaned state; lock state lives in postgres on the rimsky side and
// is not consulted here.
type Store struct {
	root string
	mu   sync.Mutex

	// claims maps claim_id → resolved path. Recorded in Open so the
	// substrate's sweep (if any) can identify orphans by claim_id (per
	// spec §7.8 obligation #2). Direct-mode filesystem has no
	// substrate state per claim, so this is observability-only — the
	// sweep responsibility is satisfied trivially.
	claims map[string]string
}

// New returns a Store rooted at the given directory.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("filesystem store: root must not be empty")
	}
	return &Store{
		root:   root,
		claims: make(map[string]string),
	}, nil
}

// Capabilities reports the substrate's advertised capabilities.
func (s *Store) Capabilities() store.Capabilities {
	return store.Capabilities{WriteSemantics: store.WriteSemanticsDirect}
}

// Open resolves the selector to a concrete path and returns the
// substrate's ClaimResult. Per §7.7 the standard filesystem store
// supports concrete paths only — globs are rejected.
//
// Selectors are canonicalized via filepath.Clean before use so that
// "foo", "./foo", "foo/.", and "foo/" all produce byte-equal regions
// (and resolve to the same on-disk path); the cleaned form is
// rejected if it tries to escape the configured root via ".." or
// becomes absolute. This is the load-bearing guard against path
// traversal — without it a selector like "../../etc/passwd" would
// resolve to a path outside s.root.
//
// ctx is accepted to keep the signature uniform across the three
// standard substrates (filesystem, postgres, stub); the filesystem
// substrate has no async work that consults it.
func (s *Store) Open(_ context.Context, claimID, selector string) (store.OpenOutcome, error) {
	raw := strings.TrimSpace(selector)
	if raw == "" {
		return store.OpenOutcome{}, errors.New("filesystem store: Open: empty selector")
	}
	if hasGlobMeta(raw) {
		return store.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q contains glob metacharacters; v3 standard filesystem supports concrete paths only",
			raw)
	}
	// Canonicalize first so "foo" and "./foo" produce identical region
	// bytes. Strip a single leading "./" before Clean because Clean
	// preserves "." as the result of "./" only when the input is "./"
	// alone — for "./foo" Clean returns "foo". The TrimPrefix is
	// belt-and-suspenders for inputs like "./".
	cleaned := filepath.Clean(strings.TrimPrefix(raw, "./"))
	if filepath.IsAbs(cleaned) {
		return store.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q is absolute; selectors must be relative paths under the configured root", raw)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return store.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q escapes the configured root", raw)
	}
	if cleaned == "." || cleaned == "" {
		return store.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolves to the root itself; selectors must name a concrete entry", raw)
	}

	// Region bytes: canonical JSON-encoded cleaned path string. Two
	// claims on the same logical path (e.g. "foo" and "./foo") produce
	// byte-equal regions (§7.7 obligation #5).
	regionBytes, err := json.Marshal(cleaned)
	if err != nil {
		return store.OpenOutcome{}, fmt.Errorf("filesystem store: marshal region: %w", err)
	}
	addrPath := filepath.Join(s.root, cleaned)
	// Defense-in-depth: confirm the resolved path is still under the
	// configured root. After the cleaned-relative-only checks above
	// this should always hold, but a Rel-based test catches any
	// edge case (Windows drive letters, symlinks at the OS layer, etc.)
	rel, relErr := filepath.Rel(s.root, addrPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return store.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolved to %q which escapes the configured root %q", raw, addrPath, s.root)
	}
	addrBytes, err := json.Marshal(addrPath)
	if err != nil {
		return store.OpenOutcome{}, fmt.Errorf("filesystem store: marshal address: %w", err)
	}

	s.mu.Lock()
	s.claims[claimID] = addrPath
	s.mu.Unlock()

	return store.OpenOutcome{
		Available: true,
		Result: store.ClaimResult{
			Address: json.RawMessage(addrBytes),
			Region:  json.RawMessage(regionBytes),
		},
	}, nil
}

// Commit is a no-op: direct mode has no staging. region/address are
// accepted to keep the signature uniform across the three standard
// substrates; the filesystem substrate ignores them.
func (s *Store) Commit(_ context.Context, claimID string, _ []byte, _ []byte) error {
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	return nil
}

// Abandon is a no-op: direct mode cannot undo writes. region/address
// are accepted for signature uniformity and ignored.
func (s *Store) Abandon(_ context.Context, claimID string, _ []byte, _ []byte) error {
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	return nil
}

// Release is a no-op: direct mode never registers substrate-side read
// state at Open. region/address are accepted for signature uniformity
// and ignored.
func (s *Store) Release(_ context.Context, claimID string, _ []byte, _ []byte) error {
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	return nil
}

// hasGlobMeta reports whether s contains any glob metacharacter
// recognised by stdlib path/filepath.Match.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}
