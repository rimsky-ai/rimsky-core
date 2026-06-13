// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// PickPolicy is one configured pick policy. Store-internal.
//
// Auto-discovery is the only insertion mechanism: the sync step
// reconciles <store-root>/<Root>/* against the available/ sentinel
// set. Queue-vs-ring vs single-shot drain is emergent from
// OnCommit / OnGiveUp action choice.
type PickPolicy struct {
	Root              string         // relative path under store root
	FolderPattern     *regexp.Regexp // nil means "no extra filter beyond skip-leading-dot"
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
	SyncStrategy      string // "on_open" | "on_drain" | "explicit" | "never"

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

	// ledger is the per-claim history surfaced via the ClaimProducerObservability
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
		res := validatePickPolicy(cfg.Root, selector, pp)
		if !res.OK() {
			msgs := make([]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				msgs = append(msgs, e.Error())
			}
			return nil, fmt.Errorf("filesystem store: pick_policies[%q]: %s",
				selector, strings.Join(msgs, "; "))
		}
		for _, w := range res.Warnings {
			slog.Warn(w)
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
//
// The standard filesystem store declares a singleton envelope of
// [sync] — concrete-path mode performs in-place writes (no staging).
// A pick-policy claim with sync_strategy: "on_commit" still presents
// sync semantics from the lock manager's perspective: the on-disk
// staging is private to the producer; the conflict matrix uses the
// envelope value.
func (s *Store) Capabilities() claimproducer.Capabilities {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
}

// Open dispatches by selector: pick-policy keys (configured map keys —
// conventionally `@policy-name`) hit openPickPolicy; everything else
// falls through to openScoped.
//
// ctx is accepted to keep the signature uniform across the three
// standard stores (filesystem, postgres, stub); the filesystem
// store has no async work that consults it.
func (s *Store) Open(_ context.Context, claimID, selector string) (claimproducer.OpenOutcome, error) {
	if err := s.checkRootAvailable("Open"); err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	if pp, ok := s.pickPolicies[selector]; ok {
		// Pick-policy selectors are a configured map-key match — they
		// intentionally bypass openScoped's glob-metacharacter
		// rejection. Operators choose the selector key (convention:
		// `@policy-name`); a key containing `*`/`?`/`[` is operator
		// misconfiguration but doesn't violate v3's "concrete paths
		// only" rule, which governs the selector-as-path
		// interpretation that openScoped implements.
		return s.openPickPolicy(claimID, selector, pp)
	}
	return s.openScoped(claimID, selector)
}

// openScoped resolves the selector as a concrete path under the
// configured root. Per §7.7 the standard filesystem store supports
// concrete paths only — globs are rejected.
//
// Selectors are canonicalized via filepath.Clean before use so that
// "foo", "./foo", "foo/.", and "foo/" all produce byte-equal scopes
// (and resolve to the same on-disk path); the cleaned form is
// rejected if it tries to escape the configured root via ".." or
// becomes absolute. This is the load-bearing guard against path
// traversal — without it a selector like "../../etc/passwd" would
// resolve to a path outside s.root.
func (s *Store) openScoped(claimID, selector string) (claimproducer.OpenOutcome, error) {
	raw := strings.TrimSpace(selector)
	if raw == "" {
		return claimproducer.OpenOutcome{}, errors.New("filesystem store: Open: empty selector")
	}
	if hasGlobMeta(raw) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q contains glob metacharacters; v3 standard filesystem supports concrete paths only",
			raw)
	}
	// Canonicalize first so "foo" and "./foo" produce identical scope
	// bytes. Strip a single leading "./" before Clean because Clean
	// preserves "." as the result of "./" only when the input is "./"
	// alone — for "./foo" Clean returns "foo". The TrimPrefix is
	// belt-and-suspenders for inputs like "./".
	cleaned := filepath.Clean(strings.TrimPrefix(raw, "./"))
	if filepath.IsAbs(cleaned) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q is absolute; selectors must be relative paths under the configured root", raw)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q escapes the configured root", raw)
	}
	if cleaned == "." || cleaned == "" {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolves to the root itself; selectors must name a concrete entry", raw)
	}

	// Scope bytes: canonical JSON-encoded cleaned path string. Two
	// claims on the same logical path (e.g. "foo" and "./foo") produce
	// byte-equal scopes (§7.7 obligation #5).
	scopeBytes, err := json.Marshal(cleaned)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: marshal scope: %w", err)
	}
	addrPath := filepath.Join(s.root, cleaned)
	// Defense-in-depth: confirm the resolved path is still under the
	// configured root. After the cleaned-relative-only checks above
	// this should always hold, but a Rel-based test catches any
	// edge case (Windows drive letters, symlinks at the OS layer, etc.)
	rel, relErr := filepath.Rel(s.root, addrPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return claimproducer.OpenOutcome{}, fmt.Errorf(
			"filesystem store: Open: selector %q resolved to %q which escapes the configured root %q", raw, addrPath, s.root)
	}
	addrBytes, err := json.Marshal(addrPath)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("filesystem store: marshal address: %w", err)
	}

	s.mu.Lock()
	s.claims[claimID] = addrPath
	s.mu.Unlock()

	s.ledger.RecordOpen(claimID, raw, addrBytes, scopeBytes)

	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addrBytes),
			ClaimScope:             json.RawMessage(scopeBytes),
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

