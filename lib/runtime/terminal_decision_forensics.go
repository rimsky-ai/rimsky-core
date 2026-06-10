// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// terminal_decision_forensics.go — observability emission for the
// terminal-decision engine. Companion to `terminal_decision.go`; this
// file holds the narrow helpers that emit the per-terminal lineage
// row + the `claim_resolution.*` event after a producer verb fires.
//
// Honors `@blessed-invariant 20` (claim content inert) and `@blessed-
// invariant 21` (messages inert): payloads carry only the claim_scope_data
// hash + rimsky-side identifiers — never the raw scope / address /
// candidate-handle / version bytes.

package runtime

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// outcomeVerbName maps AggregateOutcome to the producer-verb name for
// error messages.
func outcomeVerbName(o AggregateOutcome) string {
	switch o {
	case AggregateCommit:
		return "Commit"
	case AggregateAbandon:
		return "Abandon"
	}
	return "Unknown"
}

// emitTerminalForensics emits the per-terminal `claim_terminal` lineage
// row plus the matching `claim_resolution.*` event after a producer
// verb fires. Single emit site for every Commit/Abandon path; the
// lineage projection + the event log stay in sync regardless of which
// branch (executor terminal, auto-terminal, force-cancel) drove the
// resolution.
//
// Best-effort writes: both helpers tolerate missing dependencies (nil
// Persist / Clock / Lineage / Events) and log on error rather than
// failing the surrounding tx. The lineage + event surfaces are
// observability metadata, not control-plane state.
//
// Honors `@blessed-invariant 20` (claim content inert) and `@blessed-
// invariant 21` (messages inert): payloads carry only the claim_scope_data
// hash + rimsky-side identifiers (claim_handle_id, run_id, frame_id,
// producer_name, version_id, outcome, cause). Raw scope / address /
// candidate-handle / version bytes never appear in the projection.
func emitTerminalForensics(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	td TerminalDecision, versionID string,
) {
	if args.Persist == nil || args.Clock == nil {
		return
	}
	// Lineage hint must carry enough context to be useful; skip the
	// projection write when the call site lacks instance / frame
	// metadata (the per-call lineage hint is optional and some callers
	// — e.g. cycle-4 pre-rename paths — fire without filling it in).
	if (td.LineageHint == ClaimLineageHint{}) {
		return
	}
	outcome := terminalOutcomeKey(td)
	now := args.Clock.Now()
	// Walk the immediate sub-claim list under this claim_handle (one
	// level) so the lineage row carries the fan-out manifest the
	// OpenLineage emitter renders as the per-claim sub-claim_handle_ids
	// facet. ListChildClaimHandles is a single SELECT; pre-v1 we keep
	// it bounded at one level to avoid quadratic walks on deep trees —
	// downstream consumers can chain ancestor lookups via
	// `GET /lineage/claims/{id}/ancestors?depth=N`.
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
		ClaimHandleID: td.ClaimHandleID,
		RunID:         td.LineageHint.RunID,
		// OpenLineageRunRef seeds the OpenLineage emitter's
		// `Run.RunID` (subscribers/openlineage/emitter.go::
		// MakeClaimTerminalEvent). Using the holding-run's RunID
		// keeps the OL graph aligned with the run that resolved the
		// claim. NOT a parent-run reference in the run-tree sense —
		// see the struct comment on ClaimTerminalRecord.
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
	// Event payload mirrors the lineage shape but excludes node_id (the
	// event row already carries it as a column). The kind discriminates
	// commit vs abandon; the cause field carries the abandon-flavor.
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
		// version_id is meaningful on Commit (the producer's emitted
		// version label); on Abandon it is rarely populated and never
		// load-bearing. Drop the key when empty so the event payload
		// stays tight.
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

// terminalOutcomeKey maps the typed (Outcome, Cause) pair to the
// persistence-layer `outcome` column value. Force-cancelled Abandons
// promote to `force_cancelled` so analytical queries can isolate the
// operator-/sibling-driven branch from natural exhaustion.
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

// preferVersionID picks the verb-returned version (from a successful
// CommitCandidate) over the hint-supplied one. Falls back to the hint
// (e.g. a Held-claim row that was already labeled with a prior version)
// when the verb didn't produce a fresh value.
func preferVersionID(fromVerb, fromHint string) string {
	if fromVerb != "" {
		return fromVerb
	}
	return fromHint
}
