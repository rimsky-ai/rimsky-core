// fixtures.go — shared seed helpers for the cross-driver conformance
// suite. Each helper takes a persistence.Driver so it works against both
// Postgres and SQLite without driver-specific cruft.
package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	nodepkg "github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// seedFixtureSet creates the minimum chain of rows needed to satisfy
// the FK chain rimsky_dispatch -> rimsky_nodes -> rimsky_instances ->
// rimsky_templates AND rimsky_dispatch -> rimsky_frames. Returns the
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

func seedFixtureSet(ctx context.Context, t *testing.T, d persistence.Driver) fixtureSet {
	t.Helper()
	store := d.Store()
	if store == nil {
		t.Fatalf("seedFixtureSet: driver.Store() returned nil")
	}

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	nodeID := uuid.New()
	frameID := uuid.New()

	spec := nodepkg.TemplateSpec{
		Name:            "conformance-fixture",
		Version:         "1",
		FrameResolution: nodepkg.FrameResolutionSerialQueue,
		FrameTimeoutMs:  600000,
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

	if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID:     templateHash,
		Spec:   spec,
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}, nil); err != nil {
		t.Fatalf("seedFixtureSet: template insert: %v", err)
	}
	if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           instanceID,
		TemplateHash: templateHash,
	}, nil); err != nil {
		t.Fatalf("seedFixtureSet: instance create: %v", err)
	}
	if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID:           nodeID,
		InstanceID:   instanceID,
		NodeType:     "fixture-node-type",
		Executor:     "test-executor",
		Dependencies: []shared.UUID{},
	}, nil); err != nil {
		t.Fatalf("seedFixtureSet: node create: %v", err)
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
