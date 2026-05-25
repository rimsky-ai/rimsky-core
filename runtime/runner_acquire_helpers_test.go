// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Same-package unit tests for the runner_acquire helpers. The
// production-bug regression pin lives here as a deterministic,
// harness-free check that the recursion-guard in
// `acquireFanOutIfDeclared` short-circuits non-root acquisitions.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
)

// scopeOnlyPersist is a minimal persistence.Tables surface that only
// honors RunScopes(). Every other accessor returns nil. Used by the
// child-run-short-circuit tests below so that
// `acquireFanOutIfDeclared`'s `args.Persist.RunScopes().GetByID(...)`
// resolves without touching any other infrastructure.
type scopeOnlyPersist struct {
	scopes persistence.RunScopeTable
}

func (p *scopeOnlyPersist) Templates() persistence.TemplateTable       { return nil }
func (p *scopeOnlyPersist) TemplateTags() persistence.TemplateTagTable { return nil }
func (p *scopeOnlyPersist) Instances() persistence.InstanceTable       { return nil }
func (p *scopeOnlyPersist) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return nil
}
func (p *scopeOnlyPersist) Nodes() persistence.NodeTable                              { return nil }
func (p *scopeOnlyPersist) ClaimHandles() persistence.ClaimHandleTable                { return nil }
func (p *scopeOnlyPersist) NodeAttributes() persistence.NodeAttributeTable            { return nil }
func (p *scopeOnlyPersist) ClaimHolders() persistence.ClaimHolderTable                { return nil }
func (p *scopeOnlyPersist) Events() persistence.EventTable                            { return nil }
func (p *scopeOnlyPersist) Supervisors() persistence.SupervisorTable                  { return nil }
func (p *scopeOnlyPersist) Frames() persistence.FrameTable                            { return nil }
func (p *scopeOnlyPersist) BlobOrphans() persistence.BlobOrphanTable                  { return nil }
func (p *scopeOnlyPersist) NodeEvents() persistence.NodeEventTable                    { return nil }
func (p *scopeOnlyPersist) WaitSet() persistence.WaitSetTable                         { return nil }
func (p *scopeOnlyPersist) Messages() persistence.MessagesTable                       { return nil }
func (p *scopeOnlyPersist) MessageIdempotencies() persistence.MessageIdempotencyTable { return nil }
func (p *scopeOnlyPersist) Lineage() persistence.LineageTable                         { return nil }
func (p *scopeOnlyPersist) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return nil
}
func (p *scopeOnlyPersist) RunTree() persistence.RunTreeTable        { return nil }
func (p *scopeOnlyPersist) RunScopes() persistence.RunScopeTable     { return p.scopes }
func (p *scopeOnlyPersist) APIKeys() persistence.APIKeyTable         { return nil }
func (p *scopeOnlyPersist) Breakpoints() persistence.BreakpointTable { return nil }
func (p *scopeOnlyPersist) BreakpointHits() persistence.BreakpointHitTable {
	return nil
}

func (p *scopeOnlyPersist) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

// staticScopeTable is a RunScopeTable that returns a single canned row
// for GetByID. The other accessors are unused by these tests.
type staticScopeTable struct {
	rows map[shared.UUID]*persistence.RunScopeRow
}

func (s *staticScopeTable) Create(_ context.Context, _ persistence.Tx, row persistence.RunScopeRow) error {
	if s.rows == nil {
		s.rows = make(map[shared.UUID]*persistence.RunScopeRow)
	}
	s.rows[row.ID] = &row
	return nil
}

