// Package store is the store-internal logic for the standard
// filesystem store-service. Per spec §8.1 / §7.7: direct mode only,
// concrete-paths only (no globs). Two claims on the same path conflict;
// two claims on different paths do not.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	corestore "github.com/fallguy/rimsky/core/store"
)

// PickPolicy is one configured pick policy. Store-internal.
//
// Auto-discovery is the only insertion mechanism: the sync step
// reconciles <store-root>/<Root>/* against the available/ sentinel
// set. Queue-vs-ring vs single-shot drain is emergent from
// OnCommitDefault / OnGiveUpDefault.
type PickPolicy struct {
	Root              string         // relative path under store root
	FolderPattern     *regexp.Regexp // nil means "no extra filter beyond skip-leading-dot"
	OnCommitDefault   string         // "release_to_back" | "release_to_head" | "delete"
	OnGiveUpDefault   string         // same vocabulary
	VisibilityTimeout time.Duration
	SyncStrategy      string // "on_open" | "on_sweep"

	// syncMu serializes runSync for this policy so concurrent Open
	// callers don't race on the (read-available, read-in_progress,
	// O_CREAT|O_EXCL) sequence — without it, a sync that began before
	// another goroutine's rename can re-create a sentinel for a folder
	// that's currently in_progress, allowing the same folder to be
	// picked twice. The actual claim (rename) remains lockless and
	// relies on POSIX rename atomicity; only the reconciliation step
	// is serialized.
	syncMu sync.Mutex
}

// Config is the store-internal config struct. cmd/main.go and
// testfixture/ both build it. SweepInterval is intentionally NOT a
// field here — sweep cadence is owned by the server package and
// passed to RunSweep directly. Keeping a single source of truth.
type Config struct {
	Root         string
	PickPolicies map[string]*PickPolicy
}

// Store is the in-process store implementation. State held by Store
// itself is the configured root plus a small map keyed by claim_id used
// to identify orphaned state; lock state lives in postgres on the rimsky
// side and is not consulted here.
type Store struct {
	root         string
	pickPolicies map[string]*PickPolicy
	mu           sync.Mutex

	// claims maps claim_id → resolved path. Recorded in Open so the
	// store's sweep (if any) can identify orphans by claim_id (per
	// spec §7.8 obligation #2). Direct-mode filesystem has no
	// store-side state per claim, so this is observability-only — the
	// sweep responsibility is satisfied trivially.
	claims map[string]string

	// ledger is the per-claim history surfaced via the StoreObservability
	// protocol. Bounded; nil when observability isn't wired up. Per
	// spec §3.2: stores choose what to expose.
	ledger *ClaimLedger
}

// Ledger returns the in-memory claim ledger. Nil-safe.
func (s *Store) Ledger() *ClaimLedger { return s.ledger }

// New returns a Store rooted at the given config.
func New(cfg Config) (*Store, error) {
	if cfg.Root == "" {
		return nil, errors.New("filesystem store: root must not be empty")
	}
	for selector, pp := range cfg.PickPolicies {
		if err := validatePickPolicy(cfg.Root, selector, pp); err != nil {
			return nil, fmt.Errorf("filesystem store: pick_policies[%q]: %w", selector, err)
		}
		// Idempotent state-directory creation.
		dir := filepath.Join(cfg.Root, ".fs-store", trimAtPrefix(selector))
		for _, sub := range []string{"available", "in_progress"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				return nil, fmt.Errorf("filesystem store: mkdir %s: %w", filepath.Join(dir, sub), err)
			}
		}
	}
	return &Store{
		root:         cfg.Root,
		pickPolicies: cfg.PickPolicies,
		claims:       make(map[string]string),
		ledger:       NewClaimLedger(1024),
	}, nil
}

// trimAtPrefix returns the policy directory name corresponding to a selector.
// "@docs-ring" → "docs-ring"; selectors without a leading "@" are used verbatim.
func trimAtPrefix(selector string) string {
	if strings.HasPrefix(selector, "@") {
		return selector[1:]
	}
	return selector
}

