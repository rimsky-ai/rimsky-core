// coalesce_frame.go — pins driver-symmetric semantics for
// FrameStore.EnqueueCoalesceFrame when the caller passes tx == nil.
//
// Both drivers must accept tx == nil and run the read-then-update
// atomically (postgres uses the implicit per-statement tx; SQLite opens
// an internal tx because the read+update spans two statements).
package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
)

func testCoalesceFrameNilTx(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	store := d.Store()
	if store == nil {
		t.Fatalf("testCoalesceFrameNilTx: driver.Store() returned nil")
	}

	// Insert a fresh template + instance for this test (the fixture set
	// from seedFixtureSet uses serial_queue mode; coalesce frames require
	// a coalesce-mode template hash so the producer would normally call
	// LookupFrameMode beforehand). Here we exercise the FrameStore method
	// directly so the template's frame_resolution is irrelevant.
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	nodeA := uuid.New()
	nodeB := uuid.New()

	if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: templateHash,
		Spec: nodepkg.TemplateSpec{
			Name:            "coalesce-niltx",
			Version:         "1",
			FrameResolution: nodepkg.FrameResolutionCoalesce,
			FrameTimeoutMs:  600000,
			Nodes: []nodepkg.TemplateNodeDef{
				{Type: "n", Executor: "test-executor"},
			},
		},
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}, nil); err != nil {
		t.Fatalf("template insert: %v", err)
	}
	if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           instanceID,
		TemplateHash: templateHash,
	}, nil); err != nil {
		t.Fatalf("instance create: %v", err)
	}
	for _, nid := range []uuid.UUID{nodeA, nodeB} {
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nid,
			InstanceID: instanceID,
			NodeType:   "n",
			Executor:   "test-executor",
		}, nil); err != nil {
			t.Fatalf("node create: %v", err)
		}
	}

	// First call with tx == nil inserts a queued coalesce frame.
	frameA, err := store.Frames().EnqueueCoalesceFrame(ctx, instanceID, nodeA, 600000, nil)
	if err != nil {
		t.Fatalf("EnqueueCoalesceFrame #1 (tx=nil): %v", err)
	}
	if frameA == (uuid.UUID{}) {
		t.Fatalf("EnqueueCoalesceFrame #1: returned zero frame_id")
	}

	// Second call with tx == nil and a different source node coalesces
	// onto the same frame: the returned frame_id must match.
	frameB, err := store.Frames().EnqueueCoalesceFrame(ctx, instanceID, nodeB, 600000, nil)
	if err != nil {
		t.Fatalf("EnqueueCoalesceFrame #2 (tx=nil): %v", err)
	}
	if frameB != frameA {
		t.Fatalf("EnqueueCoalesceFrame: second call returned a different frame_id (%s != %s); coalesce semantics broken under tx=nil",
			frameB, frameA)
	}

	// Third call with a tx supplied by the caller (mixing the two paths
	// must remain coherent — same coalesce row in the table for the
	// instance).
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameC, err := store.Frames().EnqueueCoalesceFrame(ctx, instanceID, nodeA, 600000, tx)
		if err != nil {
			return err
		}
		if frameC != frameA {
			t.Fatalf("EnqueueCoalesceFrame: third call (tx != nil) returned a different frame_id (%s != %s)",
				frameC, frameA)
		}
		return nil
	}); err != nil {
		t.Fatalf("EnqueueCoalesceFrame #3 (tx supplied): %v", err)
	}
}
