// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @decision: intx-suffix-convention
func resolveAcqScope(ctx context.Context, args RunArgs, acq *acquisition, tx persistence.Tx) acqScopeTuple {
	if acq == nil || acq.RunScopeID == (shared.UUID{}) || args.Persist == nil {
		return acqScopeTuple{}
	}
	scopes := args.Persist.RunScopes()
	if scopes == nil {
		return acqScopeTuple{}
	}
	var out acqScopeTuple
	var err error
	if tx != nil {
		out, err = resolveAcqScopeRow(ctx, scopes, acq, tx)
	} else {
		err = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var ierr error
			out, ierr = resolveAcqScopeRow(ctx, scopes, acq, tx)
			return ierr
		})
	}
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("RUNNER.RUNSCOPE.LOOKUPFAILED", "site", "resolveAcqScope", "detail", "downstream omits parent_run_id and partition_key",
				"run_scope_id", acq.RunScopeID.String(),
				"error", err.Error())
		}
		return acqScopeTuple{}
	}
	return out
}

func resolveAcqScopeRow(
	ctx context.Context, scopes persistence.RunScopeTable, acq *acquisition, tx persistence.Tx,
) (acqScopeTuple, error) {
	if acq == nil || acq.RunScopeID == (shared.UUID{}) || scopes == nil {
		return acqScopeTuple{}, nil
	}
	rs, err := scopes.GetByID(ctx, acq.RunScopeID, tx)
	if err != nil {
		return acqScopeTuple{}, fmt.Errorf("resolveAcqScope: load run scope %s: %w", acq.RunScopeID, err)
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
