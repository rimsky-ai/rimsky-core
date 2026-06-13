// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// subgraph_exit_carry_empty_test.go — pins the EMPTY-attribute sub-graph
// exit through the RUNNER wrapper (`applyTerminalCompleteSubgraphExit`,
// the same call the runner terminal path makes in
// `runner_terminal.go::applyTerminalComplete`). An exit that terminates
// with no attribute map must still run the FULL settlement minus the
// attribute upsert: the sub-graph RunScope closes and the
// `subgraph.exit_carry` forensics event is emitted. Early-returning on
// the empty map (the pre-fix `if len(merged) == 0 { return nil }`)
// skipped `SettleChildren` entirely and leaked the scope open — the
// defect class this test pins closed.
//
// Lives in `package runtime` so it can construct the unexported
// `acquisition` value the wrapper consumes; the non-empty carry shapes
// are covered against the `SettleChildren` primitive in
// `test/scenarios/subgraph/exit_carry_rule_test.go`.

package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestApplyTerminalCompleteSubgraphExit_EmptyAttributes_ClosesScopeAndEmitsCarry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	// Seed the persisted sub-graph shape the carry-rule fires against: a
	// calling-node parent run in the instance's main RunScope plus an
	// exit leaf run inside a child RunScope whose parent_run_id points
	// back at the parent run. Mirrors the fixture in
	// test/scenarios/subgraph/exit_carry_rule_test.go::makeFixture.
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	callerNodeID := shared.UUID(uuid.New())
	exitNodeID := shared.UUID(uuid.New())
	parentRunID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	exitRunID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:                "exit-carry-empty-fixture",
		Version:             "1",
		FrameResolutionMode: tmplspec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []tmplspec.TemplateNodeDef{
			{Type: "caller", Delegate: "inner"},
			{Type: "inner-exit", Executor: "test-executor"},
		},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: callerNodeID, InstanceID: instanceID,
			NodeType: "caller",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: exitNodeID, InstanceID: instanceID,
			NodeType: "inner-exit", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		frameID, err := tables.Frames().EnqueueSerialFrame(ctx, instanceID, callerNodeID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := tables.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		if err := tables.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID: parentRunID, NodeID: callerNodeID, FrameID: frameID,
			RunScopeID: mainScopeID,
		}); err != nil {
			return err
		}
		parentRunIDCopy := parentRunID
		mainScopeIDCopy := mainScopeID
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               exitScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentRunID:      &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       instanceID,
		}); err != nil {
			return err
		}
		return tables.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID: exitRunID, NodeID: exitNodeID, FrameID: frameID,
			RunScopeID: exitScopeID, ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-exit-carry-empty",
	}
	acq := &acquisition{
		DispatchID: exitRunID,
		NodeID:     exitNodeID,
		InstanceID: instanceID,
		NodeType:   "inner-exit",
		Executor:   "test-executor",
		GraphName:  "inner",
		RunScopeID: exitScopeID,
		NodeDef:    &node.TemplateNodeDef{Type: "inner-exit", Executor: "test-executor", IsSubgraphExit: true},
	}

	// Drive the runner wrapper with an EMPTY attribute map — the exact
	// shape an exit produces when its executor terminates with no
	// writeback. The wrapper encodes a nil Writeback and the primitive
	// must run the full settlement minus the attribute upsert.
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return applyTerminalCompleteSubgraphExit(ctx, args, acq, map[string]any{}, tx)
	}); err != nil {
		t.Fatalf("applyTerminalCompleteSubgraphExit (empty attributes): %v", err)
	}

	// (a) The sub-graph RunScope is CLOSED — the empty carry must not
	// leak the child execution context open.
	var scope *persistence.RunScopeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		scope, err = tables.RunScopes().GetByID(ctx, tx, exitScopeID)
		return err
	}); err != nil {
		t.Fatalf("load exit scope: %v", err)
	}
	if scope == nil || scope.ClosedAt == nil {
		t.Errorf("sub-graph RunScope not closed after empty-attribute exit settlement")
	}

	// (b) The `subgraph.exit_carry` forensics event is emitted.
	var res persistence.EventListResult
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		res, err = tables.Events().List(ctx,
			persistence.EventListFilter{InstanceID: &instanceID},
			persistence.ListPagination{Limit: 50}, tx)
		return err
	}); err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range res.Events {
		if e.Kind == events.KindSubgraphExitCarry() {
			found = true
		}
	}
	if !found {
		t.Errorf("no subgraph.exit_carry event after empty-attribute exit settlement")
	}

	// The parent run's writeback row stays empty — the empty carry skips
	// ONLY the attribute upsert.
	var attrs *persistence.NodeAttributesRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		attrs, err = tables.NodeAttributes().GetByRun(ctx, parentRunID, tx)
		return err
	}); err != nil {
		t.Fatalf("load parent attributes: %v", err)
	}
	if attrs != nil && len(attrs.Data) > 0 {
		t.Errorf("parent writeback row should remain empty on empty-attribute carry, got %v", attrs.Data)
	}
}
