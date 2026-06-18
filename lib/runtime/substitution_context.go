// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: attribute
// @concept: node-run
// @concept: wait-set
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

//	@concept: signal
//	@concept: node-run
func isSettledForSubstitution(senderRun *persistence.RunTreeRow) bool {
	if senderRun == nil {
		return false
	}
	if senderRun.State != cascade.NodeStateFresh {
		return false
	}
	return senderRun.SettlingSignalType != nil
}

func BuildAttributeDeps(
	ctx context.Context,
	tx persistence.Tx,
	args RunArgs,
	receiverRunID shared.UUID,
	frameID shared.UUID,
) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)

	rows, err := args.Persist.WaitSet().ListDrainedAttributeRowsForReceiver(
		ctx, frameID, receiverRunID, tx,
	)
	if err != nil {
		return nil, fmt.Errorf("BuildAttributeDeps: list drained attribute rows: %w", err)
	}
	for _, r := range rows {
		senderRun, err := args.Persist.RunTree().GetByID(ctx, tx, r.SenderRunID)
		if err != nil {
			return nil, fmt.Errorf("BuildAttributeDeps: run-tree lookup for sender_run_id %s: %w", r.SenderRunID, err)
		}
		if senderRun == nil {
			if args.Logger != nil {
				args.Logger.Warn("BuildAttributeDeps: wait-set sender_run_id has no run-tree row",
					"sender_run_id", r.SenderRunID.String(),
					"receiver_run_id", receiverRunID.String(),
					"frame_id", frameID.String())
			}
			continue
		}
		if !isSettledForSubstitution(senderRun) {
			continue
		}
		attrRow, err := args.Persist.NodeAttributes().GetByRun(ctx, r.SenderRunID, tx)
		if err != nil {
			return nil, fmt.Errorf("BuildAttributeDeps: attribute row for sender_run_id %s: %w", r.SenderRunID, err)
		}
		nodeType, err := nodeTypeOf(ctx, args, senderRun.NodeID, tx)
		if err != nil {
			return nil, fmt.Errorf("BuildAttributeDeps: node-type for sender node_id %s: %w", senderRun.NodeID, err)
		}
		if nodeType == "" {
			if args.Logger != nil {
				args.Logger.Warn("BuildAttributeDeps: sender has empty node_type",
					"sender_run_id", r.SenderRunID.String(),
					"sender_node_id", senderRun.NodeID.String())
			}
			continue
		}
		var raw json.RawMessage
		if attrRow == nil {
			raw = json.RawMessage(`{}`)
		} else {
			marshaled, marshalErr := json.Marshal(attrRow.Data)
			if marshalErr != nil {
				return nil, fmt.Errorf("BuildAttributeDeps: marshal attribute data for sender_run_id %s: %w", r.SenderRunID, marshalErr)
			}
			raw = marshaled
		}
		out[nodeType] = raw
	}
	return out, nil
}

func nodeTypeOf(ctx context.Context, args RunArgs, nodeID shared.UUID, tx persistence.Tx) (string, error) {
	n, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
	if err != nil || n == nil {
		return "", err
	}
	return n.NodeType, nil
}