// Capabilities reports the store's advertised capabilities.
func (s *Store) Capabilities() corestore.Capabilities {
	return corestore.Capabilities{WriteSemantics: corestore.WriteSemanticsDirect}
}

// Open dispatches by selector: pick-policy keys (configured map keys —
// conventionally `@policy-name`) hit openPickPolicy; everything else
// falls through to openRegional.
//
// ctx is accepted to keep the signature uniform across the three
// standard stores (filesystem, postgres, stub); the filesystem
// store has no async work that consults it.
func (s *Store) Open(_ context.Context, claimID, selector string) (corestore.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		// Pick-policy selectors are a configured map-key match — they
		// intentionally bypass openRegional's glob-metacharacter
		// rejection. Operators choose the selector key (convention:
		// `@policy-name`); a key containing `*`/`?`/`[` is operator
		// misconfiguration but doesn't violate v3's "concrete paths
		// only" rule, which governs the selector-as-path
		// interpretation that openRegional implements.
		return s.openPickPolicy(claimID, selector, pp)
	}
	return s.openRegional(claimID, selector)
}

// openRegional resolves the selector as a concrete path under the
// configured root. Per §7.7 the standard filesystem store supports
// concrete paths only — globs are rejected.
//
// Selectors are canonicalized via filepath.Clean before use so that
// "foo", "./foo", "foo/.", and "foo/" all produce byte-equal regions
// (and resolve to the same on-disk path); the cleaned form is
// rejected if it tries to escape the configured root via ".." or
// becomes absolute. This is the load-bearing guard against path
// traversal — without it a selector like "../../etc/passwd" would
// resolve to a path outside s.root.
func (s *Store) openRegional(claimID, selector string) (corestore.OpenOutcome, error) {
	raw := strings.TrimSpace(selector)
	if raw == "" {
		return corestore.OpenOutcome{}, errors.New("filesystem store: Open: empty selector")
	}
	if hasGlobMeta(raw) {
		return corestore.OpenOutcome{}, fmt.Errorf(
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
		return corestore.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q is absolute; selectors must be relative paths under the configured root", raw)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return corestore.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q escapes the configured root", raw)
	}
	if cleaned == "." || cleaned == "" {
		return corestore.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolves to the root itself; selectors must name a concrete entry", raw)
	}

	// Region bytes: canonical JSON-encoded cleaned path string. Two
	// claims on the same logical path (e.g. "foo" and "./foo") produce
	// byte-equal regions (§7.7 obligation #5).
	regionBytes, err := json.Marshal(cleaned)
	if err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: marshal region: %w", err)
	}
	addrPath := filepath.Join(s.root, cleaned)
	// Defense-in-depth: confirm the resolved path is still under the
	// configured root. After the cleaned-relative-only checks above
	// this should always hold, but a Rel-based test catches any
	// edge case (Windows drive letters, symlinks at the OS layer, etc.)
	rel, relErr := filepath.Rel(s.root, addrPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return corestore.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolved to %q which escapes the configured root %q", raw, addrPath, s.root)
	}
	addrBytes, err := json.Marshal(addrPath)
	if err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: marshal address: %w", err)
	}

	s.mu.Lock()
	s.claims[claimID] = addrPath
	s.mu.Unlock()

	s.ledger.RecordOpen(claimID, raw, addrBytes, regionBytes)

	return corestore.OpenOutcome{
		Available: true,
		Result: corestore.ClaimResult{
			Address: json.RawMessage(addrBytes),
			Region:  json.RawMessage(regionBytes),
		},
	}, nil
}

// Commit delegates to the pick-policy action handler when the claim is
// in pick-policy state; otherwise no-op (direct mode has no staging).
// region/address are accepted to keep the signature uniform across the
// three standard stores; the filesystem store ignores them on the
// regional path.
//
// The ledger records the terminal event only after the store-side
// action succeeds. Failures surface as non-terminal
// `claim_commit_failed` events; the in-memory claim entry stays put so
// the supervisor can retry without losing track. The s.claims delete
// happens up-front because the per-claim path is needed only for
// findByClaimID, and a failed action keeps the on-disk sentinel intact
// for the next attempt.
func (s *Store) Commit(_ context.Context, claimID string, _ []byte, _ []byte) error {
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnCommitDefault); err != nil {
			s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_committed", nil)
	return nil
}