func (s *staticScopeTable) GetByID(_ context.Context, _ persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	if r, ok := s.rows[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, nil
}

func (s *staticScopeTable) GetFanoutPartition(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (*persistence.RunScopeRow, error) {
	return nil, nil
}
func (s *staticScopeTable) Close(_ context.Context, _ persistence.Tx, _ shared.UUID) error {
	return nil
}
func (s *staticScopeTable) ListChildScopes(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}
func (s *staticScopeTable) ListParentChain(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}

// TestAcquireFanOutIfDeclared_ChildRunsSkipSplitScope is the regression
// pin for the fan-out recursion bug: children of a fan-out parent
// re-use the parent's `node_id` (per
// `runtime/fanout_dispatch.go::PlanFanOutChildren`) and therefore
// inherit the same `nodeDef.FanOut`. Without the run-scope-has-parent
// guard, every child re-fires `SplitScope` and creates grand-children:
// a fan-out of 3 partition keys produces 3 → 9 → 27 → … runs without
// bound.
//
// The test invokes `acquireFanOutIfDeclared` directly with the
// child-run shape (acquisition.RunScopeID resolves to a RunScopeRow with
// a non-nil ParentRunID) plus a `nodeDef` that declares fan_out. The
// guard MUST return nil without consulting the store registry —
// `args.StoreRegistry` is left intentionally nil so any call into the
// registry would panic, surfacing a regression as an unmistakable
// failure.
//
// @concept: fan-out
func TestAcquireFanOutIfDeclared_ChildRunsSkipSplitScope(t *testing.T) {
	t.Parallel()
	parentRun := shared.UUID{1, 2, 3}
	scopeID := shared.UUID{9, 9, 9}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:           scopeID,
		ParentRunID:  &parentRun, // <-- this marks the scope as a fan-out partition
		PartitionKey: "a",
		GraphName:    "main",
	})
	out := &acquisition{RunScopeID: scopeID}
	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-leaf",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{"partition_keys": ["a", "b", "c"]}`,
			ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
		},
	}
	// args.StoreRegistry intentionally nil — if the guard fails to
	// short-circuit, the helper will reach into the registry to call
	// SplitScope and crash. That crash is the regression signal.
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		out,
		persistence.Candidate{},
		nodeDef,
		nil, // acquiredLocks
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("expected nil error for child-run short-circuit; got %v", err)
	}
	if out.SubClaims != nil {
		t.Errorf("expected no SubClaims populated on child runs; got %d entries",
			len(out.SubClaims))
	}
}

// TestAcquireFanOutIfDeclared_RootRunWithoutMatchingAliasIsNoOp covers
// the complementary path: a root run (RunScopeID resolves to a scope
// with nil ParentRunID) whose nodeDef declares fan_out but whose
// `acquiredLocks` does NOT contain the referenced alias. The helper
// returns nil (best-effort safe) so the caller proceeds without
// sub-claims. Pinned here as a guard against the recursion-guard
// accidentally catching root runs.
func TestAcquireFanOutIfDeclared_RootRunWithoutMatchingAliasIsNoOp(t *testing.T) {
	t.Parallel()
	scopeID := shared.UUID{8, 8, 8}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:        scopeID,
		GraphName: "main",
		// ParentRunID nil — main RunScope, root run.
	})
	out := &acquisition{RunScopeID: scopeID}
	nodeDef := &node.TemplateNodeDef{
		Type: "fan-root",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{"partition_keys": ["a"]}`,
			ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
		},
	}
	// No acquiredLocks → no parent claim to split → helper bails with
	// nil (validator catches this case at template-deploy time; the
	// runtime path is best-effort safe).
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		out,
		persistence.Candidate{},
		nodeDef,
		nil, // acquiredLocks — no parent claim
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("expected nil error for root run with no matching alias; got %v", err)
	}
	if out.SubClaims != nil {
		t.Errorf("expected no SubClaims; got %d entries", len(out.SubClaims))
	}
}

// TestAcquireFanOutIfDeclared_NoFanOutSpecIsNoOp pins the baseline
// "node does not declare fan_out" short-circuit. The helper returns
// nil immediately regardless of RunScope shape.
func TestAcquireFanOutIfDeclared_NoFanOutSpecIsNoOp(t *testing.T) {
	t.Parallel()
	// Two scopes: one root, one child — both must short-circuit.
	rootScopeID := shared.UUID{1}
	childScopeID := shared.UUID{2}
	parentRun := shared.UUID{3}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{ID: rootScopeID, GraphName: "main"})
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID: childScopeID, ParentRunID: &parentRun, PartitionKey: "a", GraphName: "main",
	})
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	for _, scopeID := range []shared.UUID{rootScopeID, childScopeID} {
		out := &acquisition{RunScopeID: scopeID}
		nodeDef := &node.TemplateNodeDef{
			Type: "plain-node", Executor: "stub",
		}
		err := acquireFanOutIfDeclared(
			context.Background(),
			args,
			(persistence.Tx)(nil),
			out,
			persistence.Candidate{},
			nodeDef,
			nil,
			30*time.Second,
		)
		if err != nil {
			t.Fatalf("scope=%v: expected nil error; got %v", scopeID, err)
		}
	}
}
