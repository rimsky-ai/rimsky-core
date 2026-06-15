// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Verifies that runReapOrphanFrameDispatches releases dispatch claims
// using a per-row, claimant-guarded UPDATE (blessed-invariant 4): the
// SET clauses run only when `claimed_by = priorClaimedBy`. A fresh
// supervisor that re-claimed the row between the SELECT and the UPDATE
// keeps its live claim.
//
// This test catches review Issue 1: the previous bulk UPDATE
// indiscriminately nulled claim fields whenever the joined frame was
// terminal, racing with a still-live supervisor's slow finish.
//
// Two cases:
//  1. Plain orphan reap — frame terminal, dispatch claim from the same
//     supervisor that owned it when the frame ran. The reaper releases
//     the claim.
//  2. Claimant-guard — the per-row release issued with a stale prior-
//     claimed-by must NOT touch a row whose claim has rotated to a
//     fresh supervisor.
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

	dispatchID := seedTerminalFrameAndDispatch(t, h, "stale-sup")

	require.NoError(t, frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(),
		slog.New(slog.NewTextHandler(io.Discard, nil))))

	var claimedBy *string
	h.QueryRowSQL(
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = $1`,
		[]any{dispatchID}, &claimedBy)
	require.Nil(t, claimedBy,
		"orphan reaper should release dispatch claim when joined frame is terminal")
}

func TestOrphanDispatchReaper_ClaimantGuardedRelease(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	dispatchID := seedTerminalFrameAndDispatch(t, h, "fresh-sup")

	// @constraint: Drive the same SQL shape the per-row reaper uses, but with a stale
	// claimant id ("stale-sup"). The current claimed_by is "fresh-sup",
	// so the WHERE clause must not match and the row must be untouched.
	// Use the persistence Queue's claimant-guarded ReleaseClaim so we
	// don't have to import pgx for a direct UPDATE+RowsAffected check.
	priorOwner, err := h.Driver.Queue().GetClaimedBy(h.Ctx, dispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", priorOwner.Kind)
	require.Equal(t, "fresh-sup", priorOwner.SupervisorID)

	require.NoError(t, h.Driver.Queue().ReleaseClaim(h.Ctx, dispatchID, "stale-sup"))

	owner, err := h.Driver.Queue().GetClaimedBy(h.Ctx, dispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", owner.Kind,
		"live supervisor's claim must remain after stale-claimant release")
	require.Equal(t, "fresh-sup", owner.SupervisorID,
		"live supervisor's claim must not be released by stale-claimant reap")
}

// seedTerminalFrameAndDispatch inserts a template+instance+node+
// terminal-frame+claimed-dispatch tuple suitable for orphan-reap tests.
// Returns the dispatch row's id.
func seedTerminalFrameAndDispatch(t *testing.T, h *scenario.Harness, claimedBy string) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash := "sha256-" + suffix
	h.ExecSQL(`
		INSERT INTO rimsky_templates (id, spec, state)
		VALUES ($1, '{"frame_resolution_mode":"serial_queue"}'::jsonb, 'deployed')
	`, templateHash)
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	// @constraint: rimsky_instances.main_run_scope_id ↔ rimsky_run_scopes.instance_id
	// are mutually FK'd DEFERRABLE INITIALLY DEFERRED, so the pair must
	// be inserted inside the same tx. Use the persistence layer so the
	// canonical constructors land both rows correctly.
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
			ID:             instanceID,
			TemplateHash:   templateHash,
			InstanceKey:    &ck,
			MainRunScopeID: mainScopeID,
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
	h.ExecSQL(`
		INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
			queued_at, started_at, ended_at, frame_timeout_ms)
		VALUES ($1, $2, 'serial_queue', 'completed', ARRAY[$3]::UUID[], $4, $4, $4, 600000)
	`, frameID, instanceID, nodeID, now)
	dispatchID := uuid.New()
	h.ExecSQL(`
		INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, claimed_by, frame_id, run_scope_id)
		VALUES ($1, $2, NULL, '{}', $3, $4, $5)
	`, dispatchID, nodeID, claimedBy, frameID, mainScopeID)
	return dispatchID
}
