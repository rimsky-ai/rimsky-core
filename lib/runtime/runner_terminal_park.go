// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Park-terminal handler — applies the protocol-level Park
// terminal event.
//
//   - Logs WARN when Park.reason is empty (permitted, discouraged).
//   - Persists park metadata (parked_at, resume_at, parked_reason,
//     parked_reason_label, parked_reason_note) to rimsky_node_runs.
//   - Commits the Park outcome's attributes_delta atomically with the
//     park transition (per TD-attributes-delta-on-all-settling-terminals).
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
// `resolvedAttrs` is the dispatch-time substituted attribute view; the
// executor's `t.AttributesDel` (TD-attributes-delta-on-all-settling-
// terminals) is merged against it and upserted into the node's
// attribute row inside the caller-provided tx. This makes the delta
// visible to the resume dispatch (attribute carry-forward) — the
// claude-agent session-token round-trip (TD-claude-agent-session-
// attribute-only) and any other attribute the executor authored on
// the Park outcome both ride this writeback.
//
// Per the post-collapse ParkReason invariant (proto closed two-value
// set: AWAIT_CALLBACK | SNOOZE; see proto:executor.proto::ParkReason),
// the runtime accepts park terminals without enum-value rejection —
// the proto wire layer caps the set at decode and both values are
// unconditionally valid here.
func applyTerminalPark(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, t terminalEvent, tx persistence.Tx,
) (postCommitFn, error) {

	// @constraint: max_park_duration_seconds is read from the node template so the
	// watchdog can find an overdue cutoff; zero/empty stays NULL (no cap).
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
	// @deliberate: Per TD-remove-resume-context resume state rides
	// attribute carry-forward — the executor that needs to thread state
	// across the park boundary writes it into AttributesDel on this
	// Park outcome, and the post-park attribute writeback below makes
	// it visible to the next dispatch.
	in := persistence.ParkActiveInput{
		DispatchID:        acq.DispatchID,
		ExpectedClaimedBy: args.SupervisorID,
		ParkedAt:          now,
		ResumeAt:          t.ParkResumeAt,
		Reason:            parkReasonStorageForm(t.ParkReason),
		ReasonNote:        t.ParkReasonNote,
		ReasonLabel:       t.ParkReasonLabel,
	}

	if err := args.Queue.ParkActiveInTx(ctx, tx, in); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @deliberate: attributes_delta upsert rides the park tx so the resume dispatch
	// sees the executor-authored attribute view (e.g., session_token).
	// Empty AttributesDel short-circuits without touching the row.
	// @concept: attribute
	if len(t.AttributesDel) > 0 {
		merged := mergeAttributesDelta(resolvedAttrs, t.AttributesDel)
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: upsert attributes_delta: %w", err)
		}
	}
	// @deliberate: scratch is persisted onto the dispatch row inside the park tx so
	// the column survives across the parked → pending resume transition and the
	// resume dispatch sees the scratch the park terminal attached. Inline vs.
	// spilled-handle picked via the BlobBackend's per-byte spill threshold.
	// @concept: executor
	if err := applyTerminalScratchInTx(ctx, args, tx, acq, t.Scratch); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @constraint: per-row dispatch tuning is denormalized at park-time so
	// SweepParkedNodes can find the deadline without joining through templates (F2/F3).
	if maxParkSec != nil || maxRetries != nil {
		if err := args.Queue.UpdateDispatchTuningInTx(ctx, tx, acq.DispatchID, maxParkSec, maxRetries); err != nil {
			return nil, fmt.Errorf("applyTerminalPark: %w", err)
		}
	}
	// @constraint: acq.RunScopeID is threaded so fan-out children's state-machine
	// update lands on the correct sibling row; settling_signal_type carries the
	// terminal/park/<reason> envelope.
	// @concept: signal
	parkSigType := string(parkTerminalSignal(t).Type)
	if err := args.Persist.Nodes().UpdateState(ctx, acq.NodeID, acq.RunScopeID,
		cascade.NodeStateParked, cascade.ReasonHandlerPark, &parkSigType, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @constraint: parked is a settled state for wait-set purposes — any wait-set
	// rows gating receivers on this sender's run must release here.
	if err := drainWaitSetOnSettled(ctx, args, tx, acq.FrameID, acq.DispatchID); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: %w", err)
	}
	// @deliberate: park emits a BARE audit row, NOT through the emitSignalInTx
	// chokepoint, because terminal/park does NOT cascade-fire subscribers. Park is
	// a transient suspension, not a run settlement: the node resumes and only then
	// emits a real terminal disposition. A `terminal/*` subscriber means "react
	// when the upstream is DONE", so firing it on a park would pull the downstream
	// into the frame prematurely (a held-claim inheritor would dispatch before its
	// acquirer resumes and commits — see TestParkedLifecycleHeldClaimRetentionAcrossPark).
	// The drain above only releases receivers already gated on this run; it does
	// not affirm new ones. The two ParkReason values map to two leaves of the
	// terminal/park subtree (closed two-value set). AwaitAsyncCallback is NOT a
	// park (transient/await_async; emitted at runner_dispatch.go).
	// @concept: signal
	parkSig := parkTerminalSignal(t)
	if err := signalaudit.EmitSignal(ctx, args.Persist.Events(),
		acq.InstanceID, acq.NodeID, parkSig, now, tx); err != nil {
		return nil, fmt.Errorf("applyTerminalPark: emit signal: %w", err)
	}

	dispatchID := acq.DispatchID
	post := func(ctx context.Context) {
		scope := resolveAcqScope(ctx, args, acq)
		EmitLeafRunLineage(ctx, args, LeafRunEmitInput{
			InstanceID:         acq.InstanceID,
			FrameID:            acq.FrameID,
			RunID:              dispatchID,
			NodeID:             acq.NodeID,
			State:              string(cascade.NodeStateParked),
			SettlingSignalType: parkSigType,
			TerminalKind:       "park",
			NodeAlias:          acq.NodeType,
			ExecutorName:       acq.Executor,
			TemplateHash:       acq.TemplateHash,
			Params:             acq.InstanceParams,
			AttributesMerged:   acq.MergedAttributes,
			HeldClaims:         HeldClaimsForLineage(acq),
			ParentRunID:        scope.ParentRunID,
			ChildKey:           scope.PartitionKey,
			SubstitutionRefs:   CollectSubstitutionRefsForEmit(ctx, args, acq),
		})
		// @constraint: a parked child still produces a settled per-child state for
		// the aggregator; settling_signal_type carries the terminal/park/<reason>
		// envelope so the aggregator matches parked children uniformly with other
		// settled outcomes.
		propagateSig := parkSigType
		if _, err := PropagateIfChildAfterTerminal(ctx, args, dispatchID,
			cascade.NodeStateParked, &propagateSig); err != nil {
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
// @source: lib/foundation/persistence/blob_spill.go:ShouldSpillBlob
func shouldSpillBlob(args RunArgs, size int) bool {
	return persistence.ShouldSpillBlob(args.Blob, args.BlobSpillThreshold, size)
}

// parkTerminalSignal constructs the canonical park-terminal signal
// envelope. PARK_REASON_SNOOZE → terminal/park/snooze;
// PARK_REASON_AWAIT_CALLBACK → terminal/park/await_callback. Per
// TD-remove-resume-context, Park no longer carries a session_token /
// park_payload on the signal payload — resume state rides attribute
// carry-forward; the canonical payload now is reason metadata + the
// settling terminal's tags.
//
//	@concept: signal
func parkTerminalSignal(t terminalEvent) signalpkg.Signal {
	payload := map[string]any{
		"parked_reason_label": t.ParkReasonLabel,
		"parked_reason_note":  t.ParkReasonNote,
		"tags":                t.Tags,
	}
	if t.ParkReason == genv1.ParkReason_PARK_REASON_SNOOZE {
		payload["resume_at"] = t.ParkResumeAt
		return signalpkg.Signal{
			Type:    "terminal/park/snooze",
			Payload: payload,
		}
	}
	// @deliberate: AWAIT_CALLBACK omits resume_at when zero so the
	// payload stays value-based; missing-key lookup returns nil to
	// consumers, matching the prior pointer-nil observable.
	if !t.ParkResumeAt.IsZero() {
		payload["resume_at"] = t.ParkResumeAt
	}
	return signalpkg.Signal{
		Type:    "terminal/park/await_callback",
		Payload: payload,
	}
}

// resolveMaxRetriesCap returns the effective max-retries-without-progress
// for a node-run, applying the per-row override → deployment
// default → built-in default precedence per plan E5. Returns 0 (cap
// disabled) when the per-row override is explicitly 0.
func resolveMaxRetriesCap(args RunArgs, override *int) int {
	if override != nil {
		// @constraint: explicit override (including 0 = disable cap) wins.
		return *override
	}
	if args.MaxRetriesWithoutProgressDefault > 0 {
		return args.MaxRetriesWithoutProgressDefault
	}
	return 100
}
