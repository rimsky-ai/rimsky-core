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

func outcomeVerbName(o TerminalOutcome) string {
	if o == OutcomeCommit {
		return "Commit"
	}
	if o.IsAbandon() {
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
	lineageParentClaimHandleID := td.ParentClaimHandleID
	if td.LineageParentClaimHandleID != nil {
		lineageParentClaimHandleID = td.LineageParentClaimHandleID
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
		ClaimHandleID:           td.ClaimHandleID,
		NodeRunID:               td.LineageHint.NodeRunID,
		OpenLineageRunRef:       td.LineageHint.NodeRunID.String(),
		NodeID:                  td.LineageHint.NodeID,
		FrameID:                 td.LineageHint.FrameID,
		ParentClaimHandleID:     lineageParentClaimHandleID,
		SubClaimHandleIDs:       subIDs,
		CommittedAt:             now.UTC().Format(time.RFC3339Nano),
		ProducerName:            td.LineageHint.ProducerName,
		ClaimScopeDataHash:      HashBytes(td.Scope),
		VersionID:               preferVersionID(versionID, td.LineageHint.VersionID),
		Outcome:                 outcome,
		TerminatingSupervisorID: td.SupervisorID,
	}
	switch td.Outcome {
	case OutcomeAbandonSiblingCancel, OutcomeAbandonDescendantCancel:
		rec.Cause = td.Outcome.CauseString()
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
		"run_id":                td.LineageHint.NodeRunID.String(),
		"frame_id":              td.LineageHint.FrameID.String(),
		"producer_name":         td.LineageHint.ProducerName,
		"claim_scope_data_hash": rec.ClaimScopeDataHash,
		"version_id":            rec.VersionID,
	}
	if lineageParentClaimHandleID != nil {
		payload["parent_claim_handle_id"] = lineageParentClaimHandleID.String()
	}
	if td.Outcome.IsAbandon() {
		kind = events.KindClaimResolutionAbandon()
		payload["cause"] = td.Outcome.CauseString()
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
	switch td.Outcome {
	case OutcomeCommit:
		return persistence.LineageOutcomeCommitted
	case OutcomeAbandonSiblingCancel, OutcomeAbandonDescendantCancel:
		return persistence.LineageOutcomeForceCancelled
	}
	return persistence.LineageOutcomeAbandoned
}

func preferVersionID(fromVerb, fromHint string) string {
	if fromVerb != "" {
		return fromVerb
	}
	return fromHint
}
