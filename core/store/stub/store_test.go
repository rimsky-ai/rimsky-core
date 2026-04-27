package stub

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/fallguy/rimsky/core/store"
)

// TestFactory_Build_FilesystemDefaults checks the canonical filesystem-
// stub config: no explicit caps overrides, defaults applied.
func TestFactory_Build_FilesystemDefaults(t *testing.T) {
	t.Parallel()

	s, err := FilesystemFactory().Build("content", map[string]any{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.Name() != "content" {
		t.Fatalf("Name = %q, want %q", s.Name(), "content")
	}
	if s.Kind() != KindFilesystem {
		t.Fatalf("Kind = %q, want %q", s.Kind(), KindFilesystem)
	}
	want := store.Capabilities{
		SupportsRegionLock: true,
		SupportsClaim:      false,
		SupportsDiscard:    false,
		SupportsResume:     true,
		SupportsRestore:    false,
	}
	if got := s.Capabilities(); got != want {
		t.Fatalf("Capabilities = %+v, want %+v", got, want)
	}
}

// TestFactory_Build_ClaimStoreDefaults_SeedItems checks the claim-store
// stub kind with seed items in cfg.
func TestFactory_Build_ClaimStoreDefaults_SeedItems(t *testing.T) {
	t.Parallel()

	s, err := ClaimStoreFactory().Build("topics", map[string]any{
		"initial_items": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.Kind() != KindClaimStore {
		t.Fatalf("Kind = %q, want %q", s.Kind(), KindClaimStore)
	}
	want := store.Capabilities{
		SupportsRegionLock: false,
		SupportsClaim:      true,
		SupportsDiscard:    true,
		SupportsResume:     true,
		SupportsRestore:    false,
	}
	if got := s.Capabilities(); got != want {
		t.Fatalf("Capabilities = %+v, want %+v", got, want)
	}
	stub := s.(*Store)
	if stub.QueueLen() != 3 {
		t.Fatalf("QueueLen = %d, want 3", stub.QueueLen())
	}
}

// TestFactory_Build_CapabilityOverrides covers the cfg overrides path —
// flipping individual flags to model edge-case stores.
func TestFactory_Build_CapabilityOverrides(t *testing.T) {
	t.Parallel()

	s, err := FilesystemFactory().Build("custom", map[string]any{
		"supports_region_lock": false,
		"supports_resume":      false,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	caps := s.Capabilities()
	if caps.SupportsRegionLock {
		t.Fatalf("SupportsRegionLock = true, want false (override)")
	}
	if caps.SupportsResume {
		t.Fatalf("SupportsResume = true, want false (override)")
	}
}

// TestFactory_Build_RejectsBadConfig validates the few error branches.
func TestFactory_Build_RejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		factory Factory
		cfg     map[string]any
	}{
		{
			name:    "non-bool override",
			factory: FilesystemFactory(),
			cfg:     map[string]any{"supports_region_lock": "yes"},
		},
		{
			name:    "non-slice initial_items",
			factory: ClaimStoreFactory(),
			cfg:     map[string]any{"initial_items": "abc"},
		},
		{
			name:    "unknown kind",
			factory: Factory{StubKind: "bogus"},
			cfg:     map[string]any{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.factory.Build("x", tc.cfg); err == nil {
				t.Fatalf("Build: expected error, got nil")
			}
		})
	}
}

// TestStore_RegionsConflict_TokenSet verifies the simple set-membership
// conflict semantics: two regions conflict iff they share any token.
func TestStore_RegionsConflict_TokenSet(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)

	if s.RegionsConflict([]string{"a"}, []string{"b"}) {
		t.Fatalf("disjoint regions reported conflict")
	}
	if !s.RegionsConflict([]string{"a", "b"}, []string{"b", "c"}) {
		t.Fatalf("overlapping regions reported no conflict")
	}
	// Wrong-typed inputs must fail closed.
	if !s.RegionsConflict("not-a-slice", []string{"a"}) {
		t.Fatalf("wrong-type input should fail closed")
	}
}

// TestStore_UnmarshalRegion verifies the JSONB → []string round-trip.
func TestStore_UnmarshalRegion(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	got, err := s.UnmarshalRegion([]byte(`["x","y"]`))
	if err != nil {
		t.Fatalf("UnmarshalRegion: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("got %v, want [x y]", got)
	}
	if _, err := s.UnmarshalRegion([]byte(`{}`)); err == nil {
		t.Fatalf("expected error on object input")
	}
}

// TestStore_RegionLock_AcquireReleaseRoundtrip exercises the full
// region-lock state machine: LockEligible → AcquireLock → second
// LockEligible (now blocked) → ReleaseRegion → LockEligible (clear).
func TestStore_RegionLock_AcquireReleaseRoundtrip(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := context.Background()
	spec := store.RegionLockSpec{
		StoreName: "content",
		Region:    []string{"reports/q1"},
	}

	ok, err := s.LockEligible(ctx, spec)
	if err != nil || !ok {
		t.Fatalf("LockEligible(initial) ok=%v err=%v, want ok=true", ok, err)
	}
	if _, _, err := s.AcquireLock(ctx, spec); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	// Same region: now blocked.
	ok, err = s.LockEligible(ctx, spec)
	if err != nil || ok {
		t.Fatalf("LockEligible(after acquire) ok=%v err=%v, want ok=false", ok, err)
	}

	// Disjoint region: still eligible.
	other := store.RegionLockSpec{StoreName: "content", Region: []string{"reports/q2"}}
	ok, err = s.LockEligible(ctx, other)
	if err != nil || !ok {
		t.Fatalf("LockEligible(disjoint) ok=%v err=%v, want ok=true", ok, err)
	}

	if got := s.HeldRegions(); !reflect.DeepEqual(got, []string{"reports/q1"}) {
		t.Fatalf("HeldRegions = %v, want [reports/q1]", got)
	}

	s.ReleaseRegion([]string{"reports/q1"})
	ok, err = s.LockEligible(ctx, spec)
	if err != nil || !ok {
		t.Fatalf("LockEligible(after release) ok=%v err=%v, want ok=true", ok, err)
	}
}

// TestStore_NamedLock_Counting exercises the counting-semaphore path:
// LockEligible respects Limit; AcquireLock increments; ReleaseNamedLock
// decrements.
func TestStore_NamedLock_Counting(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := context.Background()
	spec := store.NamedLockSpec{Name: "writers", Mode: store.LockModeCounting, Limit: 2}

	for i := 0; i < 2; i++ {
		ok, err := s.LockEligible(ctx, spec)
		if err != nil || !ok {
			t.Fatalf("LockEligible #%d ok=%v err=%v, want ok=true", i, ok, err)
		}
		if _, _, err := s.AcquireLock(ctx, spec); err != nil {
			t.Fatalf("AcquireLock #%d: %v", i, err)
		}
	}

	// At limit — third attempt blocked.
	ok, err := s.LockEligible(ctx, spec)
	if err != nil || ok {
		t.Fatalf("LockEligible(at limit) ok=%v err=%v, want ok=false", ok, err)
	}
	if got := s.NamedLockCount("writers"); got != 2 {
		t.Fatalf("NamedLockCount = %d, want 2", got)
	}

	s.ReleaseNamedLock("writers")
	ok, err = s.LockEligible(ctx, spec)
	if err != nil || !ok {
		t.Fatalf("LockEligible(after release) ok=%v err=%v, want ok=true", ok, err)
	}
}

// TestStore_NamedLock_MutexImplicitLimit confirms the mutex mode uses
// limit=1 regardless of the spec's Limit field.
func TestStore_NamedLock_MutexImplicitLimit(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := context.Background()
	spec := store.NamedLockSpec{Name: "singleton", Mode: store.LockModeMutex}

	if _, _, err := s.AcquireLock(ctx, spec); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	ok, err := s.LockEligible(ctx, spec)
	if err != nil || ok {
		t.Fatalf("LockEligible(mutex held) ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestStore_Claim_AcquireRelease_DeleteInFlight covers the canonical
// claim-mode lifecycle: HasClaimableItem true → AcquireLock pops head →
// in-flight non-empty → DeleteInFlight removes the in-flight row
// directly (modelling the supervisor-driven §5.6.4 DELETE that
// production claim-store-postgres routes through ResolveOnTerminal,
// not through ReleaseClaimItem).
func TestStore_Claim_AcquireRelease_DeleteInFlight(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, []any{"first", "second"})
	ctx := context.Background()

	got, err := s.HasClaimableItem(ctx, nil)
	if err != nil || !got {
		t.Fatalf("HasClaimableItem(initial) got=%v err=%v, want true", got, err)
	}

	_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if cr.Payload != "first" {
		t.Fatalf("payload = %v, want first (FIFO head)", cr.Payload)
	}
	if cr.ClaimID == "" {
		t.Fatalf("ClaimID empty, expected synthetic id")
	}
	if got := s.QueueLen(); got != 1 {
		t.Fatalf("QueueLen = %d, want 1", got)
	}
	if got := s.InFlight(); !reflect.DeepEqual(got, []string{cr.ClaimID}) {
		t.Fatalf("InFlight = %v, want [%s]", got, cr.ClaimID)
	}

	if ok := s.DeleteInFlight(cr.ClaimID); !ok {
		t.Fatalf("DeleteInFlight(%s) = false, want true", cr.ClaimID)
	}
	if got := s.InFlight(); len(got) != 0 {
		t.Fatalf("InFlight after delete = %v, want empty", got)
	}
	if got := s.QueueLen(); got != 1 {
		t.Fatalf("QueueLen after delete = %d, want 1 (only second remains)", got)
	}
}

// TestStore_ReleaseClaimItem_RejectsDeleteActions verifies the stub
// matches production claim-store-postgres in rejecting "delete" and
// "delete_won" — these actions are owned by the §5.6.4 resolution
// algorithm, not ReleaseClaimItem (spec §8.5.1). The in-flight entry
// must be preserved on rejection so the caller's bookkeeping isn't
// silently corrupted.
func TestStore_ReleaseClaimItem_RejectsDeleteActions(t *testing.T) {
	t.Parallel()

	for _, action := range []string{"delete", "delete_won"} {
		action := action
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			s := mustClaimStub(t, []any{"a"})
			ctx := context.Background()
			_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
			if err != nil {
				t.Fatalf("AcquireLock: %v", err)
			}
			if err := s.ReleaseClaimItem(ctx, cr.ClaimID, action); err == nil {
				t.Fatalf("ReleaseClaimItem(%q): expected error, got nil", action)
			}
			// In-flight state must remain so the caller can retry with
			// a valid action / route the DELETE through DeleteInFlight.
			if got := s.InFlight(); !reflect.DeepEqual(got, []string{cr.ClaimID}) {
				t.Fatalf("InFlight after rejected %q = %v, want [%s]", action, got, cr.ClaimID)
			}
		})
	}
}

// TestStore_DeleteInFlight covers the scenario-test helper that models
// the supervisor-driven §5.6.4 DELETE without going through
// ReleaseClaimItem.
func TestStore_DeleteInFlight(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, nil)
	s.SeedInFlight("preset-1", "payload-x")
	if ok := s.DeleteInFlight("preset-1"); !ok {
		t.Fatalf("DeleteInFlight(preset-1) = false, want true")
	}
	if got := s.InFlight(); len(got) != 0 {
		t.Fatalf("InFlight after delete = %v, want empty", got)
	}
	// Second delete is a no-op — returns false rather than erroring.
	if ok := s.DeleteInFlight("preset-1"); ok {
		t.Fatalf("DeleteInFlight(preset-1) on absent id = true, want false")
	}
}

// TestStore_Claim_ReleaseToBack pushes the released item to the tail.
func TestStore_Claim_ReleaseToBack(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, []any{"a", "b"})
	ctx := context.Background()

	_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if cr.Payload != "a" {
		t.Fatalf("first claim payload = %v, want a", cr.Payload)
	}
	if err := s.ReleaseClaimItem(ctx, cr.ClaimID, "release_to_back"); err != nil {
		t.Fatalf("ReleaseClaimItem: %v", err)
	}

	// Next acquire should return "b" (a is now at the tail).
	_, cr2, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock 2: %v", err)
	}
	if cr2.Payload != "b" {
		t.Fatalf("second claim payload = %v, want b (a moved to tail)", cr2.Payload)
	}
}

// TestStore_Claim_ReleaseToHead places the released item ahead of the
// existing head.
func TestStore_Claim_ReleaseToHead(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, []any{"a", "b"})
	ctx := context.Background()

	_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := s.ReleaseClaimItem(ctx, cr.ClaimID, "release_to_head"); err != nil {
		t.Fatalf("ReleaseClaimItem: %v", err)
	}

	// Next acquire returns "a" again.
	_, cr2, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock 2: %v", err)
	}
	if cr2.Payload != "a" {
		t.Fatalf("payload = %v, want a (release_to_head)", cr2.Payload)
	}
}

