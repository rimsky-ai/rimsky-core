// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_acquire_scope.go — RunScope-derived projections for an
// acquisition. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// the (parent_run_id, partition_key) tuple that used to ride on the
// rimsky_node_runs row moved to rimsky_run_scopes; callers that need
// either value resolve it from the acquisition's RunScopeID via the
// helpers below.
//
// @concept: run-scope

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// acqScopeTuple bundles the two RunScope-derived projections downstream
// emitters (lineage rows, attribute-overrides matcher overlay) consume.
// Both fields are zero for runs in the main RunScope (top-level
// outer-graph dispatches).
type acqScopeTuple struct {
	ParentRunID  *shared.UUID
	PartitionKey string
}

// resolveAcqScope returns the RunScope-derived (parent_run_id,
// partition_key) tuple for an acquisition. Opens its own short tx;
// callers running inside an open tx should use
// resolveAcqScopeInTx instead.
//
// Best-effort: on lookup failure the zero tuple is returned and a WARN
// is logged. Callers may treat the zero tuple as "main RunScope" /
// "lineage row will omit parent_run_id".
func resolveAcqScope(ctx context.Context, args RunArgs, acq *acquisition) acqScopeTuple {
	if acq == nil || acq.RunScopeID == (shared.UUID{}) || args.Persist == nil {
		return acqScopeTuple{}
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return acqScopeTuple{}
	}
	var out acqScopeTuple
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := resolveAcqScopeInTx(ctx, scopes, tx, acq)
		out = t
		return err
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("resolveAcqScope: run-scope GetByID failed; downstream will omit parent_run_id/partition_key",
				"run_scope_id", acq.RunScopeID.String(),
				"error", err.Error())
		}
		return acqScopeTuple{}
	}
	return out
}

// resolveAcqScopeInTx is the tx-taking variant of resolveAcqScope. Used
// inside an already-open tx (e.g. acquisition / terminal sequencing)
// where opening a nested tx would either self-deadlock under SQLite or
// duplicate work under Postgres.
func resolveAcqScopeInTx(
	ctx context.Context, scopes persistence.RunScopeTable, tx persistence.Tx,
	acq *acquisition,
) (acqScopeTuple, error) {
	if acq == nil || acq.RunScopeID == (shared.UUID{}) || scopes == nil {
		return acqScopeTuple{}, nil
	}
	rs, err := scopes.GetByID(ctx, tx, acq.RunScopeID)
	if err != nil {
		return acqScopeTuple{}, fmt.Errorf("resolveAcqScopeInTx: load run scope %s: %w", acq.RunScopeID, err)
	}
	if rs == nil {
		return acqScopeTuple{}, nil
	}
	out := acqScopeTuple{PartitionKey: rs.PartitionKey}
	if rs.ParentRunID != nil {
		pid := *rs.ParentRunID
		out.ParentRunID = &pid
	}
	return out, nil
}
