// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestOrphanDispatchReaper_ReleasesTerminalFrameClaim(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	nodeRunID := seedTerminalFrameAndDispatch(t, h, "stale-sup")

	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(),
		slog.New(slog.NewTextHandler(io.Discard, nil))))

	var claimedBy *string
	h.QueryRowSQL(
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = $1`,
		[]any{nodeRunID}, &claimedBy)
	require.Nil(t, claimedBy,
		"orphan reaper should release dispatch claim when joined frame is terminal")
}

func TestOrphanDispatchReaper_ClaimantGuardedRelease(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	nodeRunID := seedTerminalFrameAndDispatch(t, h, "fresh-sup")

	priorOwner, err := h.Driver.Queue().GetClaimedBy(h.Ctx, nodeRunID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", priorOwner.Kind)
	require.Equal(t, "fresh-sup", priorOwner.SupervisorID)

	require.NoError(t, h.Driver.Queue().ReleaseClaim(h.Ctx, nodeRunID, "stale-sup"))

	owner, err := h.Driver.Queue().GetClaimedBy(h.Ctx, nodeRunID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", owner.Kind,
		"live supervisor's claim must remain after stale-claimant release")
	require.Equal(t, "fresh-sup", owner.SupervisorID,
		"live supervisor's claim must not be released by stale-claimant reap")
}

func seedTerminalFrameAndDispatch(t *testing.T, h *scenario.Harness, claimedBy string) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash := "sha256-" + suffix
	h.ExecSQL(`
		INSERT INTO rimsky_templates (id, spec, state)
		VALUES ($1, '{}'::jsonb, 'deployed')
	`, templateHash)
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		ck := "ck-orphan-" + instanceID.String()[:8]
		_, err := h.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: templateHash,
			InstanceKey:  &ck,
		}, tx)
		return err
	}))
	nodeID := uuid.New()
	h.ExecSQL(`
		INSERT INTO rimsky_nodes (id, instance_id, node_type)
		VALUES ($1, $2, 'n')
	`, nodeID, instanceID)
	frameID := uuid.New()
	now := time.Now()
	messageID := uuid.New()
	h.ExecSQL(`
		INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		VALUES ($1, $2, 'fixture/orphan-reaper', 'operator', 'operator')
	`, messageID, instanceID)
	h.ExecSQL(`
		INSERT INTO rimsky_frames (frame_id, instance_id, triggering_message_id, state, started_at, ended_at, frame_timeout_ms, root_run_scope_id)
		VALUES ($1, $2, $3, 'completed', $4, $4, 600000, $5)
	`, frameID, instanceID, messageID, now, mainScopeID)
	nodeRunID := uuid.New()
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, claimed_by, frame_id, run_scope_id, sequence, state)
		VALUES ($1, $2, NULL, '{}', $3, $4, $5, 1, 'running')
	`, nodeRunID, nodeID, claimedBy, frameID, mainScopeID)
	return nodeRunID
}