// Commit delegates to the pick-policy action handler when the claim is
// in pick-policy state; otherwise no-op (direct mode has no staging).
// scope/address are accepted to keep the signature uniform across the
// three standard stores; the filesystem store ignores them on the
// scoped path.
//
// The ledger records the terminal event only after the store-side
// action succeeds. Failures surface as non-terminal
// `claim_commit_failed` events. The s.claims entry is deleted up-front
// (the per-claim path is needed only to seed findByClaimID's lookup);
// retry-ability does not depend on it — a failed action leaves the
// on-disk sentinel intact, and findByClaimID rediscovers the claim
// from that sentinel on the next attempt.
//
// Like Release, Commit refuses to attest against a backing root it can
// no longer see or write (classed fs/root_unavailable, mirroring
// checkRootAvailable's contract) — a direct-mode Commit against a
// vanished root must not silently ack, and a pick-policy Commit must
// surface the operator-misconfiguration class rather than a bare os
// error from the action handler.
func (s *Store) Commit(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Commit"); err != nil {
		return err
	}
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnCommit); err != nil {
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
// as a non-terminal `claim_abandon_failed` event. Like Release and
// Commit, Abandon refuses to attest against an unavailable root
// (classed fs/root_unavailable via checkRootAvailable).
func (s *Store) Abandon(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Abandon"); err != nil {
		return err
	}
	pp, sel, entry, folder := s.findByClaimID(claimID)
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	if pp != nil {
		if err := s.applyPickAction(pp, sel, entry, folder, pp.OnGiveUp); err != nil {
			s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

// Release drops the store's in-memory claim registration. Direct mode
// never registers on-disk read state at Open, so there is nothing to
// undo — but the store still refuses to attest a release against a
// backing root it can no longer see or write: silently acking while
// the root is gone/read-only would let rimsky delete the durable
// claim_handle row and discard the one signal the operator has that
// the store is misconfigured. scope/address are accepted for signature
// uniformity and ignored.
func (s *Store) Release(_ context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.checkRootAvailable("Release"); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.claims, claimID)
	s.mu.Unlock()
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

// checkRootAvailable verifies the configured root still exists and is
// writable before a producer verb proceeds. Failure is transmitted as
// the classed `fs/root_unavailable` error (see errors.go) so the
// operator-misconfiguration case — wrong path, unmounted volume,
// mount gone read-only — crosses the wire as the store's own class +
// message rather than an anonymous fault. Property protected:
// rejecting loudly instead of acking against a vanished root, so no
// rimsky-side state (claim handles) is mutated on the strength of a
// no-op the store could not actually honor.
func (s *Store) checkRootAvailable(verb string) error {
	if _, err := os.Stat(s.root); err != nil {
		return &ClassedError{Class: RootUnavailableClass, Err: fmt.Errorf(
			"filesystem store: %s: configured root %q is not accessible: %v", verb, s.root, err)}
	}
	// W_OK (POSIX value 2): the store's state directories and
	// pick-policy actions live under the root, so a read-only root is
	// a misconfiguration for any claim-mutating verb. This package is
	// already POSIX-only (see the syscall.Stat_t use below).
	if err := syscall.Access(s.root, 0x2); err != nil {
		return &ClassedError{Class: RootUnavailableClass, Err: fmt.Errorf(
			"filesystem store: %s: configured root %q is not writable: %v", verb, s.root, err)}
	}
	return nil
}

// hasGlobMeta reports whether s contains any glob metacharacter
// recognised by stdlib path/filepath.Match.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// validatePickPolicy validates the operator-supplied pick policy against
// the store root and returns a ValidationResult. Per spec §6: returns
// the full set of errors (so operators see every problem in one pass)
// plus advisory warnings (e.g. inert combinations). Pre-v1 break-cleanly:
// old field names and old action values are rejected at config-load.
//
// Symlink assumption: the lexical Rel-based containment check below
// confirms `<storeRoot>/<pp.Root>` is lexically under storeRoot, but
// `os.Stat` follows symlinks. If the operator places a symlink under
// the store root that points outside, runtime operations will follow
// it. Per the spec's "POSIX local filesystems" assumption the store
// root must not contain symlinks pointing outside itself; this is an
// operator-fault constraint, not enforced here.
func validatePickPolicy(storeRoot, selector string, pp *PickPolicy) action.ValidationResult {
	var res action.ValidationResult
	addErr := func(err error) { res.Errors = append(res.Errors, err) }
	addWarn := func(w string) { res.Warnings = append(res.Warnings, w) }

	if pp == nil {
		addErr(errors.New("policy is nil"))
		return res
	}
	// pp.Root may be empty: that means the policy operates at the store root
	// itself (e.g. a single-entry policy whose FolderPattern matches one
	// specific top-level folder). filepath.Clean("") == ".", so the
	// canonicalization checks below treat empty and "." identically.
	if filepath.IsAbs(pp.Root) {
		addErr(fmt.Errorf("root: %q is absolute; must be relative to store root", pp.Root))
	} else {
		cleaned := filepath.Clean(pp.Root)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			addErr(fmt.Errorf("root: %q escapes the store root", pp.Root))
		} else {
			absPath := filepath.Join(storeRoot, cleaned)
			rel, relErr := filepath.Rel(storeRoot, absPath)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				addErr(fmt.Errorf("root: %q resolves to %q which escapes the store root", pp.Root, absPath))
			} else if info, err := os.Stat(absPath); err != nil {
				addErr(fmt.Errorf("root: stat %s: %w", absPath, err))
			} else if !info.IsDir() {
				addErr(fmt.Errorf("root: %s is not a directory", absPath))
			} else if _, err := os.ReadDir(absPath); err != nil {
				addErr(fmt.Errorf("root: %s not readable: %w", absPath, err))
			} else {
				probe, perr := os.CreateTemp(absPath, ".rimsky-fs-store-probe-*")
				if perr != nil {
					addErr(fmt.Errorf("root: %s not writable: %w", absPath, perr))
				} else {
					probeName := probe.Name()
					_ = probe.Close()
					_ = os.Remove(probeName)
				}
			}
		}
	}

	// Action validation (replaces the old switch on string vocabulary).
	//
	// Issue 11: yaml.v3 silently skips UnmarshalYAML when the source is
	// null and the target is a struct value, leaving Kind=="". The raw
	// Validate() error for that case is `unknown action ""`, which
	// leaks an empty quoted string. Surface a clearer message before
	// delegating to Validate().
	if pp.OnCommit.Kind == "" {
		addErr(errors.New("on_commit: required (got null or missing)"))
	} else if err := pp.OnCommit.Validate(); err != nil {
		addErr(fmt.Errorf("on_commit: %w", err))
	}
	if pp.OnGiveUp.Kind == "" {
		addErr(errors.New("on_give_up: required (got null or missing)"))
	} else if err := pp.OnGiveUp.Validate(); err != nil {
		addErr(fmt.Errorf("on_give_up: %w", err))
	}

	// pop_and_move target validation: cross-fs check.
	if pp.OnCommit.Kind == action.PopAndMove && pp.OnCommit.MoveTarget != "" {
		if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnCommit.MoveTarget); err != nil {
			addErr(fmt.Errorf("on_commit: pop_and_move: %w", err))
		}
	}
	if pp.OnGiveUp.Kind == action.PopAndMove && pp.OnGiveUp.MoveTarget != "" {
		if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnGiveUp.MoveTarget); err != nil {
			addErr(fmt.Errorf("on_give_up: pop_and_move: %w", err))
		}
	}

	if pp.VisibilityTimeout <= 0 {
		addErr(errors.New("visibility_timeout_seconds: must be > 0"))
	}

	switch pp.SyncStrategy {
	case "":
		pp.SyncStrategy = "on_open" // default
	case "on_open", "on_drain", "explicit", "never":
		// ok
	default:
		addErr(fmt.Errorf("sync_strategy: must be on_open|on_drain|explicit|never, got %q", pp.SyncStrategy))
	}

	// Validator rule §6.1a: pop + sync_strategy: on_open is rejected.
	// Open's sync step would re-add popped folders under fs-store
	// discovery semantics, so the queue would never drain.
	if pp.OnCommit.Kind == action.Pop && pp.SyncStrategy == "on_open" {
		addErr(errors.New("on_commit: pop is incompatible with sync_strategy: on_open (queue would never drain because runSync re-adds popped folders); use sync_strategy: on_drain"))
	}

	// Validator rule §6.2: warn on recycle + on_drain (queue never empties; on_drain never fires).
	if pp.OnCommit.Kind == action.Recycle && pp.SyncStrategy == "on_drain" {
		addWarn(fmt.Sprintf("filesystem store: pick_policies[%q]: recycle + sync_strategy: on_drain is inert (queue never empties; on_drain never fires)", selector))
	}

	_ = selector
	return res
}

