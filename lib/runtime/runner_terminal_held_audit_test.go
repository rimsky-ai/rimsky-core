// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func countSignalAudits(t *testing.T, tables persistence.Tables, nodeID shared.UUID, kind string) int {
	t.Helper()
	return len(signalAuditRows(t, tables, nodeID, kind))
}

func signalAuditRows(t *testing.T, tables persistence.Tables, nodeID shared.UUID, kind string) []persistence.EventRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.EventRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		res, err := tables.Events().List(ctx, persistence.EventListFilter{NodeID: &nodeID, KindIn: []string{kind}},
			persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		rows = res.Events
		return nil
	}))
	return rows
}

func TestApplyTerminalComplete_HeldTransitionDoesNotDoubleAuditTerminalSuccess(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	const supID = "sup-park-spill"
	claimID := shared.UUID(uuid.New())
	producerName := "held-audit-store"
	intent := "rw"
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			NodeRunID:          &acq.NodeRunID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"scope"`),
			Address:            []byte(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: supID,
			HolderNodeID:       acq.NodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeSubgraph,
		}, tx)
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, nil,
			terminalEvent{Kind: terminalKindComplete, Changed: true}, tx)
		return err
	}))

	var runRow *persistence.NodeRunTreeRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}))
	require.NotNil(t, runRow)
	require.Equal(t, cascade.NodeStateHeld, runRow.State,
		"portfolio has a still-active claim, so the run must stay held rather than resolving; "+
			"a state other than held would invalidate this test's premise")

	require.Equal(t, 0, countSignalAudits(t, tables, acq.NodeID, "terminal/success"),
		"a held node-run's own running-to-held transition must emit no terminal audit")

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Promote(ctx, claimID, supID, spec.ClaimHandleStateCommitted, tx)
	}))
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := transitionThisHolderIfFullyResolved(ctx, args, acq, tx)
		return err
	}))

	rows := signalAuditRows(t, tables, acq.NodeID, "terminal/success")
	require.Equal(t, 1, len(rows),
		"no double-emit: the deferred settlement is the run's one and only terminal audit")
	require.Equal(t, "auto-terminal/Commit", rows[0].Payload["change_summary"],
		"a non-poisoned holder resolution must be labelled as a commit, derived from the holder's own "+
			"portfolio status rather than any caller-supplied outcome value")
}
