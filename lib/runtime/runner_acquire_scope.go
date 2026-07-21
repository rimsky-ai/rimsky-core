// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type acqScopeTuple struct {
	ParentNodeRunID *shared.UUID
	PartitionKey    string
}

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
		t, err := resolveAcqScopeInTx(ctx, scopes, acq, tx)
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

func resolveAcqScopeInTx(
	ctx context.Context, scopes persistence.RunScopeTable, acq *acquisition, tx persistence.Tx,
) (acqScopeTuple, error) {
	if acq == nil || acq.RunScopeID == (shared.UUID{}) || scopes == nil {
		return acqScopeTuple{}, nil
	}
	rs, err := scopes.GetByID(ctx, acq.RunScopeID, tx)
	if err != nil {
		return acqScopeTuple{}, fmt.Errorf("resolveAcqScopeInTx: load run scope %s: %w", acq.RunScopeID, err)
	}
	if rs == nil {
		return acqScopeTuple{}, nil
	}
	out := acqScopeTuple{PartitionKey: rs.PartitionKey}
	if rs.ParentNodeRunID != nil {
		pid := *rs.ParentNodeRunID
		out.ParentNodeRunID = &pid
	}
	return out, nil
}
