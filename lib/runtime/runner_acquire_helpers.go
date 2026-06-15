// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Helpers split out of runner_acquire.go to keep the orchestration
// shell (tryAcquire) focused on the §7.3 atomic-acquisition steps
// without the per-feature detail of fan-out sub-claim acquisition or
// resume-metadata reload. Both helpers preserve the original
// transactional semantics — they run inside the caller's open
// rimsky-side tx and roll back together with the rest of tryAcquire
// on any returned error.
//
// @source: lib/runtime/runner_acquire.go::tryAcquire (cycle-7 extraction)

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// acquireFanOutIfDeclared performs the E4 atomic sub-claim acquisition
// when the template node declares `fan_out:`. Splits the parent claim's
// scope into sub-scopes inside the caller's open tx. Sub-claims persist
// with `parent_claim_handle_id` pointing at the parent so the recursive
// auto-terminal (E3) resolves bottom-up correctly. A failure aborts the
// whole acquisition (returned error rolls the caller's tx back).
//
// @concept: fan-out
// @concept: claim-tree
func acquireFanOutIfDeclared(
	ctx context.Context, args RunArgs, tx persistence.Tx, instanceID shared.UUID, out *acquisition,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef,
	acquiredLocks []AcquiredLock, heartbeatInterval time.Duration,
) error {
	if nodeDef == nil || nodeDef.FanOut == nil {
		return nil
	}
	// @constraint: only the root run of a fan-out tree splits. Children re-use the
	// parent's node_id (per `runtime/fanout_dispatch.go::dispatchFanOutChildren`)
	// and therefore inherit the same `nodeDef.FanOut` block; without this
	// guard each child re-fires SplitScope and creates grand-children
	// indefinitely. The "child" predicate is "this run's RunScope has a
	// parent_run_id" — fan-out-partition / sub-graph RunScopes carry one,
	// the main RunScope does not.
	//
	// @concept: fan-out
	// @concept: run-scope
	if scopes := args.Persist.RunScopes(); scopes != nil {
		if rs, err := scopes.GetByID(ctx, tx, out.RunScopeID); err == nil && rs != nil && rs.ParentRunID != nil {
			return nil
		}
	}
	// @constraint: locate the acquiredLocks entry whose Alias matches the
	// FanOut.Claim reference. The validator (D4) rejects fan_out blocks
	// that reference an unknown alias, so this lookup is best-effort
	// safe at runtime.
	fanOutClaim := nodeDef.FanOut.Claim
	var parent *AcquiredLock
	for i := range acquiredLocks {
		if acquiredLocks[i].Alias == fanOutClaim {
			parent = &acquiredLocks[i]
			break
		}
	}
	if parent == nil {
		return nil
	}
	// @constraint: `parent.Spec` is `any` — narrow to ClaimSpec; named locks
	// can't be fan-out targets (no producer name).
	parentClaimSpec, ok := parent.Spec.(claimproducer.ClaimSpec)
	if !ok {
		args.Logger.Warn("tryAcquire: fan-out alias references non-claim spec; ignored",
			"node_id", cand.NodeID.String(),
			"alias", fanOutClaim)
		return nil
	}
	frameID := cand.FrameID
	// @constraint: substitute partition_request with the runtime-resolved trigger
	// payload before handing it to SplitScope. The fan-out node's
	// partition_request is authored to pull an operator-supplied
	// override off the triggering message (canonical form
	// `{{trigger.message.payload.partition_request_override |
	// <template-default>}}`); the override rides the delivered
	// message's payload keyed to this frame.
	//
	// Load-bearing property: the bytes that reach AcquireSubClaims /
	// SplitScope are the SUBSTITUTED bytes (the override genuinely
	// binds), not the literal template. Passing the literal verbatim
	// silently drops every override because the `{{trigger…}}`
	// directive is never resolved and the `|`-fallback to the
	// template default always fires.
	partitionRequest, err := substituteFanOutPartitionRequest(ctx, args, tx, frameID, out, nodeDef.FanOut.PartitionRequest)
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out partition_request substitution failed",
			"node_id", cand.NodeID.String(),
			"error", err.Error())
		return fmt.Errorf("acquireFanOutIfDeclared: partition_request substitution: %w", err)
	}
	subClaims, err := AcquireSubClaims(ctx, args, tx, AcquireSubClaimsInput{
		ParentClaimHandleID: parent.ClaimHandleID,
		ParentClaimScope:    parent.ClaimResult.ClaimScope,
		ProducerName:        parentClaimSpec.ProducerName,
		NodeRunID:           cand.DispatchID,
		HolderNodeID:        cand.NodeID,
		HolderSupervisorID:  args.SupervisorID,
		InstanceID:          instanceID,
		FrameID:             &frameID,
		HeartbeatInterval:   heartbeatInterval,
		PartitionRequest:    partitionRequest,
		// @constraint: sub-claims inherit the parent claim's lifetime. parentClaimSpec.Lifetime
		// is the rimsky-internal plain-string carried on the ClaimSpec (lib/protocols
		// may not import lib/foundation/spec); convert to spec.ClaimLifetime here.
		// AcquireSubClaims defaults an empty value to "subgraph". @concept: claim-lifetime
		Lifetime: spec.ClaimLifetime(parentClaimSpec.Lifetime),
		// @constraint: sub-claims inherit the parent's is_held so the rows survive
		// the leaf's active terminal until the parent's recursive
		// resolution walks them. Without this, non-held sub-claim
		// rows drop at active terminal and the parent's aggregation
		// sees an empty children set, Committing prematurely.
		ParentIsHeld: parent.IsHeld,
		// @constraint: AggregationPolicy is snapshotted onto the parent claim
		// handle so the recursive walker computes a true aggregate
		// Commit/Abandon decision over all children's outcomes
		// (cycle 4 issue C).
		AggregationPolicy: nodeDef.FanOut.ErrorPolicy,
	})
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out sub-claim acquisition failed",
			"node_id", cand.NodeID.String(),
			"producer", parentClaimSpec.ProducerName,
			"error", err.Error())
		return fmt.Errorf("acquireFanOutIfDeclared: %w", err)
	}
	out.SubClaims = subClaims
	return nil
}

