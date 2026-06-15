// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoint_eval.go — supervisor-side checkpoint evaluator for
// concept:breakpoint. Invoked at the two cooperation points
// (before_dispatch from runtime/runner_dispatch.go and after_terminal
// from runtime/runner.go + runtime/callback.go), it lists the
// instance's breakpoints, evaluates the matcher (+ optional
// signal_type prefix on after_terminal hits), enforces the
// per-breakpoint overflow policy, writes a hit row, and either
// blocks until resume (pause mode) or continues (notify_only mode).
//
// Transaction discipline: every persistence call inside this file is
// wrapped in its own short-lived `args.Persist.Transaction(...)` —
// the `q(tx)` helper in foundation/persistence/postgres/backend.go
// panics on nil tx, and waitForResume polls on per-iteration short
// txns rather than holding a tx across the indefinite wait. Callers
// must invoke EvaluateBreakpoints OUTSIDE any acquisition /
// terminal-state tx (see runtime/runner_dispatch.go +
// runtime/runner.go + runtime/callback.go for the call sites).
//
// @concept: breakpoint

package runtime

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// breakpointQueueCap is the per-breakpoint unresumed-hit queue limit
// per spec §4.8. When the cap is reached, the overflow policy decides
// whether to evict the oldest hit (drop_oldest, notify_only only),
// block the new hit-write until a slot opens (block_dispatch /
// auto_resume_after_ttl), or fall through to the default block.
const breakpointQueueCap = 100

// breakpointResumePollInterval is the per-hit polling cadence used by
// waitForResume and the overflow-block loop. A short interval keeps
// the agent debugger feel snappy without flooding the database with
// SELECTs.
const breakpointResumePollInterval = 250 * time.Millisecond

// BreakpointInfraError wraps a persistence / transport failure raised by
// EvaluateBreakpoints. The before_dispatch caller distinguishes this
// from validation / resolution failures by type-checking — debugger
// infrastructure outages must NOT route through the
// `template_resolution_failed` policy chain (which would treat a
// transient DB blip as a template substitution problem and surface a
// misleading error class to operators).
//
// Context-cancellation paths inside handleOverflow / waitForResume are
// ALSO wrapped (Phase: "ctx_cancelled"). A bare ctx.Err() would skip
// the *BreakpointInfraError type-switch in runner_dispatch and surface
// as `template_resolution_failed` to operators — supervisor shutdown
// or request cancel during a breakpoint wait is a debugger-side
// concern, not a template substitution problem.
//
// The after_terminal call sites Warn-log and swallow regardless of the
// error type, so they treat infra errors identically to other failures
// — the dispatch is already complete; debugger problems can't change
// that.
//
// @concept: breakpoint
type BreakpointInfraError struct {
	// @constraint: Phase values are the closed set list_breakpoints, create_hit,
	// overflow_check, drop_oldest, wait_resume, ctx_cancelled — matched by
	// the before_dispatch caller's *BreakpointInfraError type-switch.
	Phase string
	Cause error
}

// Error renders the wrapped failure with a `breakpoint_infra:` prefix so
// log lines and event payloads stay distinguishable from
// template-resolution / template-validation errors.
func (e *BreakpointInfraError) Error() string {
	if e.Cause == nil {
		return "breakpoint_infra: " + e.Phase
	}
	return "breakpoint_infra: " + e.Phase + ": " + e.Cause.Error()
}

// Unwrap exposes the underlying persistence / transport error for
// errors.Is / errors.As callers.
func (e *BreakpointInfraError) Unwrap() error { return e.Cause }

