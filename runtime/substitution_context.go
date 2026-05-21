// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substitution-context builder under per-run attribute keying. Resolves
// {{nodes.X.attribute.Y}} directives at dispatch by querying the
// receiver's drained wait-set rows for the current frame (filtered to
// attribute-topic and settled-success senders). Senders not in the
// drained set are absent — the substitution engine returns
// ErrMissingSource for them, and the fallback operator handles the
// receiver-side default.
//
// There is NO scope-walk and NO cross-frame caching. The substitution
// context is exactly "what fired this frame for this receiver." Per
// spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
// §"Substitution context builder".
//
// @concept: attribute
// @concept: node-run
// @concept: wait-set
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// settledSuccessOutcomes is the set of last_outcome values that count
// as "settled success" for substitution-context reads. Failed senders
// (last_outcome='failed') are excluded; their attribute rows are not
// consumed by downstream substitution. Parked senders are filtered by
// the same check: parked terminals drain the wait-set (via
// drainWaitSetOnSettled in runtime/runner_terminal.go, also called
// from runtime/runner_terminal_park.go and runtime/runner_error_policy.go)
// but leave last_outcome empty per the park-has-no-outcome convention,
// so they fail this set-membership check. The filter is by outcome
// only and works uniformly for failed and parked senders.
var settledSuccessOutcomes = map[string]struct{}{
	"fresh_changed":   {},
	"fresh_unchanged": {},
	"passed":          {},
	"pure_cascade":    {},
}

// BuildAttributeDeps assembles the substitution context's Deps map for
// the receiver's dispatch. Returns a map from sender node-type to the
// sender's attribute row data (as raw JSON for lazy walking).
//
// Steps:
//  1. Query drained wait-set rows for this receiver in this frame,
//     filtered to topic_kind='attribute', ordered by (drained_at,
//     sender_run_id).
//  2. For each contributing sender_run_id, check the sender's
//     last_outcome (via RunTree().GetByID); skip non-settled-success
//     senders. Fetch the attribute row via GetByRun. Map by sender's
//     node-type.
//  3. Senders not in the drained set are absent — the substitution
//     engine returns ErrMissingSource for them.
//
// Error policy: DB errors propagate. The wait-set row references a
// sender_run_id; an inconsistency between the wait-set and run-tree
// (`senderRun == nil`) is logged and that sender is skipped — a stray
// wait-set row should not block dispatch — but propagating that as a
// real error would mask DB failures. Real DB errors from RunTree.GetByID
// or NodeAttributes.GetByRun are returned to the caller.
//
// Same-type sender ordering: when multiple sender runs share the same
// node-type (fan-out children, or different runs settling in the same
// frame), the map is keyed by node-type and the iteration order
// determined by the query ORDER BY (drained_at ASC, sender_run_id ASC)
// makes the resolution deterministic: the LAST-drained matching sender
// wins, ties broken by sender_run_id. Authors who need to address a
// specific fan-out child must use `child.partition_key`-shaped
// substitution rather than `{{nodes.X.attribute.Y}}`.
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
			// Inconsistency: wait-set references a run that doesn't
			// exist in the run-tree. Log and skip; don't block dispatch.
			if args.Logger != nil {
				args.Logger.Warn("BuildAttributeDeps: wait-set sender_run_id has no run-tree row",
					"sender_run_id", r.SenderRunID.String(),
					"receiver_run_id", receiverRunID.String(),
					"frame_id", frameID.String())
			}
			continue
		}
		if _, ok := settledSuccessOutcomes[string(senderRun.LastOutcome)]; !ok {
			continue
		}
		// "Row missing" and "row present, empty data" are two distinct
		// signals and must be distinguished. A sender that settled but
		// wrote no attributes still has a substitution-context entry
		// (empty JSON object) so per-field directives surface
		// ErrMissingSource for the specific absent field rather than
		// the whole sender being silently dropped. Receiver-side
		// required-field gates handle the "no data" case correctly via
		// the normal missing-source path.
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

// nodeTypeOf resolves a node ID to its node-type via the nodes table.
func nodeTypeOf(ctx context.Context, args RunArgs, nodeID shared.UUID, tx persistence.Tx) (string, error) {
	n, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
	if err != nil || n == nil {
		return "", err
	}
	return n.NodeType, nil
}
