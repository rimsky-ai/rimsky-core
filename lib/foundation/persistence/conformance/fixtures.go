// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package conformance provides shared fixtures for the persistence
// conformance test suite. Each helper takes a persistence.Database so
// it works against both Postgres and SQLite without driver-specific
// cruft.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
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

// fixtureSet holds the IDs returned by seedFixtureSet for tests to
// reference across subsequent enqueue / claim / lookup operations.
// MessageID is the seeded triggering message that opened the fixture
// frame; tests that enqueue further frames re-use it as the
// triggering-message satisfaction of the
// rimsky_frames.triggering_message_id NOT NULL FK (every frame carries
// an originating message under the typed-message schema layer).
type fixtureSet struct {
	TemplateHash   string
	InstanceID     shared.UUID
	NodeID         shared.UUID
	FrameID        shared.UUID
	MainRunScopeID shared.UUID
	MessageID      shared.UUID
}

// seedFixtureSet creates the minimum chain of rows needed to satisfy
// the FK chain rimsky_node_runs -> rimsky_nodes -> rimsky_instances ->
// rimsky_templates AND rimsky_node_runs -> rimsky_frames. Returns the
// fixtureSet ids for tests to enqueue against. Tests can call this
// once per Driver instance and reuse the returned IDs across
// enqueue/claim operations.
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
		Name:           "conformance-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
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
		// @constraint: rimsky_instances.main_run_scope_id has an FK to
		// rimsky_run_scopes(id), so the RunScope row must exist before
		// Instances.Create can commit. Per @concept: run-scope.
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

	// @deliberate: production code lets the frame engine produce frames
	// from delivered messages; fixtures bypass that and call Messages() +
	// Frames() directly to seed a typed-message envelope (satisfying the
	// rimsky_frames.triggering_message_id NOT NULL FK), enqueue, then
	// promote, so dispatch rows can FK against a 'running' frame without
	// scheduling a real engine pass.
	messageID := uuid.New()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(messageID),
			InstanceID: shared.UUID(instanceID),
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertFrame(ctx, instanceID, shared.UUID(messageID), 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		// @constraint: dispatch rows FK against the frame in 'running' state;
		// promoting here avoids contending with a real frame-engine sweep.
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
		MessageID:      shared.UUID(messageID),
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

	// @deliberate: the fixture set is the only producer of instances in
	// conformance tests, so node -> instance -> main_run_scope_id is
	// guaranteed to resolve; no defensive null-handling on the chain.
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
