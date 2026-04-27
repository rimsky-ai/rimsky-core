package stub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the in-memory stub store. State is held entirely in the
// Store itself; no postgres, no filesystem. Suitable for scenario tests
// that exercise runner / state-machine semantics without needing real
// store infrastructure.
//
// State by mode:
//
//   - Region-lock mode (KindFilesystem): `regionHolders` carries the set
//     of regions currently locked. RegionsConflict and LockEligible
//     consult it. The grammar is []string of opaque tokens: two regions
//     conflict iff they share any token. This is a deliberately simple
//     stand-in for filesystem path-glob semantics — scenario tests pass
//     distinct tokens for distinct regions and equal tokens for
//     overlapping ones.
//
//   - Claim mode (KindClaimStore): `claimQueue` is the FIFO queue of
//     available items; `inFlight` maps claim_id → payload for items
//     currently checked out. AcquireLock pops from the head; release
//     actions push to head/tail/delete per the action.
//
//   - `lockHeld` records named-lock counts so the stub can model the
//     counting-semaphore Limit cap inside LockEligible. (Region/claim
//     locks are tracked above; this map is only consulted for named
//     locks.)
//
// Thread-safety: a single sync.Mutex protects all mutable state. All
// public methods acquire the mutex; method bodies are short, so there
// is no contention concern.
type Store struct {
	name         string
	kind         string
	capabilities store.Capabilities

	mu sync.Mutex

	// region-lock state
	regionHolders map[string]struct{} // set of region tokens currently held

	// claim state
	claimQueue   []claimItem    // FIFO of available items; head at index 0
	inFlight     map[string]any // claim_id → payload (currently held)
	nextClaimSeq int            // monotonic seq for synthetic claim IDs

	// named-lock state
	lockHeld map[string]int // name → current holder count
}

// claimItem is one row in the in-memory FIFO queue.
type claimItem struct {
	claimID string
	payload any
}

// Compile-time interface checks. The stub satisfies all three interfaces
// regardless of the configured capability flags; LockEligible /
// AcquireLock are the runtime gates that consult capabilities.
var (
	_ store.Store          = (*Store)(nil)
	_ store.ClaimableStore = (*Store)(nil)
	_ store.ResumableStore = (*Store)(nil)
)

// newStore constructs a fresh *Store with the given identity and
// capability flags. All state maps/slices are initialised so callers
// can use the store immediately without further setup.
func newStore(name, kind string, caps store.Capabilities) *Store {
	return &Store{
		name:          name,
		kind:          kind,
		capabilities:  caps,
		regionHolders: make(map[string]struct{}),
		claimQueue:    nil,
		inFlight:      make(map[string]any),
		lockHeld:      make(map[string]int),
	}
}

// Name returns the operator-configured store name.
func (s *Store) Name() string { return s.name }

// Kind returns the stub kind ("stub_filesystem" | "stub_claim_store").
// The supervisor's per-store-kind dispatch sees this string; tests that
// want to swap a stub for a real store can register the stub under the
// production kind by editing the registry rather than this method.
func (s *Store) Kind() string { return s.kind }

// Capabilities returns the configured flags. The supervisor reads this
// at dispatch time (spec §8.4.1) to decide whether a node's required
// operations are serviceable.
func (s *Store) Capabilities() store.Capabilities { return s.capabilities }

// LockEligible is the eligibility check used by the dispatch evaluator.
// Behaviour by spec kind:
//
//   - NamedLockSpec: returns false if the current holder count is at
//     limit. Mutex limit is implicit 1; counting takes Limit from the
//     spec.
//
//   - RegionLockSpec: returns false if the supplied region overlaps any
//     currently-held region; true otherwise. The supervisor pre-screens
//     via RegionsConflict before reaching here, but we re-check so the
//     stub remains correct in isolation.
//
//   - ClaimLockSpec: returns true iff the queue has at least one
//     available item. (Mirrors HasClaimableItem.)
//
// Returns false if the spec kind is incompatible with the configured
// capability flags (e.g. region spec against a claim-only stub).
func (s *Store) LockEligible(_ context.Context, spec store.LockSpec) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch sp := spec.(type) {
	case store.NamedLockSpec:
		limit := 1
		if sp.Mode == store.LockModeCounting {
			limit = sp.Limit
		}
		return s.lockHeld[sp.Name] < limit, nil
	case store.RegionLockSpec:
		if !s.capabilities.SupportsRegionLock {
			return false, nil
		}
		region, ok := sp.Region.([]string)
		if !ok {
			return false, nil
		}
		for _, tok := range region {
			if _, held := s.regionHolders[tok]; held {
				return false, nil
			}
		}
		return true, nil
	case store.ClaimLockSpec:
		if !s.capabilities.SupportsClaim {
			return false, nil
		}
		return len(s.claimQueue) > 0, nil
	}
	return false, nil
}