// CheckpointContext is the dispatch context the breakpoint evaluator
// reads. The runtime-side identifier for the row is DispatchID (per
// runtime/runner_acquire.go); the persistence column name is
// rimsky_node_runs.id, but the Go field stays DispatchID for
// consistency with the rest of the runtime code. CheckpointContext is
// the snapshot input — buildSnapshot composes the final JSONB payload
// written to rimsky_breakpoint_hits.snapshot.
//
// @concept: breakpoint
type CheckpointContext struct {
	InstanceID shared.UUID
	// @constraint: NodeID is the owning graph node (rimsky_nodes.id), distinct
	// from DispatchID (rimsky_node_runs.id). The co-transactional breakpoint.hit
	// event-log row (concept:event-log) scopes to the owning node, not the run;
	// all three call sites already hold acq.NodeID and thread it in directly
	// rather than re-reading it from the node-run row.
	NodeID shared.UUID
	// @constraint: DispatchID is rimsky_node_runs.id; persisted column is the
	// run row, but the Go field stays DispatchID for consistency with the rest
	// of the runtime code.
	DispatchID       shared.UUID
	FrameID          shared.UUID
	Executor         string
	NodeType         string
	Graph            string
	ChildKey         string
	MergedAttributes map[string]any
	Checkpoint       persistence.BreakpointCheckpoint
	// @constraint: TerminalSignal is non-nil only for after_terminal checkpoints;
	// before_dispatch evaluations leave it nil and the signal_type prefix filter
	// is skipped.
	TerminalSignal *signalpkg.Signal
	// EffectiveSchema is the per-dispatch effective attribute schema
	// (executor.expected_attributes_schema ∪ L1 template defaults ∪
	// L2 node schema), threaded so buildSnapshot can record it on the
	// hit row's snapshot.effective_schema field. Resume-time overlay
	// validation in runtime/breakpoint_resume.go::lookupEffectiveSchemaForHit
	// reads this from the snapshot; without it, the spec §4.7
	// pre-merge validation step is skipped (with a Warn log) and the
	// supervisor-side defense-in-depth gate at the blocked runner's
	// resume is the only schema check that fires.
	EffectiveSchema map[string]any
	NodeRunSnapshot map[string]any
	HeldClaims      []map[string]any
	OpenWaitSet     []map[string]any
}