// Abandon delegates to the pick-policy action handler when the claim is
// in pick-policy state; otherwise no-op (direct mode cannot undo
// writes). Ledger records terminal only on success; failures surface
// as a non-terminal `claim_abandon_failed` event.
func (s *Store) Abandon(_ context.Context, claimID string, _ []byte, _ []byte) error {
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnGiveUpDefault); err != nil {
			s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

// Release is a no-op: direct mode never registers store-side read
// state at Open. region/address are accepted for signature uniformity
// and ignored.
func (s *Store) Release(_ context.Context, claimID string, _ []byte, _ []byte) error {
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

// hasGlobMeta reports whether s contains any glob metacharacter
// recognised by stdlib path/filepath.Match.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// validatePickPolicy validates the operator-supplied pick policy against
// the store root: the Root field must be a relative subpath that exists
// as a readable + writable directory; action defaults must be one of
// the three known actions; visibility_timeout must be positive;
// sync_strategy defaults to "on_open" when empty.
//
// Symlink assumption: the lexical Rel-based containment check below
// confirms `<storeRoot>/<pp.Root>` is lexically under storeRoot, but
// `os.Stat` follows symlinks. If the operator places a symlink under
// the store root that points outside, runtime operations will follow
// it. Per the spec's "POSIX local filesystems" assumption (and the
// operator-guide §8.4.2 deployment guidance) the store root must not
// contain symlinks pointing outside itself; this is an operator-fault
// constraint, not enforced here. Same posture as openRegional.
func validatePickPolicy(storeRoot, selector string, pp *PickPolicy) error {
	if pp == nil {
		return errors.New("policy is nil")
	}
	if pp.Root == "" {
		return errors.New("root: required")
	}
	if filepath.IsAbs(pp.Root) {
		return fmt.Errorf("root: %q is absolute; must be relative to store root", pp.Root)
	}
	cleaned := filepath.Clean(pp.Root)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("root: %q escapes the store root", pp.Root)
	}
	absPath := filepath.Join(storeRoot, cleaned)
	// Defense-in-depth containment check: ensure the joined path
	// actually lives under storeRoot, catching symlink edge cases
	// (mirrors openRegional's existing filepath.Rel check).
	rel, relErr := filepath.Rel(storeRoot, absPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("root: %q resolves to %q which escapes the store root", pp.Root, absPath)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("root: stat %s: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root: %s is not a directory", absPath)
	}
	// Readability probe.
	if _, err := os.ReadDir(absPath); err != nil {
		return fmt.Errorf("root: %s not readable: %w", absPath, err)
	}
	// Writability probe via temp-file create + remove.
	probe, err := os.CreateTemp(absPath, ".rimsky-fs-store-probe-*")
	if err != nil {
		return fmt.Errorf("root: %s not writable: %w", absPath, err)
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	switch pp.OnCommitDefault {
	case "release_to_back", "release_to_head", "delete":
	default:
		return fmt.Errorf("on_commit_default: must be release_to_back|release_to_head|delete, got %q", pp.OnCommitDefault)
	}
	switch pp.OnGiveUpDefault {
	case "release_to_back", "release_to_head", "delete":
	default:
		return fmt.Errorf("on_give_up_default: must be release_to_back|release_to_head|delete, got %q", pp.OnGiveUpDefault)
	}
	if pp.VisibilityTimeout <= 0 {
		return errors.New("visibility_timeout_seconds: must be > 0")
	}
	switch pp.SyncStrategy {
	case "", "on_open", "on_sweep":
		if pp.SyncStrategy == "" {
			pp.SyncStrategy = "on_open"
		}
	default:
		return fmt.Errorf("sync_strategy: must be on_open|on_sweep, got %q", pp.SyncStrategy)
	}
	_ = selector // selector is interpolated by the caller for error context; not validated here.
	return nil
}