// RegionsConflict reports whether two stub regions overlap. The grammar
// is []string; two regions conflict iff they share any token. Wrong-
// typed inputs are treated as conflicting (fail-closed) — the stub
// matches the filesystem store's defensive posture so scenario tests
// catch upstream bugs that pass the wrong region shape.
func (s *Store) RegionsConflict(a, b any) bool {
	ga, okA := a.([]string)
	gb, okB := b.([]string)
	if !okA || !okB {
		return true
	}
	set := make(map[string]struct{}, len(ga))
	for _, t := range ga {
		set[t] = struct{}{}
	}
	for _, t := range gb {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

// UnmarshalRegion deserializes region_data JSONB into []string. Mirrors
// the filesystem store's contract so scenarios that exercise the
// supervisor's region-decoding path work uniformly.
func (s *Store) UnmarshalRegion(raw []byte) (any, error) {
	var tokens []string
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, fmt.Errorf("stub store %q: unmarshal region: %w", s.name, err)
	}
	return tokens, nil
}

// AcquireLock honours the spec kind:
//
//   - NamedLockSpec: increments the holder count for the name. The
//     supervisor's outer tx still inserts the rimsky_lock_holders row;
//     the stub's count tracks "what would the holders table say".
//
//   - RegionLockSpec: inserts the supplied region tokens into the
//     held set. Conflicts must be screened upstream via LockEligible /
//     RegionsConflict.
//
//   - ClaimLockSpec: pops the head of the FIFO queue, returns its
//     payload + auto-generated claim_id, records it in inFlight. If
//     the queue is empty, returns a zero ClaimResult and no error
//     (mirrors claim-store-postgres semantics).
//
// Returns an error if the spec kind is incompatible with the configured
// capabilities (e.g. claim spec against a region-only stub).
func (s *Store) AcquireLock(_ context.Context, spec store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch sp := spec.(type) {
	case store.NamedLockSpec:
		s.lockHeld[sp.Name]++
		return store.LockHandle{}, store.ClaimResult{}, nil
	case store.RegionLockSpec:
		if !s.capabilities.SupportsRegionLock {
			return store.LockHandle{}, store.ClaimResult{},
				fmt.Errorf("stub store %q: AcquireLock: region locks not supported", s.name)
		}
		region, ok := sp.Region.([]string)
		if !ok {
			return store.LockHandle{}, store.ClaimResult{},
				fmt.Errorf("stub store %q: AcquireLock: region must be []string, got %T", s.name, sp.Region)
		}
		for _, tok := range region {
			s.regionHolders[tok] = struct{}{}
		}
		return store.LockHandle{}, store.ClaimResult{}, nil
	case store.ClaimLockSpec:
		if !s.capabilities.SupportsClaim {
			return store.LockHandle{}, store.ClaimResult{},
				fmt.Errorf("stub store %q: AcquireLock: claim locks not supported", s.name)
		}
		if len(s.claimQueue) == 0 {
			return store.LockHandle{}, store.ClaimResult{}, nil
		}
		head := s.claimQueue[0]
		s.claimQueue = s.claimQueue[1:]
		s.inFlight[head.claimID] = head.payload
		return store.LockHandle{}, store.ClaimResult{
			Payload: head.payload,
			ClaimID: head.claimID,
		}, nil
	}
	return store.LockHandle{}, store.ClaimResult{},
		fmt.Errorf("stub store %q: AcquireLock: unknown spec kind %T", s.name, spec)
}

// OpenHandle returns a NativeHandle shaped to the configured kind:
//
//   - KindFilesystem returns FilesystemDirectHandle{Path: "<store-name>"}
//     with the regions stashed by the runner via WithRegions; tests
//     that don't attach regions get zero-valued slices.
//
//   - KindClaimStore returns ClaimStoreHandle with payload + claim_id
//     stashed by the runner via WithHandleData. Tests that don't attach
//     these get zero values.
//
// resumed is reflected on the handle as-is for tests that want to
// assert the runner's resume-vs-fresh dispatch logic; the stub does
// not change handle shape based on it.
func (s *Store) OpenHandle(ctx context.Context, _ store.LockHandle, _ bool) (store.NativeHandle, error) {
	switch s.kind {
	case KindFilesystem:
		write, read := regionsFromContext(ctx)
		return store.FilesystemDirectHandle{
			Path:         s.name,
			WriteRegions: write,
			ReadRegions:  read,
		}, nil
	case KindClaimStore:
		payload, claimID := handleDataFromContext(ctx)
		return store.ClaimStoreHandle{
			Payload:   payload,
			ClaimID:   claimID,
			StoreName: s.name,
		}, nil
	}
	return nil, fmt.Errorf("stub store %q: OpenHandle: unknown kind %q", s.name, s.kind)
}

// Commit is a no-op apart from returning Changed:true. The stub does
// not track per-acquisition metadata, so every commit reports change.
func (s *Store) Commit(_ context.Context, _ store.LockHandle) (store.CommitResult, error) {
	return store.CommitResult{Changed: true}, nil
}

// ReleaseLock cleans up in-memory state per the spec kind. The stub
// holds the original LockSpec on no LockHandle field (the LockHandle
// shape is not store-kind-specific), so we accept the action verbatim
// without spec-kind dispatch and rely on the caller having paired
// AcquireLock + ReleaseLock correctly.
//
// In practice ReleaseLock is invoked in scenario tests via
// ReleaseRegion / ReleaseNamedLock helpers, which know which slot to
// clear; the action is a no-op for region and named locks because the
// holder set/count is updated via those helpers. For claim locks, this
// method is a no-op too: the items-table mutation is owned by
// ReleaseClaimItem (mirrors claim-store-postgres §8.5.1 split).
func (s *Store) ReleaseLock(_ context.Context, _ store.LockHandle, _ store.ReleaseAction) error {
	return nil
}

// HasClaimableItem reports whether at least one item is currently
// available. v1 ignores criteria (mirrors claim-store-postgres §13.3
// behaviour where criteria-derived predicates are deferred).
func (s *Store) HasClaimableItem(_ context.Context, _ map[string]any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.claimQueue) > 0, nil
}

