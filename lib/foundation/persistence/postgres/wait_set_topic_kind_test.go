// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// wait_set_topic_kind_test.go — proves the rimsky_wait_set.topic_kind
// CHECK constraint admits the full 5-value signal taxonomy
// ('state','attribute','event','transient','message','terminal') on a
// freshly-migrated Postgres. The legacy schema (001-schema.sql) only
// permits ('state','attribute','event'); a broadening migration
// (006-waitset-topic-kind-taxonomy.sql) widens it. RED until that
// migration exists — the 'transient'/'message'/'terminal' inserts are
// rejected by the legacy CHECK today.
//
// Backs S-cascade-waitset-topic-taxonomy: the wait-set ledger must record
// the actual signal class an edge gates on, not collapse three classes
// onto a lossy 'state' bucket.

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
// template → main run scope → instance, then a running frame, two nodes,
// and one in-flight node_run per node (one for receiver_run_id, one for
// sender_run_id, since the wait-set PK distinguishes them). Two distinct
// nodes are required because uq_node_runs_in_flight_per_run_scope forbids
// two in-flight runs sharing (node_id, run_scope_id). Returns (frameID,
// receiverRunID, senderRunID) — all real, FK-satisfying ids so the only
// thing under test is the topic_kind CHECK.
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
		Name:                "wait-set-topic-kind-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
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

	pgtest.ExecForTest(ctx, t, d,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		    frame_timeout_ms, started_at)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$3]::uuid[], 60000, now())`,
		frame, instanceID, receiverNodeID,
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
// row for each of the three signal classes that the legacy CHECK rejects —
// 'transient', 'message', 'terminal' — against a freshly-migrated Postgres
// and asserts every insert succeeds. RED until 006-waitset-topic-kind-taxonomy
// broadens the CHECK; today each insert is rejected by
// CHECK (topic_kind IN ('state','attribute','event')).
func TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	frameID, receiverRunID, senderRunID := seedWaitSetParentsPG(t, ctx, d)

	// One row per broadened topic_kind value. The wait-set PK is
	// (frame_id, receiver_run_id, sender_run_id, topic_kind,
	// subscription_scope); topic_kind varies, so all three coexist.
	for _, topicKind := range []string{"transient", "message", "terminal"} {
		pgtest.ExecForTest(ctx, t, d,
			`INSERT INTO rimsky_wait_set
			   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
			 VALUES ($1, $2, $3, $4, 'direct')`,
			uuid.UUID(frameID), uuid.UUID(receiverRunID), uuid.UUID(senderRunID), topicKind,
		)
	}

	// Confirm all three rows landed: the CHECK admitted the broadened
	// taxonomy rather than silently rejecting (ExecForTest fatals on a
	// CHECK violation, so reaching here already proves admission; the
	// count makes the assertion explicit).
	var count int
	pgtest.QueryRowForTest(ctx, t, d,
		`SELECT count(*) FROM rimsky_wait_set
		 WHERE frame_id = $1 AND topic_kind IN ('transient','message','terminal')`,
		[]any{uuid.UUID(frameID)}, &count,
	)
	if count != 3 {
		t.Fatalf("expected 3 wait-set rows with broadened topic_kind values, got %d; "+
			"the topic_kind CHECK must admit the full 5-value signal taxonomy "+
			"('transient','message','terminal' included)", count)
	}
}