// TestStore_Claim_AcquireFromEmpty returns a zero ClaimResult and no
// error when the queue has nothing to hand out.
func TestStore_Claim_AcquireFromEmpty(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, nil)
	_, cr, err := s.AcquireLock(context.Background(), store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock(empty queue): %v", err)
	}
	if cr.Payload != nil || cr.ClaimID != "" {
		t.Fatalf("AcquireLock(empty queue) ClaimResult = %+v, want zero", cr)
	}
}

// TestStore_Claim_LockEligibleByCapability ensures a region spec
// against a claim-only stub fails LockEligible (capability mismatch).
func TestStore_Claim_LockEligibleByCapability(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, []any{"x"})
	ctx := context.Background()

	ok, err := s.LockEligible(ctx, store.RegionLockSpec{StoreName: "topics", Region: []string{"a"}})
	if err != nil {
		t.Fatalf("LockEligible(region against claim-only): %v", err)
	}
	if ok {
		t.Fatalf("LockEligible(region against claim-only) = true, want false")
	}

	ok, err = s.LockEligible(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil || !ok {
		t.Fatalf("LockEligible(claim) ok=%v err=%v, want ok=true", ok, err)
	}
}

// TestStore_Claim_AcquireWrongCapability rejects with an error when a
// claim spec is presented against a filesystem-only stub.
func TestStore_Claim_AcquireWrongCapability(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	_, _, err := s.AcquireLock(context.Background(), store.ClaimLockSpec{StoreName: "content"})
	if err == nil {
		t.Fatalf("AcquireLock(claim against region-only): expected error, got nil")
	}
}

