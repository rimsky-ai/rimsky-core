// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: cascade
// @decision: walker-rule-per-sender-node
func ensureCascadePending(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiverNodeID, runScopeID, frameID, senderNodeID, senderRunID foundationshared.UUID,
) (foundationshared.UUID, error) {
	if err := args.Persist.Nodes().LockReceiverCascade(ctx, tx, receiverNodeID, runScopeID, frameID); err != nil {
		return foundationshared.UUID{}, err
	}
	latest, err := args.Persist.Nodes().FindLatestCascadePending(ctx, tx, receiverNodeID, runScopeID, frameID)
	if err != nil {
		return foundationshared.UUID{}, err
	}
	if latest != nil {
		if senderRunID != (foundationshared.UUID{}) {
			covered, err := args.Persist.WaitSet().HasRowForSenderRun(ctx, frameID, latest.RunID, senderRunID, tx)
			if err != nil {
				return foundationshared.UUID{}, err
			}
			if covered {
				return latest.RunID, nil
			}
		}
		senderNodes, err := args.Persist.WaitSet().ListSenderNodesForReceiver(ctx, frameID, latest.RunID, tx)
		if err != nil {
			return foundationshared.UUID{}, err
		}
		coversSender := false
		for _, sn := range senderNodes {
			if sn == senderNodeID {
				coversSender = true
				break
			}
		}
		if !coversSender {
			return latest.RunID, nil
		}
	}
	return args.Persist.Nodes().CreateCascadePending(ctx, tx, receiverNodeID, runScopeID, frameID)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func resolveReceiverRunForCascade(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	receiverNodeID, runScopeID, frameID, senderNodeID, senderRunID foundationshared.UUID,
	visitedThisTurn map[foundationshared.UUID]struct{},
) (foundationshared.UUID, bool, error) {
	if _, seen := visitedThisTurn[receiverNodeID]; seen {
		if err := args.Persist.Nodes().LockReceiverCascade(ctx, tx, receiverNodeID, runScopeID, frameID); err != nil {
			return foundationshared.UUID{}, false, err
		}
		latest, err := args.Persist.Nodes().FindLatestCascadePending(ctx, tx, receiverNodeID, runScopeID, frameID)
		if err != nil {
			return foundationshared.UUID{}, false, err
		}
		if latest != nil {
			return latest.RunID, true, nil
		}
		return foundationshared.UUID{}, false, nil
	}
	id, err := ensureCascadePending(ctx, args, tx, receiverNodeID, runScopeID, frameID, senderNodeID, senderRunID)
	if err != nil {
		if errors.Is(err, persistence.ErrRunScopeClosed) {
			return foundationshared.UUID{}, false, nil
		}
		return foundationshared.UUID{}, false, err
	}
	visitedThisTurn[receiverNodeID] = struct{}{}
	return id, true, nil
}
