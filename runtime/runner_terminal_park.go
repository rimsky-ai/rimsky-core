// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Park-terminal handler — applies the protocol-level Park
// terminal event. Per the 2026-05-08 platform-extensions plan E1:
//
//   - Logs WARN when Park.reason is empty (permitted, discouraged).
//   - Persists park metadata (parked_at, resume_at, parked_reason,
//     session_token) to rimsky_node_runs.
//   - Spills payloads larger than BlobSpillThreshold through the
//     configured BlobBackend; smaller payloads stored inline. NOTE: when
//     the backend is the degenerate "inline" backend (Blob.Name() ==
//     "inline"), shouldSpillBlob returns false regardless of size, so all
//     payloads are stored inline — this is intentional, the inline
//     backend is the no-spill option.
//   - Transitions phase active→parked (clears claimed_by so the orphan-
//     claim reaper's `claimed_by IS NOT NULL` predicate excludes the row).
//   - Transitions node state running→parked via cascade.ReasonHandlerPark.
//
// Held claim handles are NOT released here — held-claim semantics already
// retain claims across the park boundary (see the auto-terminal mechanism
// in runtime/auto_terminal.go::CheckAndFireResolution).

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// parkReasonStorageForm converts the proto ParkReason enum to the
// snake_case text stored in col:rimsky_node_runs.parked_reason. The
// same form drives the diagnostics endpoint, the rimsky flag, and
// the Prometheus gauge label.
//
//	@concept: parked-state
func parkReasonStorageForm(r genv1.ParkReason) string {
	s := strings.TrimPrefix(r.String(), "PARK_REASON_")
	return strings.ToLower(s)
}

// parkReasonFromStorageForm inverts parkReasonStorageForm: given the
// snake_case text (used on the async-callback body), return the proto
// enum value. Unrecognized inputs fall back to
// PARK_REASON_AWAIT_CALLBACK, the safer of the two values in the
// closed set (no auto-resume).
//
//	@concept: parked-state
func parkReasonFromStorageForm(s string) genv1.ParkReason {
	upper := "PARK_REASON_" + strings.ToUpper(s)
	if v, ok := genv1.ParkReason_value[upper]; ok {
		return genv1.ParkReason(v)
	}
	return genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK
}