// substituteFanOutPartitionRequest resolves a fan-out node's
// partition_request template against the frame's trigger message and
// returns the producer-interpreted bytes to hand to SplitScope.
//
// The directive the operator authors is canonically
// `{{trigger.message.payload.partition_request_override | <default>}}`:
// it pulls the operator-supplied `partition_request_override` off the
// message delivered into this frame. The override is bound through
// ResolveContext.TriggerMessagePayload — the slot resolveTriggerValue
// reads — so the substituted bytes carry the operator's override, not
// the template default.
//
// Trigger message recovery (the load-bearing ordering): message
// delivery marks rimsky_messages.frame_id and invalidates the target
// node BEFORE the resulting node-run is acquired, so by the time
// fan-out acquisition runs the delivered message for this frame is
// present and recoverable by frame_id. We bind it only when EXACTLY
// one delivered message exists for the frame; zero or more than one →
// leave TriggerMessagePayload empty so the directive's `|`-fallback
// (or ErrMissingSource for a strict directive) governs — never a
// silent wrong-partition run. (Conflicting-override coalescing into
// one frame is prevented by the conflict-aware delivery pass.)
//
// Value→bytes conversion: SubstituteValue lifts a whole `{{…}}`
// directive to its JSON value. A string result (a string-shaped
// override, or a literal partition_request with no directives such as
// "all") flows through as its raw bytes — preserving the producer's
// existing interpretation. A non-string result (object/array/number/
// bool) is JSON-encoded, the form a producer that splits on a
// structured override expects.
//
// @concept: fan-out
func substituteFanOutPartitionRequest(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	frameID shared.UUID, out *acquisition, partitionRequest string,
) ([]byte, error) {
	resolveCtx := attributes.ResolveContext{
		Params: instanceParamsRaw(out),
		Claim:  out.HeldClaims,
		// @constraint: defense-in-depth — thread the template's declared
		// message-type set so `{{messages.<type>.<field>}}` references
		// against undeclared types fail with ErrMissingSource even on
		// fan-out partition_request substitution. Mirrors
		// `buildResolveContextForDispatch` in runner_dispatch.go.
		RegistryDeclaredTypes: declaredMessageTypesForTemplate(ctx, args, out.TemplateHash, tx),
	}
	// @constraint: bind both payload bytes AND message-type. The fan-out
	// partition_request path historically reads only
	// `{{trigger.message.payload.X}}`, but the typed-message arm
	// (`{{messages.<type>.<field>}}`) shares the same resolver function
	// and must see the same triggering-message envelope so a fan-out
	// node whose partition_request reads through the typed-message
	// grammar resolves the same way (one substitution engine, two
	// surfaces).
	payload, mtype := triggerMessageForFrame(ctx, args, tx, frameID)
	if len(payload) > 0 {
		resolveCtx.TriggerMessagePayload = payload
	}
	if mtype != "" {
		resolveCtx.TriggerMessageType = mtype
	}
	val, err := attributes.SubstituteValue(partitionRequest, resolveCtx)
	if err != nil {
		return nil, err
	}
	if s, ok := val.(string); ok {
		return []byte(s), nil
	}
	b, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("marshal substituted partition_request: %w", err)
	}
	return b, nil
}

