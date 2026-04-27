package stub

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/fallguy/rimsky/core/store"
)

// TestFactory_Build_FilesystemDefaults verifies the filesystem-shaped
// stub builds with default direct write_semantics.
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
	if got := s.Capabilities().WriteSemantics; got != store.WriteSemanticsDirect {
		t.Fatalf("WriteSemantics = %q, want direct", got)
	}
}

// TestFactory_Build_PostgresWithPickPolicies builds a postgres-shaped
// stub with two configured pick policies.
func TestFactory_Build_PostgresWithPickPolicies(t *testing.T) {
	t.Parallel()

	s, err := PostgresFactory().Build("topics", map[string]any{
		"pick_policies": map[string]any{
			"@queue": map[string]any{
				"on_commit_default":  "delete",
				"on_give_up_default": "release_to_head",
			},
			"@ring": map[string]any{
				"on_commit_default":  "release_to_back",
				"on_give_up_default": "release_to_back",
			},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.Kind() != KindPostgres {
		t.Fatalf("Kind = %q, want %q", s.Kind(), KindPostgres)
	}
	stub := s.(*Store)
	if stub.QueueLen("@queue") != 0 {
		t.Fatalf("QueueLen(@queue) = %d, want 0 (no seeded items)", stub.QueueLen("@queue"))
	}
	if stub.QueueLen("@ring") != 0 {
		t.Fatalf("QueueLen(@ring) = %d, want 0", stub.QueueLen("@ring"))
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
			name:    "non-string write_semantics",
			factory: FilesystemFactory(),
			cfg:     map[string]any{"write_semantics": 42},
		},
		{
			name:    "non-map pick_policies",
			factory: PostgresFactory(),
			cfg:     map[string]any{"pick_policies": "abc"},
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

	mk := func(elems ...string) []byte {
		b, _ := json.Marshal(elems)
		return b
	}
	if s.RegionsConflict(mk("a"), mk("b")) {
		t.Fatalf("disjoint regions reported conflict")
	}
	if !s.RegionsConflict(mk("a", "b"), mk("b", "c")) {
		t.Fatalf("overlapping regions reported no conflict")
	}
	// Wrong-shape input must fail closed.
	if !s.RegionsConflict([]byte(`not json`), mk("a")) {
		t.Fatalf("wrong-shape input should fail closed")
	}
}

// TestStore_UnmarshalRegion verifies the round-trip is a copy.
func TestStore_UnmarshalRegion(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	got, err := s.UnmarshalRegion([]byte(`["x","y"]`))
	if err != nil {
		t.Fatalf("UnmarshalRegion: %v", err)
	}
	if string(got) != `["x","y"]` {
		t.Fatalf("got %q, want unchanged input", got)
	}
}

// TestStore_Open_RegionDirect verifies that a non-policy selector echoes
// straight through Open as Address and Region.
func TestStore_Open_RegionDirect(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	cr, err := s.Open(context.Background(), store.ClaimSpec{
		StoreName: "content",
		Selector:  "/data/reports/q1",
		Intent:    store.IntentReadWrite,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(cr.Address) != `"/data/reports/q1"` {
		t.Fatalf("Address = %q, want %q", cr.Address, `"/data/reports/q1"`)
	}
	if string(cr.Region) != `"/data/reports/q1"` {
		t.Fatalf("Region = %q, want %q", cr.Region, `"/data/reports/q1"`)
	}
}

// TestStore_Open_PickPolicy_FIFO seeds two items on a queue policy and
// verifies Open pops the head.
func TestStore_Open_PickPolicy_FIFO(t *testing.T) {
	t.Parallel()

	s := mustPostgresStub(t)
	s.ConfigurePickPolicy("@queue", "delete", "release_to_head")
	id1, err := s.SeedPickPolicyItem("@queue", json.RawMessage(`{"task":"first"}`))
	if err != nil {
		t.Fatalf("SeedPickPolicyItem: %v", err)
	}
	id2, err := s.SeedPickPolicyItem("@queue", json.RawMessage(`{"task":"second"}`))
	if err != nil {
		t.Fatalf("SeedPickPolicyItem: %v", err)
	}

	ctx := context.Background()
	cr1, err := s.Open(ctx, store.ClaimSpec{
		StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite,
	})
	if err != nil {
		t.Fatalf("Open #1: %v", err)
	}
	if string(cr1.Region) != `"`+id1+`"` {
		t.Fatalf("Open #1 region = %q, want first id %q", cr1.Region, id1)
	}
	cr2, err := s.Open(ctx, store.ClaimSpec{
		StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite,
	})
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if string(cr2.Region) != `"`+id2+`"` {
		t.Fatalf("Open #2 region = %q, want second id %q", cr2.Region, id2)
	}
	// Empty queue → zero ClaimResult.
	cr3, err := s.Open(ctx, store.ClaimSpec{
		StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite,
	})
	if err != nil {
		t.Fatalf("Open #3: %v", err)
	}
	if cr3.Address != nil || cr3.Region != nil {
		t.Fatalf("Open(empty queue) = %+v, want zero ClaimResult", cr3)
	}
}

// TestStore_Commit_PickPolicy_DeleteAction removes the in-flight item
// without re-queueing.
func TestStore_Commit_PickPolicy_DeleteAction(t *testing.T) {
	t.Parallel()

	s := mustPostgresStub(t)
	s.ConfigurePickPolicy("@queue", "delete", "release_to_head")
	id, _ := s.SeedPickPolicyItem("@queue", json.RawMessage(`{"x":1}`))
	ctx := context.Background()
	cr, err := s.Open(ctx, store.ClaimSpec{StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Commit(ctx, cr.Region, cr.Address, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := s.InFlight("@queue"); len(got) != 0 {
		t.Fatalf("InFlight after commit-delete = %v, want empty", got)
	}
	if got := s.QueueLen("@queue"); got != 0 {
		t.Fatalf("QueueLen after commit-delete = %d, want 0", got)
	}
	_ = id
}

// TestStore_Abandon_PickPolicy_ReleaseToHead returns the item to the
// front of the queue.
func TestStore_Abandon_PickPolicy_ReleaseToHead(t *testing.T) {
	t.Parallel()

	s := mustPostgresStub(t)
	s.ConfigurePickPolicy("@queue", "delete", "release_to_head")
	idA, _ := s.SeedPickPolicyItem("@queue", json.RawMessage(`"a"`))
	idB, _ := s.SeedPickPolicyItem("@queue", json.RawMessage(`"b"`))
	ctx := context.Background()
	cr, err := s.Open(ctx, store.ClaimSpec{StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Abandon(ctx, cr.Region, cr.Address, ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if s.QueueLen("@queue") != 2 {
		t.Fatalf("QueueLen after abandon = %d, want 2 (item back at head)", s.QueueLen("@queue"))
	}
	// Next open returns idA again (was at head).
	cr2, err := s.Open(ctx, store.ClaimSpec{StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite})
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if string(cr2.Region) != `"`+idA+`"` {
		t.Fatalf("Open #2 region = %q, want idA %q (release_to_head)", cr2.Region, idA)
	}
	_ = idB
}

// TestStore_Recorder verifies all five verbs append to the call recorder.
func TestStore_Recorder(t *testing.T) {
	t.Parallel()

	s := mustFilesystemStub(t)
	ctx := context.Background()
	if _, err := s.Open(ctx, store.ClaimSpec{StoreName: "content", Selector: "/x", Intent: store.IntentRead}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Commit(ctx, []byte(`"/x"`), []byte(`"/x"`), ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.Abandon(ctx, []byte(`"/x"`), []byte(`"/x"`), ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := s.Delete(ctx, []byte(`"/x"`)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Release(ctx, []byte(`"/x"`), []byte(`"/x"`)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	calls := s.Calls()
	if len(calls) != 5 {
		t.Fatalf("Calls len = %d, want 5", len(calls))
	}
	wantVerbs := []string{"open", "commit", "abandon", "delete", "release"}
	for i, c := range calls {
		if c.Verb != wantVerbs[i] {
			t.Fatalf("Calls[%d].Verb = %q, want %q", i, c.Verb, wantVerbs[i])
		}
	}
}

// TestStore_ConcurrentOpen_PickPolicy verifies the mutex serialises
// concurrent Open calls — no item handed out twice.
func TestStore_ConcurrentOpen_PickPolicy(t *testing.T) {
	t.Parallel()

	const N = 50
	s := mustPostgresStub(t)
	s.ConfigurePickPolicy("@queue", "delete", "release_to_head")
	for i := 0; i < N; i++ {
		if _, err := s.SeedPickPolicyItem("@queue", json.RawMessage(`null`)); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}
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
			cr, err := s.Open(ctx, store.ClaimSpec{StoreName: "topics", Selector: "@queue", Intent: store.IntentReadWrite})
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if _, dup := seen[string(cr.Region)]; dup {
				t.Errorf("region %s handed out twice", cr.Region)
			}
			seen[string(cr.Region)] = struct{}{}
		}()
	}
	wg.Wait()
	if len(seen) != N {
		t.Fatalf("distinct regions = %d, want %d", len(seen), N)
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

func mustPostgresStub(t *testing.T) *Store {
	t.Helper()
	s, err := PostgresFactory().Build("topics", map[string]any{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s.(*Store)
}