// TestStore_OpenHandle_Filesystem verifies the FilesystemDirectHandle
// shape and the regions-from-context threading.
func TestStore_OpenHandle_Filesystem(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := WithRegions(context.Background(), []string{"w/*"}, []string{"r/*"})
	nh, err := s.OpenHandle(ctx, store.LockHandle{}, false)
	if err != nil {
		t.Fatalf("OpenHandle: %v", err)
	}
	fh, ok := nh.(store.FilesystemDirectHandle)
	if !ok {
		t.Fatalf("OpenHandle returned %T, want FilesystemDirectHandle", nh)
	}
	if fh.Path != "content" {
		t.Fatalf("Path = %q, want content", fh.Path)
	}
	if !reflect.DeepEqual(fh.WriteRegions, []string{"w/*"}) {
		t.Fatalf("WriteRegions = %v, want [w/*]", fh.WriteRegions)
	}
	if !reflect.DeepEqual(fh.ReadRegions, []string{"r/*"}) {
		t.Fatalf("ReadRegions = %v, want [r/*]", fh.ReadRegions)
	}
}

// TestStore_OpenHandle_ClaimStore verifies the ClaimStoreHandle shape
// and the payload/claim_id-from-context threading.
func TestStore_OpenHandle_ClaimStore(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, nil)
	ctx := WithHandleData(context.Background(), map[string]any{"k": "v"}, "stub-claim-1")
	nh, err := s.OpenHandle(ctx, store.LockHandle{}, false)
	if err != nil {
		t.Fatalf("OpenHandle: %v", err)
	}
	ch, ok := nh.(store.ClaimStoreHandle)
	if !ok {
		t.Fatalf("OpenHandle returned %T, want ClaimStoreHandle", nh)
	}
	if ch.ClaimID != "stub-claim-1" {
		t.Fatalf("ClaimID = %q, want stub-claim-1", ch.ClaimID)
	}
	if ch.StoreName != "topics" {
		t.Fatalf("StoreName = %q, want topics", ch.StoreName)
	}
	if got, ok := ch.Payload.(map[string]any); !ok || got["k"] != "v" {
		t.Fatalf("Payload = %v, want {k:v}", ch.Payload)
	}
}