// EvaluateBreakpoints runs all matching breakpoints at the given
// checkpoint. For pause-mode hits it blocks until resume; for
// notify_only hits it writes the hit row and returns. Returns the
// (possibly-overlay-mutated) MergedAttributes for the caller to use
// in the actual dispatch — the after_terminal call sites discard the
// return value because the dispatch is already complete.
//
// Sequencing: per-iteration block contract. Matchers are evaluated in
// `ListForInstance` order (created_at ASC). When a pause-mode hit is
// written the loop blocks on `waitForResume` BEFORE evaluating the
// next breakpoint's matcher. Two pause-mode breakpoints that both
// match a single dispatch therefore produce two hits in sequence: hit
// 1 is written, the loop blocks on its resume, hit 2 is written after
// hit 1 resumes, the loop blocks on hit 2's resume, and only then
// does the dispatch proceed. The agent sees one hit per
// `resources/read` poll, not both at once. This trade is deliberate:
// pre-writing all matching hits before any resume would require
// snapshotting the matcher inputs (so breakpoint N+1's matcher does
// not observe breakpoint N's resume overlay) and would lose the
// ability for an operator to bail out of further breakpoint
// evaluation by deleting the unresumed downstream breakpoints during
// the pause. Spec §10.2 wording has been aligned to this contract.
//
// Matcher-input snapshot: the matcher for every iteration reads from
// `matcherInput`, the bag captured at function entry. This isolates
// breakpoint N+1's matcher from any L6 resume overlay applied at
// breakpoint N — the matcher view stays post-L5 across the loop, per
// spec §4.4. Overlays still mutate `result` (which feeds the actual
// dispatch + downstream attribute validation), they just don't feed
// back into matcher decisions for later breakpoints in the same
// EvaluateBreakpoints call.
//
// Transaction discipline: every persistence call opens its own short
// tx via args.Persist.Transaction. waitForResume polls on per-iteration
// short txns; no tx is held across the wait. The CALLER must NOT pass
// an outer tx (this function is invoked outside any dispatch tx — see
// runtime/runner_dispatch.go for before_dispatch and runtime/runner.go
// + runtime/callback.go for after_terminal).
//
// Failures: persistence / transport failures are wrapped in
// *BreakpointInfraError so the before_dispatch caller can route them
// distinctly from validation/resolution failures. The after_terminal
// call sites Warn-log and swallow (debugger problems shouldn't fail
// the run). Overlay-induced validation failures from the resume path
// flow through the existing `template_validation_failed` route per
// concept:error-policy when the caller re-validates the returned bag.
//
// @concept: breakpoint
func EvaluateBreakpoints(
	ctx context.Context,
	args RunArgs,
	cc CheckpointContext,
) (map[string]any, error) {
	var bps []persistence.BreakpointRow
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		bps, err = args.Persist.Breakpoints().ListForInstance(ctx, cc.InstanceID, false, tx)
		return err
	}); err != nil {
		return cc.MergedAttributes, &BreakpointInfraError{Phase: "list_breakpoints", Cause: err}
	}

	// @constraint: matcher-input snapshot bound once at entry so iteration N+1's
	// matcher does not observe iteration N's L6 resume overlay (spec §4.4 —
	// matcher reads the post-L5 snapshot).
	matcherInput := cc.MergedAttributes
	result := cc.MergedAttributes
	for _, bp := range bps {
		if bp.Checkpoint != cc.Checkpoint {
			continue
		}
		if !matcher.Evaluate(matcher.Matcher(bp.Matcher), matcher.Context{
			Executor:     cc.Executor,
			NodeType:     cc.NodeType,
			Graph:        cc.Graph,
			ChildKey:     cc.ChildKey,
			AttributeBag: matcherInput,
		}, args.Logger, 0) {
			continue
		}
		// @constraint: signal_type prefix filter per spec §4.5 — applies only to
		// after_terminal hits where the breakpoint declared a signal_type.
		// before_dispatch breakpoints have signal_type nil at create-time
		// validation (§7.1 / Pass 2 matcher package).
		if bp.SignalType != nil && cc.TerminalSignal != nil {
			if !cc.TerminalSignal.Type.HasPrefix(signalpkg.TypePath(*bp.SignalType)) {
				continue
			}
		}

		// @constraint: pre-write overflow handling per spec §4.8 — runs before the
		// hit row is created so the queue cap is enforced even under bursty hits.
		if err := handleOverflow(ctx, args, bp); err != nil {
			return result, err
		}

		// @constraint: NodeRunID / FrameID are nullable on the schema; pass nil
		// pointers when the CheckpointContext carries the zero-UUID so the
		// persisted column honors its nullability semantics rather than recording
		// a meaningless all-zero value. The before_dispatch path always populates
		// both; some after_terminal call sites (e.g. callback paths with partial
		// AsyncContext) may pass zero values, which the schema represents as NULL.
		var (
			hitID       shared.UUID
			nodeRunIDPt *shared.UUID
			frameIDPt   *shared.UUID
		)
		if cc.DispatchID != (shared.UUID{}) {
			id := cc.DispatchID
			nodeRunIDPt = &id
		}
		if cc.FrameID != (shared.UUID{}) {
			id := cc.FrameID
			frameIDPt = &id
		}
		// @constraint: co-transactional hit + event-log row — the breakpoint.hit
		// event is appended inside the SAME tx that creates the ledger hit row, so
		// a recorded hit is ALWAYS reflected on /events (both commit or both roll
		// back). An Append failure rolls the hit back with it and routes through
		// the debugger-infra path (Phase:"create_hit"), never leaving an orphaned
		// hit with no event or vice-versa.
		// @concept: event-log
		nodeIDPt := nodeIDPtr(cc.NodeID)
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			hitID, _, err = args.Persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bp.ID,
				InstanceID:   cc.InstanceID,
				NodeRunID:    nodeRunIDPt,
				FrameID:      frameIDPt,
				Checkpoint:   cc.Checkpoint,
				Mode:         bp.Mode,
				Snapshot:     buildSnapshot(cc),
			}, tx)
			if err != nil {
				return err
			}
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &cc.InstanceID,
				NodeID:     nodeIDPt,
				Kind:       events.KindBreakpointHit(),
				Payload: map[string]any{
					"instance_id":   cc.InstanceID.String(),
					"node_id":       cc.NodeID.String(),
					"breakpoint_id": bp.ID.String(),
					"hit_id":        hitID.String(),
					"checkpoint":    string(cc.Checkpoint),
					"mode":          string(bp.Mode),
				},
			}, tx)
		}); err != nil {
			return result, &BreakpointInfraError{Phase: "create_hit", Cause: err}
		}

		if bp.Mode == persistence.BreakpointModeNotifyOnly {
			continue
		}

		hit, err := waitForResume(ctx, args, hitID)
		if err != nil {
			return result, err
		}
		if hit != nil && hit.ResumeOverlay != nil {
			merged, _ := shared.DeepMergeJSON(result, hit.ResumeOverlay).(map[string]any)
			if merged != nil {
				// @constraint: defense-in-depth validation runs in the caller before
				// dispatch — this function returns the merged bag and the caller
				// routes validation failures through template_validation_failed per
				// concept:error-policy. matcherInput stays bound to the pre-overlay
				// snapshot so later iterations' matchers don't observe this overlay.
				result = merged
			}
		}
	}
	return result, nil
}

