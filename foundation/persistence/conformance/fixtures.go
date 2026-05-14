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

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

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
	TemplateHash string
	InstanceID   shared.UUID
	NodeID       shared.UUID
	FrameID      shared.UUID
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

	spec := spec.TemplateSpec{
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
			Spec:   spec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:           nodeID,
			InstanceID:   instanceID,
			NodeType:     "fixture-node-type",
			Executor:     "test-executor",
			Dependencies: []shared.UUID{},
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
		TemplateHash: templateHash,
		InstanceID:   instanceID,
		NodeID:       nodeID,
		FrameID:      frameID,
	}
}

// inTx wraps fn in a fresh Persist.Transaction for use in test-helper
// reads under option C (every Store method requires an explicit tx).
func inTx(ctx context.Context, store persistence.Tables, fn func(tx persistence.Tx) error) error {
	return store.Transaction(ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	})
}
