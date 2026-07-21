// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint
// @concept: inertness

package runtime

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func nodeRunSnapshotForBreakpoint(acq *acquisition) map[string]any {
	if acq == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"dispatch_id":   acq.NodeRunID.String(),
		"node_id":       acq.NodeID.String(),
		"instance_id":   acq.InstanceID.String(),
		"node_type":     acq.NodeType,
		"executor":      acq.Executor,
		"graph_name":    acq.GraphName,
		"frame_id":      acq.FrameID.String(),
		"run_scope_id":  acq.RunScopeID.String(),
		"template_hash": acq.TemplateHash,
	}
	if acq.PriorNodeRunID != nil {
		out["prior_dispatch_id"] = acq.PriorNodeRunID.String()
	}
	if acq.PriorDispatchDisposition != "" {
		out["prior_dispatch_disposition"] = acq.PriorDispatchDisposition
	}
	return out
}

func heldClaimsSummaryForBreakpoint(acq *acquisition) []map[string]any {
	if acq == nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(claimproducer.ClaimSpec)
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
	heldAliases := make([]string, 0, len(acq.HeldClaims))
	for alias := range acq.HeldClaims {
		heldAliases = append(heldAliases, alias)
	}
	sort.Strings(heldAliases)
	for _, alias := range heldAliases {
		out = append(out, map[string]any{
			"alias":  alias,
			"source": "held",
		})
	}
	return out
}

func openWaitSetSummaryForBreakpoint(ctx context.Context, args RunArgs, acq *acquisition) []map[string]any {
	if acq == nil || args.Persist == nil {
		return []map[string]any{}
	}
	out := []map[string]any{}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.WaitSet().ListForReceiver(ctx, acq.FrameID, acq.NodeRunID, tx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.DrainedAt != nil {
				continue
			}
			entry := map[string]any{
				"sender_run_id": r.SenderNodeRunID.String(),
				"topic_kind":    r.TopicKind,
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
			"dispatch_id", acq.NodeRunID.String(),
			"frame_id", acq.FrameID.String(),
			"error", err.Error())
	}
	return out
}