// handleOverflow implements the per-policy cap behavior per spec §4.8.
// Each persistence call opens its own short tx. drop_oldest evicts
// the oldest unresumed row + increments the breakpoint's
// dropped_count; block_dispatch / auto_resume_after_ttl spin on a
// short poll until the queue drains (the sweeper drains
// auto_resume_after_ttl entries in the background).
//
// Persistence failures are wrapped in *BreakpointInfraError so the
// before_dispatch caller can route them distinctly from
// resolution/validation failures (see EvaluateBreakpoints doc).
func handleOverflow(ctx context.Context, args RunArgs, bp persistence.BreakpointRow) error {
	warnedUnknownPolicy := false
	for {
		var unresumed int
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			unresumed, err = args.Persist.BreakpointHits().UnresumedCount(ctx, bp.ID, tx)
			return err
		}); err != nil {
			return &BreakpointInfraError{Phase: "overflow_check", Cause: err}
		}
		if unresumed < breakpointQueueCap {
			return nil
		}
		switch bp.OverflowPolicy {
		case persistence.OverflowDropOldest:
			if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if _, err := args.Persist.BreakpointHits().DropOldest(ctx, bp.ID, breakpointQueueCap-1, tx); err != nil {
					return err
				}
				return args.Persist.Breakpoints().IncrementDropped(ctx, bp.ID, tx)
			}); err != nil {
				return &BreakpointInfraError{Phase: "drop_oldest", Cause: err}
			}
			return nil
		case persistence.OverflowBlockDispatch, persistence.OverflowAutoResumeAfterTTL:
			// @constraint: block until something drains — the sweeper handles
			// auto_resume_after_ttl drainage in the background.
			select {
			case <-ctx.Done():
				// @deliberate: wrap as *BreakpointInfraError so the before_dispatch
				// caller's errors.As routes ctx-cancel through the debugger-infra
				// path rather than the attribute-failure chain (which would surface
				// ctx-cancel as `template_resolution_failed`).
				return &BreakpointInfraError{Phase: "ctx_cancelled", Cause: ctx.Err()}
			case <-time.After(breakpointResumePollInterval):
			}
		default:
			// @deliberate: unknown policy defaults to block; log once at Warn so
			// operators can spot a corrupted policy value instead of watching the
			// loop spin silently.
			if !warnedUnknownPolicy && args.Logger != nil {
				args.Logger.Warn("breakpoint.overflow.unknown_policy",
					"breakpoint_id", bp.ID.String(),
					"overflow_policy", string(bp.OverflowPolicy),
					"action", "defaulting to block until queue drains")
				warnedUnknownPolicy = true
			}
			select {
			case <-ctx.Done():
				return &BreakpointInfraError{Phase: "ctx_cancelled", Cause: ctx.Err()}
			case <-time.After(breakpointResumePollInterval):
			}
		}
	}
}