// instanceParamsRaw marshals the acquisition's instance params blob to
// raw JSON for the substitution ResolveContext, mirroring the shaping
// buildLockSpecs performs. Returns nil when there are no params (nil is
// treated as empty by the resolver).
func instanceParamsRaw(out *acquisition) json.RawMessage {
	if out == nil || len(out.InstanceParams) == 0 {
		return nil
	}
	b, err := json.Marshal(out.InstanceParams)
	if err != nil {
		return nil
	}
	return b
}

// triggerMessageForFrame returns the (payload, type) tuple of the
// single delivered message bound to frameID, or (nil, "") when zero or
// more than one delivered message exists. The type is the
// `rimsky_messages.type` discriminator that the substitution engine's
// `messages.<type>.<field>` arm matches against (see
// `code:graph/attribute/substitution.go::resolveMessagesValue`).
//
// Reuses the caller's open acquisition tx via the tx-aware
// ListDeliveredForFrame. The tx-less Messages().List would deadlock
// here: under the SQLite driver's MaxOpenConns=1, a fresh-connection
// read from inside the open tx blocks forever waiting for the only pool
// conn (held by the tx). The delivered row is visible inside the tx
// because message delivery committed it before this acquisition began
// (see the ordering note on substituteFanOutPartitionRequest).
//
// Per @blessed-invariant 20/21 the payload bytes are inert — forwarded
// verbatim into the substitution context, never logged or transformed.
// The type-path discriminator is identifier-shaped and safe to log; the
// payload is not.
func triggerMessageForFrame(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID shared.UUID,
) (json.RawMessage, string) {
	if args.Persist == nil || args.Persist.Messages() == nil {
		return nil, ""
	}
	rows, err := args.Persist.Messages().ListDeliveredForFrame(ctx, tx, frameID)
	if err != nil {
		args.Logger.Warn("trigger-message lookup failed; substitution falls back to ErrMissingSource",
			"frame_id", frameID.String(),
			"error", err.Error())
		return nil, ""
	}
	if len(rows) != 1 {
		return nil, ""
	}
	return rows[0].Payload, rows[0].Type
}

// loadResumeMetadataIfParked reads the per-run resume metadata from
// the persistence layer when the node-run row carries parked metadata
// surviving from a prior park, builds a resumeMetadata struct, and
// resolves any spilled payload through the BlobBackend. The presence
// of `out.Resume` is consumed by buildExecuteRequest to populate
// ExecuteRequest.resume_context.
//
// Best-effort: blob/backend-mismatch failures pass an empty payload to
// the executor and log a warn, rather than failing the acquisition —
// the resume signal itself (session_token, wake_reason) is the
// load-bearing part; the payload is executor-private metadata.
func loadResumeMetadataIfParked(
	ctx context.Context, args RunArgs, tx persistence.Tx, out *acquisition,
	cand persistence.Candidate,
) {
	rm, rerr := args.Queue.LoadResumeMetadataInTx(ctx, tx, cand.DispatchID)
	if rerr != nil || rm == nil {
		return
	}
	payload := rm.PayloadInline
	if rm.PayloadHandle != "" {
		payload = readResumePayloadBlob(ctx, args, rm, cand)
	}
	// @deliberate: resume_reason is read from the persisted wake_reason column,
	// populated by ResumeParkedInTx at wake time. Empty wake_reason
	// (NULL) falls back to deadline_elapsed — covers older rows
	// upgraded in place pre-v1 and any wake path that forgot to set
	// it (none today; the fallback is defensive). The
	// operator-invalidate path that used to set
	// external_invalidate retired with the 2026-06-14
	// message-schema-layer reshape; the parked-resume sweep is the
	// only live wake source.
	wakeReason := WakeDeadlineElapsed
	if rm.WakeReason != "" {
		wakeReason = WakeReason(rm.WakeReason)
	}
	out.Resume = &resumeMetadata{
		Payload:      payload,
		SessionToken: rm.SessionToken,
		Reason:       wakeReason,
	}
	// @deliberate: observe parked duration on resume — measured from when the
	// node-run entered phase='parked' (rm.ParkedAt) to now. Skipped
	// when ParkedAt is zero (legacy rows or callers that haven't
	// backfilled the field).
	if !rm.ParkedAt.IsZero() {
		metricsOf(args).ObserveParkedDurationOnResume(args.Clock.Now().Sub(rm.ParkedAt).Seconds())
	}
}