// ReleaseClaimItem performs the in-memory items-table reposition for a
// previously claimed (in-flight or already-released) item. Action
// vocabulary mirrors claim-store-postgres release.go (spec §8.5.1):
//
//   - "release_to_back": move the item to the queue's tail and clear
//     its in-flight entry.
//   - "release_to_head": move the item to the queue's head and clear
//     its in-flight entry.
//
// "delete" and "delete_won" are NOT valid actions here: per spec §5.6.4
// the items-table DELETE is owned by the supervisor's resolution
// algorithm (ResolveOnTerminal), not by ReleaseClaimItem. The stub
// rejects them with the same wording as claim-store-postgres so
// scenario tests can't pass against the stub and then break against
// production. Tests that need to model the supervisor-driven §5.6.4
// DELETE without going through this API should call DeleteInFlight
// directly.
//
// Errors on unknown actions, the delete actions, or unknown claim IDs.
// On any error path the in-flight entry is preserved so the caller can
// retry with a valid action.
func (s *Store) ReleaseClaimItem(_ context.Context, claimID string, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, ok := s.takeInFlight(claimID)
	if !ok {
		return fmt.Errorf("stub store %q: ReleaseClaimItem: no item with claim_id %q", s.name, claimID)
	}

	switch action {
	case "release_to_back":
		s.claimQueue = append(s.claimQueue, claimItem{claimID: claimID, payload: payload})
		return nil
	case "release_to_head":
		s.claimQueue = append([]claimItem{{claimID: claimID, payload: payload}}, s.claimQueue...)
		return nil
	case "delete", "delete_won":
		// Restore inFlight so the caller's bookkeeping isn't silently
		// corrupted by an action that production rejects.
		s.inFlight[claimID] = payload
		return fmt.Errorf("stub store %q: ReleaseClaimItem called with action %q — delete is owned by the §5.6.4 resolution algorithm, not this method", s.name, action)
	default:
		// Restore inFlight so we don't strand the row on an error path.
		s.inFlight[claimID] = payload
		return fmt.Errorf("stub store %q: ReleaseClaimItem: unknown action %q", s.name, action)
	}
}

// HasPriorWork reports whether prior in-progress state exists for the
// given lock spec. The stub returns false unconditionally — mirrors the
// v1 simplification in both filesystem and claim-store-postgres
// implementations. Scenario tests that need to exercise the resumed=true
// path of OpenHandle should rebuild the harness with prior in-flight
// state injected directly via SeedInFlight.
func (s *Store) HasPriorWork(_ context.Context, _ store.LockSpec) (bool, error) {
	return false, nil
}

// ReleaseRegion clears region tokens from the held set. Scenario test
// helper: tests call this to model the supervisor's outer tx removing
// a rimsky_lock_holders row for a region acquisition.
func (s *Store) ReleaseRegion(region []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tok := range region {
		delete(s.regionHolders, tok)
	}
}

