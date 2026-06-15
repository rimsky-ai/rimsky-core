// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// synthetic_envelope.go — single chokepoint for runtime-synthetic
// envelopes (`node/invalidate`, `node/reset`, `asset/materialize`,
// `instance/root`).
//
// Runtime-synthetic envelopes bypass the receipt-time message-schema
// registry gate by calling `EnqueueMessage` in-process, and they wake
// receivers by enumerating node-UUIDs in `payload.wake_node_ids` (read at
// frame promotion in `graph/frame/engine.go::advanceOneFrame`) rather
// than through the subscriber-side cascade. The wake-mechanism divide is
// load-bearing — see the structural-divide invariant on `advanceOneFrame`.
//
// The previous shape was four hand-rolled call sites repeating
// near-identical scaffolding (mint a UUID, marshal a wake-bearing
// payload, build the EnqueueMessageRequest, enqueue the frame in the
// same tx) with subtle differences in `sender_kind` and `sender`
// strings. Drift between any two of those sites silently broke the
// invariant for one path. This helper collapses them onto one
// implementation so the invariant lives in one place.
//
// @concept: message
// @concept: frame
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
)

// EnqueueSyntheticWakeFrame builds a runtime-synthetic wake envelope
// (`type` is a runtime-only type-path NOT declared in any template's
// `messages:` registry) and the queued frame that delivers it, inside
// the caller's outer tx.
//
// The envelope is tagged `sender_kind: "instance"` — the same shape
// `runner_emit_message.go`'s cascade-emit uses for messages the
// runtime synthesizes. Operator- and publisher-initiated actions that
// flow through this helper (asset materialize, node reset,
// operator-fired node invalidate) are still operator-INITIATED but the
// envelope itself is runtime-synthesized; `sender_kind: "instance"` is
// the correct discriminator for that distinction. A genuine
// operator-posted envelope (one whose body the operator authored) goes
// through `POST /instances/{id}/messages` and keeps
// `sender_kind: "operator"`.
//
// `sender` defaults to `"instance:<instanceID>"` (matching the
// `runner_emit_message.go::emitCascadeMessageInTx` convention) when
// the caller passes the empty string. Callers that want a per-origin
// discriminator (cron, parked-wake, cascade, ad-hoc admin) pass a
// non-empty string; the dedup-tuple isolation still holds because the
// instance ID is part of the row key elsewhere.
//
// The payload merges `extraPayload` (caller-supplied; may carry
// `reason`, `target_node`, etc.) with a `wake_node_ids` entry built
// from the supplied node UUIDs. The caller may pass nil/empty
// `extraPayload`; the resulting body is `{"wake_node_ids": [...]}`.
//
// Returns the new (messageID, frameID) on success.
func EnqueueSyntheticWakeFrame(
	ctx context.Context,
	tx persistence.Tx,
	persist persistence.Tables,
	instanceID shared.UUID,
	syntheticType string,
	sender string,
	wakeNodeIDs []shared.UUID,
	extraPayload map[string]any,
) (shared.UUID, shared.UUID, error) {
	if instanceID == (shared.UUID{}) {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: instance_id required")
	}
	if syntheticType == "" {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: synthetic type required")
	}
	if len(wakeNodeIDs) == 0 {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: wake_node_ids must be non-empty (the frame would promote and stale-mark nothing — a silent no-op)")
	}
	if sender == "" {
		sender = "instance:" + instanceID.String()
	}
	// @deliberate: expand wake_node_ids to include each input wake-node's
	// force_upstream_refresh upstreams resolved against the instance's
	// template. The receiver's substitution context reads upstream
	// attributes through drained wait-set rows; for the upstream's
	// freshest value to be observable at the receiver's dispatch the
	// upstream must run AND drain a wait-set row keyed (receiver_run,
	// upstream_run) before the receiver dispatches. Including the
	// upstream in wake_node_ids gets it stale-marked alongside the
	// receiver in advanceOneFrame's promotion tx; the companion
	// `wait_set_pairs` field tells advanceOneFrame which (receiver,
	// upstream) pairs to pre-install wait-set rows for so the
	// supervisor's eligibility predicate gates the receiver until the
	// upstream settles. Both wake-list expansion and pair-list build
	// happen here, in the synthetic envelope chokepoint, so every
	// synthetic-envelope call site gets the structural pre-staling for
	// free without per-site code.
	//
	// @story: upstream-pull-on-invalidate
	expanded, pairs, err := expandWakeWithUpstreamRefresh(ctx, tx, persist, instanceID, wakeNodeIDs)
	if err != nil {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: expand upstream-refresh: %w", err)
	}
	wakeStrs := make([]string, 0, len(expanded))
	for _, id := range expanded {
		wakeStrs = append(wakeStrs, id.String())
	}
	body := map[string]any{}
	for k, v := range extraPayload {
		body[k] = v
	}
	// `wake_node_ids` is set after the merge so a caller cannot smuggle
	// in a contradictory list via extraPayload.
	body["wake_node_ids"] = wakeStrs
	if len(pairs) > 0 {
		pairStrs := make([]map[string]string, 0, len(pairs))
		for _, p := range pairs {
			pairStrs = append(pairStrs, map[string]string{
				"receiver": p.receiver.String(),
				"upstream": p.upstream.String(),
			})
		}
		body["wait_set_pairs"] = pairStrs
	}
	payloadBytes, err := json.Marshal(body)
	if err != nil {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: marshal payload: %w", err)
	}
	msgID := shared.UUID(uuid.New())
	if err := EnqueueMessage(ctx, tx, persist.Messages(), persistence.EnqueueMessageRequest{
		ID:         msgID,
		InstanceID: instanceID,
		Type:       syntheticType,
		Sender:     sender,
		SenderKind: "instance",
		Payload:    payloadBytes,
	}); err != nil {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: enqueue envelope: %w", err)
	}
	frameID, err := frame.EnqueueFrame(ctx, persist, tx, instanceID, msgID)
	if err != nil {
		return shared.UUID{}, shared.UUID{}, fmt.Errorf("EnqueueSyntheticWakeFrame: enqueue frame: %w", err)
	}
	return msgID, frameID, nil
}