// validateMoveTargetContained mirrors the policy-root containment
// checks for `target` so an operator config of
// `pop_and_move: ../../etc/triage` cannot redirect renames outside
// the configured store root. The target — like the policy root —
// must be relative, must not begin with `..`, and must lexically
// resolve under storeRoot.
//
// This is symmetric with the openScoped traversal guard
// (filesystem/store.go::openScoped lines ~190-204).
func validateMoveTargetContained(storeRoot, target string) error {
	if target == "" {
		return errors.New("target must be non-empty")
	}
	if filepath.IsAbs(target) {
		return fmt.Errorf("target %q is absolute; must be relative to store root", target)
	}
	cleaned := filepath.Clean(target)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %q escapes the store root", target)
	}
	absPath := filepath.Join(storeRoot, cleaned)
	rel, relErr := filepath.Rel(storeRoot, absPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %q resolves to %q which escapes the store root", target, absPath)
	}
	return nil
}

// validateMoveTargetSameFS checks that target (resolved relative to
// storeRoot) is on the same filesystem as <storeRoot>/<policyRoot>,
// is contained within the store root, exists, is a directory, and
// is not the same directory as the policy root.
//
// Per spec §6.3: resolve both paths to absolute, follow symlinks via
// filepath.EvalSymlinks, then compare device IDs.
//
// The target must also exist (must be a directory). The validator
// does NOT create missing target directories.
//
// Containment-check note: containment is enforced first, before the
// EvalSymlinks-based same-fs check — a `..`-prefixed target that
// happens to resolve to a same-fs directory would otherwise load
// cleanly and let every commit rename folders outside the store root.
func validateMoveTargetSameFS(storeRoot, policyRoot, target string) error {
	if err := validateMoveTargetContained(storeRoot, target); err != nil {
		return err
	}
	policyAbs, err := filepath.Abs(filepath.Join(storeRoot, policyRoot))
	if err != nil {
		return fmt.Errorf("resolve policy root: %w", err)
	}
	targetAbs, err := filepath.Abs(filepath.Join(storeRoot, target))
	if err != nil {
		return fmt.Errorf("resolve target %q: %w", target, err)
	}
	policyResolved, err := filepath.EvalSymlinks(policyAbs)
	if err != nil {
		return fmt.Errorf("resolve symlinks for policy root %q: %w", policyAbs, err)
	}
	targetResolved, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return fmt.Errorf("resolve symlinks for target %q: %w", targetAbs, err)
	}
	// Issue 7: degenerate target == policy root produces silent
	// behavioral drift (os.Rename(dir, dir) is a no-op). Reject
	// explicitly so the operator gets an error instead of "pop_and_move
	// silently degenerated to pop."
	if policyResolved == targetResolved {
		return fmt.Errorf("target %q resolves to the same directory as the policy root %q; pop_and_move with target == policy root is a no-op (use pop instead)", target, policyRoot)
	}
	policyStat, err := os.Stat(policyResolved)
	if err != nil {
		return fmt.Errorf("stat policy root: %w", err)
	}
	targetStat, err := os.Stat(targetResolved)
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if !targetStat.IsDir() {
		return fmt.Errorf("target %q is not a directory", target)
	}
	policySys, ok1 := policyStat.Sys().(*syscall.Stat_t)
	targetSys, ok2 := targetStat.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return errors.New("filesystem device-id query unavailable on this platform")
	}
	if policySys.Dev != targetSys.Dev {
		return fmt.Errorf("target %q is on a different filesystem than the policy root %q; os.Rename across filesystems is not atomic, refusing to load", target, policyRoot)
	}
	return nil
}