// TestStore_Commit_AndReleaseLock covers the no-op return shapes.
func TestStore_Commit_AndReleaseLock(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := context.Background()
	cr, err := s.Commit(ctx, store.LockHandle{})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !cr.Changed {
		t.Fatalf("Commit Changed = false, want true")
	}
	for _, a := range []store.ReleaseAction{
		store.ReleaseCommit, store.ReleaseDiscard, store.ReleaseGiveUp, store.ReleasePreserveResume,
	} {
		if err := s.ReleaseLock(ctx, store.LockHandle{}, a); err != nil {
			t.Fatalf("ReleaseLock(%q): %v", a, err)
		}
	}
}

// TestStore_HasPriorWork_AlwaysFalse documents the v1 simplification.
func TestStore_HasPriorWork_AlwaysFalse(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	got, err := s.HasPriorWork(context.Background(),
		store.RegionLockSpec{StoreName: "content", Region: []string{"a"}})
	if err != nil {
		t.Fatalf("HasPriorWork: %v", err)
	}
	if got {
		t.Fatalf("HasPriorWork = true, want false")
	}
}

// TestStore_ReleaseClaimItem_UnknownClaimID errors loudly.
func TestStore_ReleaseClaimItem_UnknownClaimID(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, nil)
	if err := s.ReleaseClaimItem(context.Background(), "missing-id", "release_to_back"); err == nil {
		t.Fatalf("ReleaseClaimItem(unknown id): expected error")
	}
}