// upstreamRefreshPair is one (receiver, upstream) pair that
// `EnqueueSyntheticWakeFrame` embeds in the synthetic envelope's
// `wait_set_pairs` payload field so `advanceOneFrame` can install the
// pre-emptive wait-set row that gates the receiver's dispatch until the
// upstream settles.
type upstreamRefreshPair struct {
	receiver shared.UUID
	upstream shared.UUID
}

// expandWakeWithUpstreamRefresh resolves each input wake-node's
// force_upstream_refresh upstreams against the instance's template and
// returns the expanded wake list (originals + upstreams, deduped) plus
// the receiver→upstream pair list. Skips quietly when the instance,
// template, or edge map cannot be resolved — the caller's wake list is
// returned unchanged in that case.
//
// @story: upstream-pull-on-invalidate
// @concept: cascade
func expandWakeWithUpstreamRefresh(
	ctx context.Context, tx persistence.Tx, persist persistence.Tables,
	instanceID shared.UUID, wakeNodeIDs []shared.UUID,
) ([]shared.UUID, []upstreamRefreshPair, error) {
	inst, err := persist.Instances().Get(ctx, instanceID, tx)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil {
		return wakeNodeIDs, nil, nil
	}
	runArgs := RunArgs{Persist: persist}
	refreshEdges, err := hardDepEdgesForTemplate(ctx, runArgs, inst.TemplateHash, tx)
	if err != nil {
		return nil, nil, err
	}
	if len(refreshEdges) == 0 {
		return wakeNodeIDs, nil, nil
	}
	// @constraint: index instance nodes by node-type so upstream-type
	// lookups resolve to per-instance node IDs without an N×M scan.
	instNodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil, nil, err
	}
	idByType := make(map[string]shared.UUID, len(instNodes))
	for _, n := range instNodes {
		// @constraint: one node per type per instance is a template
		// invariant; the last write wins is structurally equivalent to
		// the first since they cannot differ.
		idByType[n.NodeType] = n.ID
	}
	seen := make(map[shared.UUID]struct{}, len(wakeNodeIDs))
	for _, id := range wakeNodeIDs {
		seen[id] = struct{}{}
	}
	expanded := make([]shared.UUID, 0, len(wakeNodeIDs))
	expanded = append(expanded, wakeNodeIDs...)
	var pairs []upstreamRefreshPair
	for _, receiverID := range wakeNodeIDs {
		var receiverType string
		for _, n := range instNodes {
			if n.ID == receiverID {
				receiverType = n.NodeType
				break
			}
		}
		if receiverType == "" {
			continue
		}
		upstreamTypes := refreshEdges[receiverType]
		for _, upstreamType := range upstreamTypes {
			upstreamID, ok := idByType[upstreamType]
			if !ok {
				continue
			}
			pairs = append(pairs, upstreamRefreshPair{receiver: receiverID, upstream: upstreamID})
			if _, dup := seen[upstreamID]; !dup {
				expanded = append(expanded, upstreamID)
				seen[upstreamID] = struct{}{}
			}
		}
	}
	return expanded, pairs, nil
}