// applyTerminalPark handles the Park terminal event. Persists
// the park metadata, spills large payloads, transitions the node-run
// phase to parked, and transitions the node state to parked.
//
// Per the post-collapse ParkReason invariant (proto closed two-value
// set: AWAIT_CALLBACK | SNOOZE; see proto:executor.proto::ParkReason),
// the runtime no longer rejects park terminals on enum value: the
// proto wire layer caps the set at decode, and both values are
// unconditionally valid here.
func applyTerminalPark(
	ctx context.Context, args RunArgs, acq *acquisition, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {

	// Compute payload spill: inline-or-handle based on threshold and
	// backend availability. Blob.Write is out-of-DB-tx by design (the
	// blob backend is its own storage); fine to call inside the
	// supervisor's outer tx.
	var (
		payloadInline        []byte
		payloadHandle        string
		payloadHandleBackend string
	)
	if shouldSpillBlob(args, len(t.ParkPayload)) {
		key := persistence.BlobKey{
			NodeID: acq.NodeID.String(),
			Hint:   "parked_payload",
		}
		h, err := args.Blob.Write(ctx, key, t.ParkPayload)
		if err != nil {
			// Spill failure → fall back to inline. Blob writes failing
			// must not block the park transition; the operator's
			// attribute-storage size constraints will surface separately.
			args.Logger.Warn("applyTerminalPark: blob spill failed; falling back to inline",
				"node_id", acq.NodeID.String(), "error", err.Error())
			payloadInline = t.ParkPayload
		} else {
			payloadHandle = string(h)
			payloadHandleBackend = args.Blob.Name()
		}
	} else {
		payloadInline = t.ParkPayload
	}

	// Resolve max_park_duration_seconds from the node's template DSL so
	// the watchdog can find an overdue cutoff. Zero/empty → don't write
	// (NULL = no cap).
	var maxParkSec *int
	if acq.NodeDef != nil && acq.NodeDef.MaxParkDuration != "" {
		if d, err := time.ParseDuration(acq.NodeDef.MaxParkDuration); err == nil {
			s := int(d.Seconds())
			if s > 0 {
				maxParkSec = &s
			}
		}
	}
	var maxRetries *int
	if acq.NodeDef != nil && acq.NodeDef.MaxRetriesWithoutProgress != nil {
		v := *acq.NodeDef.MaxRetriesWithoutProgress
		maxRetries = &v
	}

	now := args.Clock.Now()
	in := persistence.ParkActiveInput{
		DispatchID:           acq.DispatchID,
		ExpectedClaimedBy:    args.SupervisorID,
		ParkedAt:             now,
		ResumeAt:             t.ParkResumeAt,
		Reason:               parkReasonStorageForm(t.ParkReason),
		ReasonNote:           t.ParkReasonNote,
		ReasonLabel:          t.ParkReasonLabel,
		SessionToken:         t.ParkSessionToken,
		PayloadInline:        payloadInline,
		PayloadHandle:        payloadHandle,
		PayloadHandleBackend: payloadHandleBackend,
	}

	// Primary state-mutation work runs inline in the caller's outer tx.
	if err := args.Queue.ParkActiveInTx(ctx, tx, in); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// Per-row dispatch tuning denormalization (F2/F3) — populated at
	// park-time so SweepParkedNodes can find the deadline without
	// joining through templates.
	if maxParkSec != nil || maxRetries != nil {
		if err := args.Queue.UpdateDispatchTuningInTx(ctx, tx, acq.DispatchID, maxParkSec, maxRetries); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: %w", err)
		}
	}
	// Transition node state running → parked. Thread acq.RunScopeID
	// so fan-out children's state-machine update lands on the
	// correct sibling row.
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateParked, cascade.ReasonHandlerPark, "", tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// Settled-state drain on park: the sender reached a settled
	// state (parked); any wait-set rows gating receivers on this
	// sender's run release.
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// Audit-log the park event.
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "park_requested",
		Payload: map[string]any{
			"reason":            parkReasonStorageForm(t.ParkReason),
			"reason_note":       t.ParkReasonNote,
			"reason_label":      t.ParkReasonLabel,
			"resume_at":         resumeAtForLog(t.ParkResumeAt),
			"has_session_token": t.ParkSessionToken != "",
			"payload_bytes":     len(t.ParkPayload),
			"spilled_to_blob":   payloadHandle != "",
		},
	}, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}

	// Post-commit: lineage emit, run-tree propagation.
	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		// E8: emit leaf-run lineage record for the park terminal.
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:       acq.InstanceID,
			FrameID:          acq.FrameID,
			RunID:            dispatchID,
			NodeID:           acq.NodeID,
			State:            string(cascade.NodeStateParked),
			TerminalKind:     "park",
			NodeAlias:        acq.NodeType,
			ExecutorName:     acq.Executor,
			TemplateHash:     acq.TemplateHash,
			Params:           acq.InstanceParams,
			AttributesMerged: acq.MergedAttributes,
			HeldClaims:       HeldClaimsForLineage(acq),
			ParentRunID:      scope.ParentRunID,
			ChildKey:         scope.PartitionKey,
			SubstitutionRefs: CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		// Run-tree state propagation (E2): a parked child still
		// produces a settled per-child state for the aggregator. Empty
		// LastOutcome because park has no fresh-vs-changed distinction.
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateParked, ""); err != nil {
			args.Logger.Warn("applyTerminalPark: run-tree propagation failed",
				"run_id", dispatchID.String(), "error", err.Error())
		}
	}
	return post, nil
}

// shouldSpillBlob is a thin wrapper around persistence.ShouldSpillBlob
// that takes a RunArgs. The persistence version is the canonical
// spill-decision; both sites must agree so a value spilled at write time
// can be read back without ambiguity.
//
// @source: foundation/persistence/blob_spill.go:ShouldSpillBlob
func shouldSpillBlob(args RunArgs, size int) bool {
	return persistence.ShouldSpillBlob(args.Blob, args.BlobSpillThreshold, size)
}

// resumeAtForLog formats time.Time for slog. The zero value renders as
// the empty string so the audit-log row is honest about "no deadline."
func resumeAtForLog(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}

// resolveMaxRetriesCap returns the effective max-retries-without-progress
// for a node-run, applying the per-row override → deployment
// default → built-in default precedence per plan E5. Returns 0 (cap
// disabled) when the per-row override is explicitly 0.
func resolveMaxRetriesCap(args RunArgs, override *int) int {
	if override != nil {
		// Explicit override (including 0 = disable cap).
		return *override
	}
	if args.MaxRetriesWithoutProgressDefault > 0 {
		return args.MaxRetriesWithoutProgressDefault
	}
	return 100 // built-in default
}
