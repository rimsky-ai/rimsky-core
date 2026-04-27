package claimstorepg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the postgres-backed claim store. State held by Store itself is
// immutable after construction (name + pool reference + items-table name +
// configured defaults). Lock state lives in `rimsky_lock_holders`; per-item
// availability lives in the operator-owned items table; this struct caches
// neither.
//
// Thread-safety: all methods are safe for concurrent use. Reads use the
// caller-supplied tx (via `store.TxFromContext`) when one is attached; the
// pool fallback is reserved for read-only paths that do not run inside the
// supervisor's atomic acquisition transaction (e.g. eligibility hints).
type Store struct {
	name              string
	pool              *pgxpool.Pool
	itemsTable        string
	onCommitDefault   string // 'delete' | 'release_to_back' | 'release_to_head'
	onGiveUpDefault   string // same vocabulary
	visibilityTimeout time.Duration
}

// Compile-time interface checks.
var (
	_ store.Store          = (*Store)(nil)
	_ store.ClaimableStore = (*Store)(nil)
	_ store.ResumableStore = (*Store)(nil)
)

// Name returns the operator-configured store name.
func (s *Store) Name() string { return s.name }

// Kind returns the canonical store kind, "claim_store".
func (s *Store) Kind() string { return "claim_store" }

// ItemsTable returns the operator-owned items-table name. Exposed for tests.
func (s *Store) ItemsTable() string { return s.itemsTable }

// OnCommitDefault returns the configured default action for commit
// terminations. Exposed for tests and §5.6.4 resolution callers.
func (s *Store) OnCommitDefault() string { return s.onCommitDefault }

// OnGiveUpDefault returns the configured default action for give-up
// terminations. Exposed for tests and §5.6.4 resolution callers.
func (s *Store) OnGiveUpDefault() string { return s.onGiveUpDefault }

// VisibilityTimeout returns the §7.7 visibility-timeout backstop window.
// Exposed for the §13.5 step-4 sweep.
func (s *Store) VisibilityTimeout() time.Duration { return s.visibilityTimeout }

// Capabilities reports the claim_store's supported operations per spec §7.5.
// Region locks: no. Claim: yes. Discard: yes (semantics: store-side release-
// on-give_up). Resume: yes (claim ref preserved). Restore: no.
func (s *Store) Capabilities() store.Capabilities {
	return store.Capabilities{
		SupportsRegionLock: false,
		SupportsClaim:      true,
		SupportsDiscard:    true,
		SupportsResume:     true,
		SupportsRestore:    false,
	}
}

// LockEligible reports whether at least one claimable item is available
// for a ClaimLockSpec; defers to HasClaimableItem. For non-claim specs
// (which should not be issued against a claim_store at all) we return
// false to fail closed.
func (s *Store) LockEligible(ctx context.Context, spec store.LockSpec) (bool, error) {
	cls, ok := spec.(store.ClaimLockSpec)
	if !ok {
		return false, nil
	}
	return s.HasClaimableItem(ctx, cls.Criteria)
}

// RegionsConflict always returns false: claim stores have no regions, so
// no two acquisitions can be region-conflicting. The supervisor's region
// pre-screen (spec §13.2) is short-circuited for claim specs upstream of
// this call, but we return false unconditionally for completeness.
func (s *Store) RegionsConflict(_, _ any) bool { return false }

// UnmarshalRegion returns (nil, nil): claim_store has no region grammar.
// `rimsky_lock_holders.region_data` is null for kind='claim' rows by the
// CHECK constraint in the migration.
func (s *Store) UnmarshalRegion(_ []byte) (any, error) { return nil, nil }

