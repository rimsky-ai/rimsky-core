// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestRetryDoesNotPrematurelyEndFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-frame-end-predicate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-retry-pred", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	freshScopeID := createFreshRunScope(t, h, iid)
	_, err := h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_node_runs WHERE node_id = $1`, uuid.UUID(worker.ID))
	require.NoError(t, err)
	_, err = h.Pool.Exec(h.Ctx,
		`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))
	require.NoError(t, err)

	messageID := uuid.New()
	_, err = h.Pool.Exec(h.Ctx, `
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		VALUES ($1, $2, 'fixture/retry-preserves-frame-id', 'operator', 'operator')
	`, messageID, uuid.UUID(iid))
	require.NoError(t, err)
	var frameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_frames(instance_id, triggering_message_id, started_at, root_run_scope_id)
		VALUES ($1, $2, now(), $3)
		RETURNING frame_id
	`, uuid.UUID(iid), messageID, uuid.UUID(freshScopeID)).Scan(&frameID))
	var seededRunID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		INSERT INTO rimsky_node_runs
		    (id, node_id, executor_name, required_claim_producers, enqueued_at, state, sequence, frame_id, run_scope_id)
		VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'running', 1, $2, $3)
		RETURNING id
	`, uuid.UUID(worker.ID), frameID, uuid.UUID(freshScopeID)).Scan(&seededRunID))

	// @decision: non-cascade-direct-to-stale
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		if err := h.Persist.Nodes().UpdateState(h.Ctx,
			shared.UUID(seededRunID), cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx); err != nil {
			return err
		}
		_, err := h.Persist.Nodes().CreateNonCascadeStale(h.Ctx, persistence.NonCascadeStaleInput{
			NodeID:                 worker.ID,
			RunScopeID:             freshScopeID,
			FrameID:                shared.UUID(frameID),
			ExecutorName:           "stub",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now(),
			CreationReason:         cascade.CreationReasonOperatorInvalidate,
		}, tx)
		return err
	}))

	var preservedFrameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND state IN ('pending','stale','running','held','parked')
		  ORDER BY sequence DESC LIMIT 1`,
		uuid.UUID(worker.ID)).Scan(&preservedFrameID))
	require.Equal(t, frameID, preservedFrameID, "retry must preserve the frame_id on the in-flight run")

	var inflight int
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `
		SELECT count(*) FROM rimsky_node_runs r
		JOIN rimsky_frames f ON f.frame_id = r.frame_id
		WHERE f.ended_at IS NULL
		  AND r.state IN ('stale','running')
	`).Scan(&inflight))
	require.Equal(t, 1, inflight,
		"retried-stale run row must still register as in-flight under the running frame")
}