// readResumePayloadBlob resolves a spilled resume payload through the
// configured BlobBackend. Returns nil on any failure (missing backend,
// backend-name mismatch, fetch error) with a per-case warn — the
// caller treats nil as "no payload to thread through".
func readResumePayloadBlob(
	ctx context.Context, args RunArgs, rm *persistence.ResumeMetadataRow,
	cand persistence.Candidate,
) []byte {
	if args.Blob == nil {
		args.Logger.Warn("tryAcquire: spilled resume payload but no BlobBackend configured; passing empty payload to executor",
			"node_id", cand.NodeID.String(),
			"handle_backend", rm.PayloadHandleBackend)
		return nil
	}
	if args.Blob.Name() != rm.PayloadHandleBackend {
		args.Logger.Warn("tryAcquire: blob backend mismatch on resume; passing empty payload to executor",
			"node_id", cand.NodeID.String(),
			"current_backend", args.Blob.Name(),
			"handle_backend", rm.PayloadHandleBackend)
		return nil
	}
	b, berr := args.Blob.Read(ctx, persistence.Handle(rm.PayloadHandle))
	if berr != nil {
		args.Logger.Warn("tryAcquire: blob fetch for resume payload failed; passing empty payload to executor",
			"node_id", cand.NodeID.String(), "error", berr.Error())
		return nil
	}
	return b
}

// loadScratchIntoAcquisition reads the dispatch row's scratch bytes
// (inline or spilled) and stamps them onto `out.Scratch` so
// buildExecuteRequest populates `ExecuteRequest.scratch`. Mirrors the
// resume-payload loader: best-effort blob materialization — a missing
// backend, backend-name mismatch, or fetch error degrades to empty
// scratch with a logged warn, NOT a failed acquisition.
// STORY-opaque-executor-scratch's load-bearing property is round-trip
// integrity when the read succeeds; a transient backend outage
// degrading to empty is acceptable because the executor sees
// `len(scratch) == 0` and handles it the same as a fresh dispatch.
//
// @concept: executor
func loadScratchIntoAcquisition(
	ctx context.Context, args RunArgs, tx persistence.Tx, out *acquisition,
	cand persistence.Candidate,
) {
	inline, handle, handleBackend, err := args.Queue.LoadScratchInTx(ctx, tx, cand.DispatchID)
	if err != nil {
		args.Logger.Warn("tryAcquire: LoadScratchInTx failed; passing empty scratch to executor",
			"dispatch_id", cand.DispatchID.String(), "error", err.Error())
		return
	}
	if handle == "" {
		// @deliberate: either no scratch at all, or inline scratch — pass through.
		out.Scratch = inline
		return
	}
	// @deliberate: spilled scratch — materialize through the configured BlobBackend.
	if args.Blob == nil {
		args.Logger.Warn("tryAcquire: spilled scratch but no BlobBackend configured; passing empty scratch to executor",
			"node_id", cand.NodeID.String(),
			"handle_backend", handleBackend)
		return
	}
	if args.Blob.Name() != handleBackend {
		args.Logger.Warn("tryAcquire: blob backend mismatch on scratch; passing empty scratch to executor",
			"node_id", cand.NodeID.String(),
			"current_backend", args.Blob.Name(),
			"handle_backend", handleBackend)
		return
	}
	b, berr := args.Blob.Read(ctx, persistence.Handle(handle))
	if berr != nil {
		args.Logger.Warn("tryAcquire: blob fetch for scratch failed; passing empty scratch to executor",
			"node_id", cand.NodeID.String(), "error", berr.Error())
		return
	}
	out.Scratch = b
}