// HasClaimableItem reports whether at least one row matching `criteria`
// is currently in `state='available'`. Used by the dispatch eligibility
// evaluator to skip nodes whose claim pool is empty.
//
// v1 ignores `criteria` entirely — the spec leaves criteria-derived
// predicates as a future extension (§13.3 SQL says
// `(<criteria-derived predicate> OR true)`). When criteria-driven
// eligibility lands, this is the call site.
//
// Reads from the pool, not the tx, because eligibility is evaluated
// outside the atomic acquisition tx (spec §13.2). Acquiring the row is
// done by AcquireLock inside the tx with FOR UPDATE SKIP LOCKED.
func (s *Store) HasClaimableItem(ctx context.Context, _ map[string]any) (bool, error) {
	q := fmt.Sprintf(
		`SELECT 1 FROM %s WHERE state = 'available' LIMIT 1`,
		s.itemsTable,
	)
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return false, fmt.Errorf("claim_store %q: HasClaimableItem: %w", s.name, err)
	}
	defer rows.Close()
	return rows.Next(), nil
}

// HasPriorWork reports whether the store retains in-progress state for
// the given lock spec. For claim stores, in-progress state is the
// items-table row whose state='in_progress' and claim_token matches a
// rimsky_lock_holders row; the rebind path of §13.3 step 3a handles this
// based on the lock-holder row alone, so for v1 we conservatively report
// false. The supervisor's runner falls back to OpenHandle(resumed=false)
// in that case, which still works because AcquireLock returns the same
// items-row payload from the rebound state. Documented as v1 simplification.
func (s *Store) HasPriorWork(_ context.Context, _ store.LockSpec) (bool, error) {
	return false, nil
}

// OpenHandle returns the ClaimStoreHandle the executor will see in
// `ExecuteRequest.stores[<name>].handle` (spec §12.1). The supervisor
// stashes the resolved claim payload + claim ID in the context via
// `WithHandleData` immediately before calling OpenHandle (mirroring the
// filesystem store's region-threading pattern); we read them back here.
//
// The `resumed` flag is ignored in v1 — the handle shape is the same
// whether or not the caller is rebinding a resumed acquisition. Executors
// that care can read `ExecuteRequest.run_attempt`.
func (s *Store) OpenHandle(ctx context.Context, _ store.LockHandle, _ bool) (store.NativeHandle, error) {
	payload, claimID := handleDataFromContext(ctx)
	return store.ClaimStoreHandle{
		Payload:   payload,
		ClaimID:   claimID,
		StoreName: s.name,
	}, nil
}

// Commit is a no-op for claim stores: the items-table mutation happens at
// AcquireLock time and is finalized by ReleaseLock per the on_commit
// policy. We return Changed=true unconditionally because the executor
// signals Complete{changed: ...} via a separate path before the supervisor
// calls Commit.
func (s *Store) Commit(_ context.Context, _ store.LockHandle) (store.CommitResult, error) {
	return store.CommitResult{Changed: true}, nil
}

// handleDataCtxKey is the unexported context key under which the
// supervisor's runner stashes the claim payload + claim ID before calling
// OpenHandle.
type handleDataCtxKey struct{}

// handleDataCtxValue is the payload attached via WithHandleData.
type handleDataCtxValue struct {
	payload any
	claimID string
}

// WithHandleData attaches a claim payload + claim ID to the context. The
// supervisor's runner calls this with the values from the AcquireLock
// result before calling Store.OpenHandle, so the claim_store can echo
// them back into the ClaimStoreHandle without inflating the LockHandle
// struct with store-kind-specific data.
func WithHandleData(ctx context.Context, payload any, claimID string) context.Context {
	return context.WithValue(ctx, handleDataCtxKey{}, handleDataCtxValue{payload: payload, claimID: claimID})
}

// handleDataFromContext returns the (payload, claimID) attached via
// WithHandleData, or (nil, "") if none is present.
func handleDataFromContext(ctx context.Context) (any, string) {
	v, ok := ctx.Value(handleDataCtxKey{}).(handleDataCtxValue)
	if !ok {
		return nil, ""
	}
	return v.payload, v.claimID
}
