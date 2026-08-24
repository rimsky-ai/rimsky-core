// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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

// @concept: data-processing
// @decision: promotion-lineage-record-after-commit
func claimTerminalLineageWaitsForCommit(td TerminalDecision) bool {
	return td.Outcome == OutcomeCommit && len(td.CandidateHandle) > 0
}

func lineageParentOf(td TerminalDecision) *shared.UUID {
	if td.LineageParentClaimHandleID != nil {
		return td.LineageParentClaimHandleID
	}
	return td.ParentClaimHandleID
}

func subClaimHandleIDsFor(
	ctx context.Context, args RunArgs, claimHandleID shared.UUID, tx persistence.Tx,
) []string {
	if args.ClaimHandles == nil {
		return nil
	}
	children, err := args.ClaimHandles.ListChildClaimHandles(ctx, claimHandleID, tx)
	if err != nil {
		if args.Logger != nil {
			args.Logger.Warn("CLAIMHANDLE.CHILDHANDLES.LISTFAILED", "site", "ResolveClaimHandleTerminal",
				"claim_handle_id", claimHandleID.String(),
				"error", err.Error())
		}
		return nil
	}
	out := make([]string, 0, len(children))
	for _, c := range children {
		out = append(out, c.ID.String())
	}
	return out
}

// @concept: lineage-record
func claimTerminalRecordFor(
	ctx context.Context, args RunArgs, td TerminalDecision, observedAt time.Time, tx persistence.Tx,
) ClaimTerminalRecord {
	rec := ClaimTerminalRecord{
		ClaimHandleID:           td.ClaimHandleID,
		NodeRunID:               td.LineageHint.NodeRunID,
		OpenLineageRunRef:       td.LineageHint.NodeRunID.String(),
		NodeID:                  td.LineageHint.NodeID,
		FrameID:                 td.LineageHint.FrameID,
		ParentClaimHandleID:     lineageParentOf(td),
		SubClaimHandleIDs:       subClaimHandleIDsFor(ctx, args, td.ClaimHandleID, tx),
		CommittedAt:             observedAt.UTC().Format(time.RFC3339Nano),
		ProducerName:            td.LineageHint.ProducerName,
		ClaimScopeDataHash:      HashBytes(td.Scope),
		VersionID:               td.LineageHint.VersionID,
		Outcome:                 terminalOutcomeKey(td),
		TerminatingSupervisorID: td.SupervisorID,
	}
	switch td.Outcome {
	case OutcomeAbandonSiblingCancel, OutcomeAbandonDescendantCancel:
		rec.Cause = td.Outcome.CauseString()
	}
	return rec
}

// @decision: promotion-lineage-record-after-commit
func emitTerminalForensics(
	ctx context.Context, args RunArgs, td TerminalDecision, tx persistence.Tx,
) []byte {
	if args.Persist == nil || args.Clock == nil {
		return nil
	}
	if (td.LineageHint == ClaimLineageHint{}) {
		return nil
	}
	now := args.Clock.Now()
	rec := claimTerminalRecordFor(ctx, args, td, now, tx)
	var pending []byte
	if lt := args.Persist.Lineage(); lt != nil {
		if claimTerminalLineageWaitsForCommit(td) && ProducerVerbOutboxOf(args) != nil {
			marshalled, err := json.Marshal(rec)
			if err != nil {
				if args.Logger != nil {
					args.Logger.Warn("CLAIMHANDLE.PROMOTIONLINEAGE.STAGEFAILED", "site", "ResolveClaimHandleTerminal", "detail", "rimsky writes the lineage record at settlement without a version",
						"claim_handle_id", td.ClaimHandleID.String(),
						"error", err.Error())
				}
			} else {
				pending = marshalled
			}
		}
		if pending == nil {
			if err := WriteClaimTerminalLineage(ctx, lt, td.LineageHint.InstanceID, td.LineageHint.FrameID, now, rec, tx); err != nil && args.Logger != nil {
				args.Logger.Warn("CLAIMHANDLE.PROMOTIONLINEAGE.WRITEFAILED", "site", "ResolveClaimHandleTerminal",
					"claim_handle_id", td.ClaimHandleID.String(),
					"outcome", rec.Outcome,
					"error", err.Error())
			}
		}
	}
	kind := events.KindClaimResolutionCommit()
	payload := &genv1.ClaimResolutionSettledPayload{
		ClaimHandleId:      td.ClaimHandleID.String(),
		RunId:              td.LineageHint.NodeRunID.String(),
		FrameId:            td.LineageHint.FrameID.String(),
		ProducerName:       td.LineageHint.ProducerName,
		ClaimScopeDataHash: rec.ClaimScopeDataHash,
		VersionId:          rec.VersionID,
	}
	if parent := lineageParentOf(td); parent != nil {
		s := parent.String()
		payload.ParentClaimHandleId = &s
	}
	if td.Outcome.IsAbandon() {
		kind = events.KindClaimResolutionAbandon()
		payload.Cause = td.Outcome.CauseString()
	}
	nodeID := td.LineageHint.NodeID
	instanceID := td.LineageHint.InstanceID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID:     &nodeID,
		InstanceID: &instanceID,
		Kind:       kind,
		Payload:    eventpayload.New(payload),
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("CLAIMHANDLE.TERMINALEVENT.APPENDFAILED", "site", "ResolveClaimHandleTerminal",
			"claim_handle_id", td.ClaimHandleID.String(),
			"kind", kind.String(),
			"error", err.Error())
	}
	return pending
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