// ReleaseNamedLock decrements the holder count for a named lock.
// Scenario test helper: mirrors ReleaseRegion's role for named locks.
// Bottoms out at zero — does not go negative on extra releases.
func (s *Store) ReleaseNamedLock(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockHeld[name] > 0 {
		s.lockHeld[name]--
	}
}

// SeedItem appends a payload to the FIFO queue and returns the synthetic
// claim ID assigned. Scenario test helper used by claim-mode tests to
// stage items before exercising AcquireLock.
func (s *Store) SeedItem(payload any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.mintClaimID()
	s.claimQueue = append(s.claimQueue, claimItem{claimID: id, payload: payload})
	return id
}

// SeedInFlight injects an item directly into the in-flight set with the
// supplied claim ID. Scenario test helper for exercising release /
// resume paths without first running AcquireLock.
func (s *Store) SeedInFlight(claimID string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight[claimID] = payload
}

// DeleteInFlight removes an in-flight entry without repositioning the
// items-table row. Scenario test helper that models the supervisor-
// driven §5.6.4 resolution-algorithm DELETE: production claim-store-
// postgres has the supervisor execute `DELETE FROM <items_table>` via
// ResolveOnTerminal, completely separate from ReleaseClaimItem. Tests
// that need to model "this in-flight row is gone" call this directly
// rather than passing "delete" / "delete_won" to ReleaseClaimItem
// (which is rejected, matching production's §8.5.1 contract).
//
// Returns true if the claim ID was present, false otherwise.
func (s *Store) DeleteInFlight(claimID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inFlight[claimID]; !ok {
		return false
	}
	delete(s.inFlight, claimID)
	return true
}

// QueueLen returns the current FIFO queue length. Scenario test helper
// for asserting queue state after release / claim sequences.
func (s *Store) QueueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.claimQueue)
}

// InFlight returns the set of claim IDs currently in-flight, sorted for
// deterministic test assertions.
func (s *Store) InFlight() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.inFlight))
	for id := range s.inFlight {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// HeldRegions returns the currently-held region token set, sorted.
// Scenario test helper.
func (s *Store) HeldRegions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.regionHolders))
	for tok := range s.regionHolders {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// NamedLockCount returns the holder count for a named lock. Scenario
// test helper for asserting Acquire / ReleaseNamedLock pairs.
func (s *Store) NamedLockCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lockHeld[name]
}

// takeInFlight removes and returns the payload for the given claim_id
// from the in-flight set. Returns ok=false if not present. Caller must
// hold s.mu.
func (s *Store) takeInFlight(claimID string) (any, bool) {
	payload, ok := s.inFlight[claimID]
	if !ok {
		return nil, false
	}
	delete(s.inFlight, claimID)
	return payload, true
}

// mintClaimID returns a fresh synthetic claim ID. Caller must hold s.mu.
// Format is "stub-claim-N" where N is a monotonic counter local to this
// store; deterministic across runs given the same seeding sequence.
func (s *Store) mintClaimID() string {
	s.nextClaimSeq++
	return "stub-claim-" + strconv.Itoa(s.nextClaimSeq)
}

// regionsCtxKey is the unexported context key under which scenario test
// runners stash resolved write/read regions for OpenHandle. Mirrors the
// filesystem store's pattern.
type regionsCtxKey struct{}

type regionsCtxValue struct {
	write []string
	read  []string
}

// WithRegions attaches resolved write and read regions to the context.
// Scenario test helper that mirrors filesystem.WithRegions.
func WithRegions(ctx context.Context, write, read []string) context.Context {
	return context.WithValue(ctx, regionsCtxKey{}, regionsCtxValue{write: write, read: read})
}

func regionsFromContext(ctx context.Context) (write, read []string) {
	v, ok := ctx.Value(regionsCtxKey{}).(regionsCtxValue)
	if !ok {
		return nil, nil
	}
	return v.write, v.read
}

// handleDataCtxKey is the unexported context key under which scenario
// test runners stash payload/claim_id for claim-mode OpenHandle.
// Mirrors claimstorepg.WithHandleData.
type handleDataCtxKey struct{}

type handleDataCtxValue struct {
	payload any
	claimID string
}

// WithHandleData attaches a claim payload + claim ID to the context.
// Scenario test helper that mirrors claimstorepg.WithHandleData.
func WithHandleData(ctx context.Context, payload any, claimID string) context.Context {
	return context.WithValue(ctx, handleDataCtxKey{}, handleDataCtxValue{payload: payload, claimID: claimID})
}

func handleDataFromContext(ctx context.Context) (any, string) {
	v, ok := ctx.Value(handleDataCtxKey{}).(handleDataCtxValue)
	if !ok {
		return nil, ""
	}
	return v.payload, v.claimID
}