// TestStore_ReleaseClaimItem_UnknownAction errors loudly and restores
// in-flight state so the caller can retry without losing the item.
func TestStore_ReleaseClaimItem_UnknownAction(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, []any{"a"})
	ctx := context.Background()
	_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := s.ReleaseClaimItem(ctx, cr.ClaimID, "bogus"); err == nil {
		t.Fatalf("ReleaseClaimItem(bogus): expected error")
	}
	// In-flight state must remain so the test can retry with a valid
	// action.
	if got := s.InFlight(); !reflect.DeepEqual(got, []string{cr.ClaimID}) {
		t.Fatalf("InFlight after rejected action = %v, want [%s]", got, cr.ClaimID)
	}
	if err := s.ReleaseClaimItem(ctx, cr.ClaimID, "release_to_back"); err != nil {
		t.Fatalf("ReleaseClaimItem(release_to_back) after retry: %v", err)
	}
}

// TestStore_ConcurrentClaim verifies the Mutex actually serialises
// concurrent AcquireLock calls — no item handed out twice.
func TestStore_ConcurrentClaim(t *testing.T) {
	t.Parallel()

	const N = 50
	items := make([]any, N)
	for i := 0; i < N; i++ {
		items[i] = i
	}
	s := mustClaimStub(t, items)
	ctx := context.Background()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make(map[string]struct{}, N)
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cr, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "topics"})
			if err != nil {
				t.Errorf("AcquireLock: %v", err)
				return
			}
			if cr.ClaimID == "" {
				t.Errorf("empty ClaimID")
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if _, dup := seen[cr.ClaimID]; dup {
				t.Errorf("claim id %q handed out twice", cr.ClaimID)
			}
			seen[cr.ClaimID] = struct{}{}
		}()
	}
	wg.Wait()
	if len(seen) != N {
		t.Fatalf("distinct claims = %d, want %d", len(seen), N)
	}
}

// TestStore_SeedInFlight allows tests to inject in-flight rows directly
// for exercising release / resume paths without first running
// AcquireLock.
func TestStore_SeedInFlight(t *testing.T) {
	t.Parallel()

	s := mustClaimStub(t, nil)
	s.SeedInFlight("preset-1", "payload-x")
	if got := s.InFlight(); !reflect.DeepEqual(got, []string{"preset-1"}) {
		t.Fatalf("InFlight = %v, want [preset-1]", got)
	}
	if err := s.ReleaseClaimItem(context.Background(), "preset-1", "release_to_back"); err != nil {
		t.Fatalf("ReleaseClaimItem: %v", err)
	}
	if got := s.QueueLen(); got != 1 {
		t.Fatalf("QueueLen after release = %d, want 1", got)
	}
}

func mustFilesystemStub(t *testing.T) *Store {
	t.Helper()
	s, err := FilesystemFactory().Build("content", map[string]any{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s.(*Store)
}

func mustClaimStub(t *testing.T, items []any) *Store {
	t.Helper()
	cfg := map[string]any{}
	if items != nil {
		cfg["initial_items"] = items
	}
	s, err := ClaimStoreFactory().Build("topics", cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s.(*Store)
}
