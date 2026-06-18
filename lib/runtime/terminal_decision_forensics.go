// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func outcomeVerbName(o AggregateOutcome) string {
	switch o {
	case AggregateCommit:
		return "Commit"
	case AggregateAbandon:
		return "Abandon"
	}
	return "Unknown"
}

func emitTerminalForensics(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	td TerminalDecision, versionID string,
) {
	if args.Persist == nil || args.Clock == nil {
		return
	}
	if (td.LineageHint == ClaimLineageHint{}) {
		return
	}
	outcome := terminalOutcomeKey(td)
	now := args.Clock.Now()
	var subIDs []string
	if args.ClaimHandles != nil {
		children, cerr := args.ClaimHandles.ListChildClaimHandles(ctx, td.ClaimHandleID, tx)
		if cerr != nil && args.Logger != nil {
			args.Logger.Warn("ResolveClaimHandleTerminal: ListChildClaimHandles failed",
				"claim_handle_id", td.ClaimHandleID.String(),
				"error", cerr.Error())
		} else {
			subIDs = make([]string, 0, len(children))
			for _, c := range children {
				subIDs = append(subIDs, c.ID.String())
			}
		}
	}
	rec := ClaimTerminalRecord{
		ClaimHandleID:       td.ClaimHandleID,
		RunID:               td.LineageHint.RunID,
		OpenLineageRunRef:   td.LineageHint.RunID.String(),
		NodeID:              td.LineageHint.NodeID,
		FrameID:             td.LineageHint.FrameID,
		ParentClaimHandleID: td.ParentClaimHandleID,
		SubClaimHandleIDs:   subIDs,
		CommittedAt:         now.UTC().Format(time.RFC3339Nano),
		ProducerName:        td.LineageHint.ProducerName,
		ClaimScopeDataHash:  HashBytes(td.Scope),
		VersionID:           preferVersionID(versionID, td.LineageHint.VersionID),
		Outcome:             outcome,
	}
	if td.Outcome == AggregateAbandon && td.Cause != "" && td.Cause != TerminalCauseNatural {
		rec.Cause = string(td.Cause)
	}
	if lt := args.Persist.Lineage(); lt != nil {
		if err := WriteClaimTerminalLineage(ctx, tx, lt,
			td.LineageHint.InstanceID, td.LineageHint.FrameID,
			now, rec); err != nil && args.Logger != nil {
			args.Logger.Warn("ResolveClaimHandleTerminal: lineage write failed",
				"claim_handle_id", td.ClaimHandleID.String(),
				"outcome", outcome,
				"error", err.Error())
		}
	}
	kind := events.KindClaimResolutionCommit()
	payload := map[string]any{
		"claim_handle_id":       td.ClaimHandleID.String(),
		"run_id":                td.LineageHint.RunID.String(),
		"frame_id":              td.LineageHint.FrameID.String(),
		"producer_name":         td.LineageHint.ProducerName,
		"claim_scope_data_hash": rec.ClaimScopeDataHash,
		"version_id":            rec.VersionID,
	}
	if td.ParentClaimHandleID != nil {
		payload["parent_claim_handle_id"] = td.ParentClaimHandleID.String()
	}
	if td.Outcome == AggregateAbandon {
		kind = events.KindClaimResolutionAbandon()
		cause := td.Cause
		if cause == "" {
			cause = TerminalCauseNatural
		}
		payload["cause"] = string(cause)
		if rec.VersionID == "" {
			delete(payload, "version_id")
		}
	}
	nodeID := td.LineageHint.NodeID
	instanceID := td.LineageHint.InstanceID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       kind,
		Payload:    payload,
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("ResolveClaimHandleTerminal: event append failed",
			"claim_handle_id", td.ClaimHandleID.String(),
			"kind", kind.String(),
			"error", err.Error())
	}
}

func terminalOutcomeKey(td TerminalDecision) string {
	if td.Outcome == AggregateCommit {
		return persistence.LineageOutcomeCommitted
	}
	switch td.Cause {
	case TerminalCauseSiblingCancel, TerminalCauseDescendantCancel:
		return persistence.LineageOutcomeForceCancelled
	default:
		return persistence.LineageOutcomeAbandoned
	}
}

func preferVersionID(fromVerb, fromHint string) string {
	if fromVerb != "" {
		return fromVerb
	}
	return fromHint
}