// waitForResume polls the hit row until resumed_at != NULL, opening a
// fresh tx per poll iteration. Returns nil hit if the row was
// cascade-deleted via parent-breakpoint delete (treated as
// auto-resume with no overlay).
//
// Persistence failures are wrapped in *BreakpointInfraError so the
// before_dispatch caller can route them distinctly from
// resolution/validation failures (see EvaluateBreakpoints doc).
func waitForResume(ctx context.Context, args RunArgs, hitID shared.UUID) (*persistence.BreakpointHitRow, error) {
	for {
		var hit *persistence.BreakpointHitRow
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			hit, err = args.Persist.BreakpointHits().Get(ctx, hitID, tx)
			return err
		}); err != nil {
			return nil, &BreakpointInfraError{Phase: "wait_resume", Cause: err}
		}
		if hit == nil {
			// @constraint: hit was cascade-deleted — FK ON DELETE CASCADE on
			// rimsky_breakpoint_hits.breakpoint_id removes hits when their parent
			// breakpoint is deleted; treat as auto-resume with no overlay.
			return nil, nil
		}
		if hit.ResumedAt != nil {
			return hit, nil
		}
		select {
		case <-ctx.Done():
			// @deliberate: wrap as *BreakpointInfraError so the before_dispatch caller
			// routes ctx-cancel through the debugger-infra path instead of the
			// attribute-failure chain.
			return nil, &BreakpointInfraError{Phase: "ctx_cancelled", Cause: ctx.Err()}
		case <-time.After(breakpointResumePollInterval):
		}
	}
}

// nodeIDPtr returns a heap pointer to id, or nil when id is the zero
// UUID. rimsky_events.node_id is nullable; passing nil for a missing
// owning node honors that nullability rather than persisting an
// all-zero placeholder (mirrors the nodeRunIDPt / frameIDPt handling at
// the hit-create site). The dispatch call site always populates NodeID;
// only degenerate after_terminal contexts could be zero here.
func nodeIDPtr(id shared.UUID) *shared.UUID {
	if id == (shared.UUID{}) {
		return nil
	}
	out := id
	return &out
}

// buildSnapshot constructs the JSONB snapshot payload per spec §4.6.
// The effective_schema field is the load-bearing carrier for
// resume-time overlay validation in
// runtime/breakpoint_resume.go::lookupEffectiveSchemaForHit; omit it
// only when no schema is known (the resume path then falls back to the
// spec §4.7 step-4 defense-in-depth gate at the supervisor).
func buildSnapshot(cc CheckpointContext) map[string]any {
	snap := map[string]any{
		"checkpoint": string(cc.Checkpoint),
		"dispatch_context": map[string]any{
			"executor":          cc.Executor,
			"node_type":         cc.NodeType,
			"graph":             cc.Graph,
			"child_key":         cc.ChildKey,
			"merged_attributes": cc.MergedAttributes,
		},
		"node_run":      cc.NodeRunSnapshot,
		"held_claims":   cc.HeldClaims,
		"open_wait_set": cc.OpenWaitSet,
	}
	if len(cc.EffectiveSchema) > 0 {
		snap["effective_schema"] = cc.EffectiveSchema
	}
	if cc.TerminalSignal != nil {
		snap["terminal_signal"] = map[string]any{
			"type":    string(cc.TerminalSignal.Type),
			"payload": cc.TerminalSignal.Payload,
		}
	}
	return snap
}
