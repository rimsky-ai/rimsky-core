// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoint_snapshot.go — runtime-side helpers that project the
// in-memory `acquisition` struct into the JSONB-serializable shape the
// breakpoint snapshot writer (runtime/breakpoint_eval.go::buildSnapshot)
// expects. Per spec §4.6 the snapshot is *summary* data — IDs and
// short strings, not opaque scope / address / payload bytes (see
// concept:inertness for the rationale).
//
// @concept: breakpoint
// @concept: inertness

package runtime

import (
	"context"
	"encoding/json"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
)

// nodeRunSnapshotForBreakpoint projects the acquisition's node-run
// identifying fields into a JSON-serializable map. Spec §4.6 calls
// this the "projection of rimsky_node_runs row at hit time" — the
// fields the agent debugger needs to correlate the hit back to a run
// row plus its template position. The acquisition struct in
// runtime/runner_acquire.go is the only available carrier at the
// before_dispatch and after_terminal call sites; that struct's
// fields are the source of truth for this projection.
func nodeRunSnapshotForBreakpoint(acq *acquisition) map[string]any {
	if acq == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"dispatch_id":   acq.DispatchID.String(),
		"node_id":       acq.NodeID.String(),
		"instance_id":   acq.InstanceID.String(),
		"node_type":     acq.NodeType,
		"executor":      acq.Executor,
		"graph_name":    acq.GraphName,
		"frame_id":      acq.FrameID.String(),
		"run_scope_id":  acq.RunScopeID.String(),
		"template_hash": acq.TemplateHash,
	}
	if acq.PriorDispatchID != nil {
		out["prior_dispatch_id"] = acq.PriorDispatchID.String()
	}
	if acq.PriorDispatchDisposition != "" {
		out["prior_dispatch_disposition"] = acq.PriorDispatchDisposition
	}
	return out
}

// heldClaimsSummaryForBreakpoint summarizes the per-alias claims the
// run is currently holding. Per concept:inertness the summary deliberately
// omits scope / address / payload bytes — only the claim_handle_id,
// alias, and a short producer-name label are exposed. Combines locks
// acquired this dispatch (`acq.Locks`) with co-held upstream claims
// (`acq.HeldClaims`).
func heldClaimsSummaryForBreakpoint(acq *acquisition) []map[string]any {
	if acq == nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(locks.ClaimSpec)
		if !ok {
			continue
		}
		entry := map[string]any{
			"claim_handle_id": lk.ClaimHandleID.String(),
			"alias":           spec.Alias,
			"intent":          string(spec.Intent),
			"source":          "acquired",
		}
		if lk.Producer != nil {
			entry["producer"] = lk.Producer.Name()
		}
		out = append(out, entry)
	}
	for alias := range acq.HeldClaims {
		// Co-held upstream claims (`holds:` / legacy `inherits:`)
		// arrive via locks.ClaimResult — we have no
		// ClaimHandleID for them on the in-memory acquisition
		// shape, so the summary records only the alias label and
		// flags the source so the agent can distinguish co-held
		// from acquired entries.
		out = append(out, map[string]any{
			"alias":  alias,
			"source": "held",
		})
	}
	return out
}

// openWaitSetSummaryForBreakpoint queries the per-frame wait-set
// rows where this dispatch is the receiver, summarizing each as
// {sender_run_id, topic_kind, subscription_scope, topic_filter}. Per
// concept:inertness the topic_filter is surfaced because it's
// already a public diagnostic field (mirrors control/controlapi/
// admin_waitset.go's wire shape) and helps the agent debugger
// correlate hits with the subscription edges that gated them.
//
// Opens its own short tx via args.Persist.Transaction — the
// EvaluateBreakpoints caller does NOT run inside an outer tx, so
// this helper's tx is independent.
func openWaitSetSummaryForBreakpoint(ctx context.Context, args RunArgs, acq *acquisition) []map[string]any {
	if acq == nil || args.Persist == nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.WaitSet().ListForReceiver(ctx, acq.FrameID, acq.DispatchID, tx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.DrainedAt != nil {
				continue
			}
			entry := map[string]any{
				"sender_run_id":      r.SenderRunID.String(),
				"topic_kind":         r.TopicKind,
				"subscription_scope": r.SubscriptionScope,
			}
			if len(r.TopicFilter) > 0 {
				var f any
				if jerr := json.Unmarshal(r.TopicFilter, &f); jerr == nil {
					entry["topic_filter"] = f
				}
			}
			out = append(out, entry)
		}
		return nil
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("openWaitSetSummaryForBreakpoint: wait-set list failed; snapshot will omit",
			"dispatch_id", acq.DispatchID.String(),
			"frame_id", acq.FrameID.String(),
			"error", err.Error())
	}
	return out
}
