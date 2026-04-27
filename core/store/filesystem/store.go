package filesystem

import (
	"context"
	"encoding/json"
	"fmt"

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

// Name returns the operator-configured store name (matches stores.<name>
// in YAML).
func (s *Store) Name() string { return s.name }

// Kind returns the canonical store kind, "filesystem".
func (s *Store) Kind() string { return "filesystem" }

// Root returns the root directory path the store was configured with.
// Exposed primarily for tests; callers in the main runtime should use
// the Path field on FilesystemDirectHandle instead.
func (s *Store) Root() string { return s.root }

// Capabilities reports the direct-mode filesystem store's supported
// operations. Region locks: yes. Resume: yes (live region carries any
// in-progress writes from a prior dispatch). Claim/discard/restore: no.
func (s *Store) Capabilities() store.Capabilities {
	return store.Capabilities{
		SupportsRegionLock: true,
		SupportsClaim:      false,
		SupportsDiscard:    false,
		SupportsResume:     true,
		SupportsRestore:    false,
	}
}

// LockEligible always returns true. The supervisor pre-screens region
// locks against existing holders via RegionsConflict before this is
// called; the direct-mode filesystem store has no additional eligibility
// constraints (no quotas, no per-region pacing).
func (s *Store) LockEligible(_ context.Context, _ store.LockSpec) (bool, error) {
	return true, nil
}

// RegionsConflict delegates to the package-level pure helper. Inputs are
// expected to be []string of path globs (the store's region grammar).
// Inputs of an unexpected type are treated as conflicting — the
// supervisor must never silently admit an acquisition whose region we
// cannot interpret.
func (s *Store) RegionsConflict(a, b any) bool {
	ga, okA := a.([]string)
	gb, okB := b.([]string)
	if !okA || !okB {
		return true
	}
	return RegionsConflict(ga, gb)
}

// UnmarshalRegion deserializes region_data JSONB into []string. The
// returned `any` is the same []string typed back through `any` so the
// caller can pass it straight to RegionsConflict.
func (s *Store) UnmarshalRegion(raw []byte) (any, error) {
	var globs []string
	if err := json.Unmarshal(raw, &globs); err != nil {
		return nil, fmt.Errorf("filesystem store %q: unmarshal region: %w", s.name, err)
	}
	return globs, nil
}

// AcquireLock is a no-op for direct-mode filesystem. The supervisor's
// outer transaction has already inserted the lock-holder row by the
// time this is called. Nothing to do on the store side; the caller
// receives the LockHandle they passed an empty struct under.
//
// We return the lock handle the supervisor passed nothing for — the
// supervisor populates LockHandle from the inserted row independently of
// the store. AcquireLock's contract is: return LockHandle and
// ClaimResult. Direct-mode returns zero values for both; the supervisor
// only consumes ClaimResult for claim-mode acquisitions. The supervisor
// fills in LockHandle's ID/timestamps from the SQL row before calling
// OpenHandle.
func (s *Store) AcquireLock(_ context.Context, _ store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
	return store.LockHandle{}, store.ClaimResult{}, nil
}

// OpenHandle returns the FilesystemDirectHandle pointing at the live
// region. The supervisor passes the original LockSpec's RegionLockSpec
// payload through `lh` indirectly — but AcquireLock didn't capture it,
// so OpenHandle can't reconstruct the regions from `lh` alone.
//
// To keep OpenHandle pure (and to honour the spec's "OpenHandle takes
// LockHandle, not LockSpec"), the supervisor's runner threads the
// resolved write/read regions through a context value before calling
// OpenHandle. See OpenHandleWithRegions for the entry point used in
// practice; OpenHandle exists only to satisfy the Store interface and
// returns a handle with empty region slices when called without context.
//
// resumed is ignored: in direct mode the live path is the same whether
// or not a prior dispatch left in-progress writes behind.
func (s *Store) OpenHandle(ctx context.Context, _ store.LockHandle, _ bool) (store.NativeHandle, error) {
	write, read := regionsFromContext(ctx)
	return store.FilesystemDirectHandle{
		Path:         s.root,
		WriteRegions: write,
		ReadRegions:  read,
	}, nil
}

// Commit is a no-op for direct mode. Writes already landed on the live
// filesystem when the executor performed them; there is no sidecar to
// apply. We return Changed:true unconditionally because the executor
// signals Complete{changed: true} via a separate path before the
// supervisor calls Commit; the value here is a placeholder for the
// post-v1 sidecar/versioned modes that compute it.
func (s *Store) Commit(_ context.Context, _ store.LockHandle) (store.CommitResult, error) {
	return store.CommitResult{Changed: true}, nil
}

// ReleaseLock is a no-op for all actions. Direct-mode has no sidecar to
// discard, no claim to release, no items-table flip to undo.
func (s *Store) ReleaseLock(_ context.Context, _ store.LockHandle, _ store.ReleaseAction) error {
	return nil
}

// HasPriorWork reports whether prior in-progress state exists for the
// given lock spec. In direct mode the live region is always usable, so
// we conservatively return false: the supervisor will not pass
// resumed=true to OpenHandle, and the executor sees a fresh dispatch
// with the live region. (Direct-mode resumption semantics: §6.1 — the
// executor itself is responsible for noticing prior partial state on
// disk and skipping/redoing as appropriate.)
func (s *Store) HasPriorWork(_ context.Context, _ store.LockSpec) (bool, error) {
	return false, nil
}

// regionsCtxKey is the context key under which the supervisor's runner
// stashes the resolved write/read regions before calling OpenHandle.
// Unexported zero-size struct: no collision with other packages.
type regionsCtxKey struct{}

// regionsCtxValue is the payload attached via WithRegions.
type regionsCtxValue struct {
	write []string
	read  []string
}

// WithRegions attaches resolved write and read regions to the context.
// The supervisor's runner calls this with the values from the original
// RegionLockSpec before calling Store.OpenHandle, so the direct-mode
// store can echo them back into the FilesystemDirectHandle without
// inflating the LockHandle struct.
func WithRegions(ctx context.Context, write, read []string) context.Context {
	return context.WithValue(ctx, regionsCtxKey{}, regionsCtxValue{write: write, read: read})
}

// regionsFromContext returns the (write, read) regions attached via
// WithRegions, or two nil slices if none is present.
func regionsFromContext(ctx context.Context) (write, read []string) {
	v, ok := ctx.Value(regionsCtxKey{}).(regionsCtxValue)
	if !ok {
		return nil, nil
	}
	return v.write, v.read
}
