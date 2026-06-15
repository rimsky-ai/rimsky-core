// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// seedWaitSetParentsPG creates the FK parents a rimsky_wait_set row needs:
// template → main run scope → instance, then a typed triggering message,
// a running frame, two nodes, and one in-flight node_run per node (one for
// receiver_run_id, one for sender_run_id, since the wait-set PK distinguishes
// them). Two distinct nodes are required because
// uq_node_runs_in_flight_per_run_scope forbids two in-flight runs sharing
// (node_id, run_scope_id). Returns (frameID, receiverRunID, senderRunID) —
// all real, FK-satisfying ids so the only thing under test is the
// topic_kind CHECK.
func seedWaitSetParentsPG(
	t *testing.T, ctx context.Context, d persistence.Database,
) (frameID, receiverRunID, senderRunID shared.UUID) {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()
	frame := uuid.New()
	receiverNodeID := uuid.New()
	senderNodeID := uuid.New()
	receiver := uuid.New()
	sender := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:           "wait-set-topic-kind-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainRunScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedWaitSetParentsPG: %v", err)
	}

	// @constraint: rimsky_frames.triggering_message_id is NOT NULL FK
	// (migration 010/011 message-schema-layer reshape) — every frame insert
	// must reference a pre-existing rimsky_messages row.
	messageID := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ($1, $2, 'fixture/message', 'operator', 'operator')`,
		messageID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state,
		    frame_timeout_ms, started_at)
		 VALUES ($1, $2, $3, 'running', 60000, now())`,
		frame, instanceID, messageID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, frame_id)
		 VALUES ($1, $3, 'fixture-node-type', $4), ($2, $3, 'fixture-node-type', $4)`,
		receiverNodeID, senderNodeID, instanceID, frame,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, phase, state, frame_id, run_scope_id)
		 VALUES ($1, $3, 'test-executor', 'active', 'running', $5, $6),
		        ($2, $4, 'test-executor', 'active', 'running', $5, $6)`,
		receiver, sender, receiverNodeID, senderNodeID, frame, mainRunScopeID,
	)
	return shared.UUID(frame), shared.UUID(receiver), shared.UUID(sender)
}

// TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy inserts a rimsky_wait_set
// row for each of the two signal classes that the legacy CHECK rejects but
// the post-006 CHECK admits — 'transient' and 'terminal' — against a
// freshly-migrated Postgres and asserts every insert succeeds. Also asserts
// that 'message' is rejected by the post-011 CHECK: the virtual-node-settle
// model has no wait-set rows under that bucket.
func TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	frameID, receiverRunID, senderRunID := seedWaitSetParentsPG(t, ctx, d)

	// @constraint: wait-set PK is (frame_id, receiver_run_id, sender_run_id,
	// topic_kind, subscription_scope) — topic_kind varies across the loop, so
	// both rows coexist under one (frame, receiver, sender) triple.
	for _, topicKind := range []string{"transient", "terminal"} {
		pgtest.ExecForTest(ctx, t, d,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
			 VALUES ($1, $2, $3, $4, 'direct')`,
			uuid.UUID(frameID), uuid.UUID(receiverRunID), uuid.UUID(senderRunID), topicKind,
		)
	}

	// @deliberate: explicit count assertion is redundant with ExecForTest
	// fataling on CHECK violation (reaching here already proves admission),
	// but the count makes the success criterion visible at the test site.
	var count int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT count(*) FROM rimsky_wait_set
		 WHERE frame_id = $1 AND topic_kind IN ('transient','terminal')`,
		[]any{uuid.UUID(frameID)}, &count,
	)
	if count != 2 {
		t.Fatalf("expected 2 wait-set rows with broadened topic_kind values, got %d; "+
			"the topic_kind CHECK must admit ('transient','terminal')", count)
	}

	// @constraint: 'message' must NOT be admitted by the post-011 CHECK.
	// The virtual-node-settle model has no wait-set rows under that bucket;
	// an INSERT must fail through the CHECK rejection path.
	if err := pgtest.TryExecForTest(ctx, t, d,
		`INSERT INTO rimsky_wait_set
		   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
		 VALUES ($1, $2, $3, 'message', 'direct')`,
		uuid.UUID(frameID), uuid.UUID(receiverRunID), uuid.UUID(senderRunID),
	); err == nil {
		t.Fatalf("insert wait_set row topic_kind='message' returned nil error; " +
			"the topic_kind CHECK must REJECT 'message' (post-011 retirement)")
	}
}
