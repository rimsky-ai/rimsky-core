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
	ctx context.Context, args RunArgs, receiverNodeID, runScopeID, frameID, senderNodeID, senderNodeRunID foundationshared.UUID, tx persistence.Tx,
) (foundationshared.UUID, error) {
	if err := args.Persist.Nodes().LockReceiverCascade(ctx, receiverNodeID, runScopeID, frameID, tx); err != nil {
		return foundationshared.UUID{}, err
	}
	latest, err := args.Persist.Nodes().FindLatestCascadePending(ctx, receiverNodeID, runScopeID, frameID, tx)
	if err != nil {
		return foundationshared.UUID{}, err
	}
	if latest != nil {
		if senderNodeRunID != (foundationshared.UUID{}) {
			covered, err := args.Persist.WaitSet().HasRowForSenderRun(ctx, frameID, latest.NodeRunID, senderNodeRunID, tx)
			if err != nil {
				return foundationshared.UUID{}, err
			}
			if covered {
				return latest.NodeRunID, nil
			}
		}
		senderNodes, err := args.Persist.WaitSet().ListSenderNodesForReceiver(ctx, frameID, latest.NodeRunID, tx)
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
			return latest.NodeRunID, nil
		}
	}
	return args.Persist.Nodes().CreateCascadePending(ctx, receiverNodeID, runScopeID, frameID, tx)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func resolveReceiverRunForCascade(
	ctx context.Context, args RunArgs, receiverNodeID, runScopeID, frameID, senderNodeID, senderNodeRunID foundationshared.UUID, visitedThisTurn map[foundationshared.UUID]struct{}, tx persistence.Tx,
) (foundationshared.UUID, bool, error) {
	if _, seen := visitedThisTurn[receiverNodeID]; seen {
		if err := args.Persist.Nodes().LockReceiverCascade(ctx, receiverNodeID, runScopeID, frameID, tx); err != nil {
			return foundationshared.UUID{}, false, err
		}
		latest, err := args.Persist.Nodes().FindLatestCascadePending(ctx, receiverNodeID, runScopeID, frameID, tx)
		if err != nil {
			return foundationshared.UUID{}, false, err
		}
		if latest != nil {
			return latest.NodeRunID, true, nil
		}
		return foundationshared.UUID{}, false, nil
	}
	id, err := ensureCascadePending(ctx, args, receiverNodeID, runScopeID, frameID, senderNodeID, senderNodeRunID, tx)
	if err != nil {
		if errors.Is(err, persistence.ErrRunScopeClosed) {
			return foundationshared.UUID{}, false, nil
		}
		return foundationshared.UUID{}, false, err
	}
	visitedThisTurn[receiverNodeID] = struct{}{}
	return id, true, nil
}
