// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// fixtures.go — shared seed helpers for the cross-driver conformance
// suite. Each helper takes a persistence.Database so it works against both
// Postgres and SQLite without driver-specific cruft.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// seedMainRunScopeForInstance creates a main RunScope row for the
// given instance id and returns its id. Convenience helper for tests
// that exercise Instances.Create directly (and therefore need to
// satisfy the rimsky_instances.main_run_scope_id NOT NULL FK before
// the tx commits). Per concept:run-scope every instance has exactly
// one main RunScope rooted at the top of the run-tree. Idempotent
// across re-invocations only at distinct instance ids.
func seedMainRunScopeForInstance(
	ctx context.Context, t *testing.T, tx persistence.Tx, store persistence.Tables,
	instanceID shared.UUID,
) shared.UUID {
	t.Helper()
	id := shared.UUID(uuid.New())
	if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
		ID:         id,
		GraphName:  spec.MainGraphName,
		InstanceID: instanceID,
	}); err != nil {
		t.Fatalf("seedMainRunScopeForInstance: %v", err)
	}
	return id
}

// seedFixtureSet creates the minimum chain of rows needed to satisfy
// the FK chain rimsky_node_runs -> rimsky_nodes -> rimsky_instances ->
// rimsky_templates AND rimsky_node_runs -> rimsky_frames. Returns the
// (nodeID, frameID) pair for tests to enqueue against.
//
// The template carries frame_resolution = "serial_queue" + a node-typed
// definition that matches the inserted node row's node_type. Tests
// can call this once per Driver instance and reuse the returned IDs
// across enqueue/claim operations.
type fixtureSet struct {
	TemplateHash   string
	InstanceID     shared.UUID
	NodeID         shared.UUID
	FrameID        shared.UUID
	MainRunScopeID shared.UUID
}

func seedFixtureSet(ctx context.Context, t *testing.T, d persistence.Database) fixtureSet {
	t.Helper()
	store := d.Tables()
	if store == nil {
		t.Fatalf("seedFixtureSet: driver.Tables() returned nil")
	}

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	nodeID := uuid.New()
	frameID := uuid.New()
	mainRunScopeID := uuid.New()

	tmplSpec := spec.TemplateSpec{
		Name:                "conformance-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplSpec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		// Allocate the main RunScope first — rimsky_instances.main_run_scope_id
		// has an FK to rimsky_run_scopes(id). Per concept:run-scope.
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:           shared.UUID(mainRunScopeID),
			GraphName:    spec.MainGraphName,
			InstanceID:   shared.UUID(instanceID),
			PartitionKey: "",
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: shared.UUID(mainRunScopeID),
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seedFixtureSet: template/instance/node create: %v", err)
	}

	// Create a frame in 'queued' state then promote to 'running' so the
	// dispatch FK is satisfiable. The frame engine produces frames itself
	// in production; for fixture seeding we go through Frames() directly.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := store.Frames().EnqueueSerialFrame(ctx, instanceID, nodeID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		// Promote to 'running' so dispatch rows can FK against it without
		// surprising a frame-engine sweep.
		if _, err := store.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedFixtureSet: frame enqueue/promote: %v", err)
	}

	return fixtureSet{
		TemplateHash:   templateHash,
		InstanceID:     instanceID,
		NodeID:         nodeID,
		FrameID:        frameID,
		MainRunScopeID: mainRunScopeID,
	}
}

// seedConformanceRunForNode enqueues a pending `rimsky_node_runs` row
// for the given node + frame in the fixture's main RunScope and returns
// the run id. Post-stage-5 of the run-row lifecycle cutover, claim-holders
// / wait-set rows key on run id, so fixture-driven seeds that exercise
// those tables need a real run id. Per concept:run-scope, every dispatch
// row carries a non-null `run_scope_id`; this helper sources it from the
// fixture set's MainRunScopeID. Lives alongside the fixture-set helpers
// so individual conformance areas (fk, wait_set, etc.) can enqueue runs
// without every fixture seed paying the cost.
//
// @concept: run-scope
func seedConformanceRunForNode(
	ctx context.Context, t *testing.T, d persistence.Database,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	store := d.Tables()
	q := d.Queue()

	// Resolve the instance's main RunScope from the node row. The fixture
	// set is the only thing that creates instances in conformance tests,
	// so the FK chain guarantees this resolves.
	var runScopeID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodeRow, err := store.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		if nodeRow == nil {
			t.Fatalf("seedConformanceRunForNode: node %s not found", nodeID)
		}
		instRow, err := store.Instances().Get(ctx, nodeRow.InstanceID, tx)
		if err != nil {
			return err
		}
		if instRow == nil {
			t.Fatalf("seedConformanceRunForNode: instance %s not found", nodeRow.InstanceID)
		}
		runScopeID = instRow.MainRunScopeID
		return nil
	}); err != nil {
		t.Fatalf("seedConformanceRunForNode: resolve run scope: %v", err)
	}

	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         nodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        frameID,
			RunScopeID:     runScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				runID = c.DispatchID
				return nil
			}
		}
		t.Fatalf("seedConformanceRunForNode: candidate not surfaced for %s", nodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedConformanceRunForNode: %v", err)
	}
	return runID
}

// inTx wraps fn in a fresh Persist.Transaction for use in test-helper
// reads under option C (every Store method requires an explicit tx).
func inTx(ctx context.Context, store persistence.Tables, fn func(tx persistence.Tx) error) error {
	return store.Transaction(ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	})
}
