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

func seedWaitSetParentsPG(
	t *testing.T, ctx context.Context, d persistence.Database,
) (frameID, receiverNodeRunID, senderNodeRunID shared.UUID) {
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
		Name:    "wait-set-topic-kind-fixture",
		Version: "1",
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
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainRunScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedWaitSetParentsPG: %v", err)
	}

	messageID := uuid.New()
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ($1, $2, 'fixture/message', 'operator', 'operator')`,
		messageID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, root_run_scope_id,
		    started_at)
		 VALUES ($1, $2, $3, $4, now())`,
		frame, instanceID, messageID, mainRunScopeID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type)
		 VALUES ($1, $3, 'fixture-node-type'), ($2, $3, 'fixture-node-type')`,
		receiverNodeID, senderNodeID, instanceID,
	)
	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, state, sequence, creation_reason, enqueued_at, frame_id, run_scope_id)
		 VALUES ($1, $3, 'test-executor', 'running', 1, 'cascade', NOW(), $5, $6),
		        ($2, $4, 'test-executor', 'running', 1, 'cascade', NOW(), $5, $6)`,
		receiver, sender, receiverNodeID, senderNodeID, frame, mainRunScopeID,
	)
	return shared.UUID(frame), shared.UUID(receiver), shared.UUID(sender)
}

func TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	frameID, receiverNodeRunID, senderNodeRunID := seedWaitSetParentsPG(t, ctx, d)

	for _, topicKind := range []string{"transient", "terminal"} {
		pgtest.ExecForTest(ctx, t, d,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind)
			 VALUES ($1, $2, $3, $4)`,
			uuid.UUID(frameID), uuid.UUID(receiverNodeRunID), uuid.UUID(senderNodeRunID), topicKind,
		)
	}

	var count int
	pgtest.QueryRowForTest(ctx, t, d, `SELECT count(*) FROM rimsky_wait_set
		 WHERE frame_id = $1 AND topic_kind IN ('transient','terminal')`, []any{&count}, uuid.UUID(frameID))
	if count != 2 {
		t.Fatalf("expected 2 wait-set rows with broadened topic_kind values, got %d; "+
			"the topic_kind CHECK must admit ('transient','terminal')", count)
	}

	for _, rejected := range []string{"message", "event"} {
		if err := pgtest.TryExecForTest(ctx, t, d,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind)
			 VALUES ($1, $2, $3, $4)`,
			uuid.UUID(frameID), uuid.UUID(receiverNodeRunID), uuid.UUID(senderNodeRunID), rejected,
		); err == nil {
			t.Fatalf("insert wait_set row topic_kind=%q returned nil error; "+
				"the topic_kind CHECK must REJECT %q (retired value)", rejected, rejected)
		}
	}
}
