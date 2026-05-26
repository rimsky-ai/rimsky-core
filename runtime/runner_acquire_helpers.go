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
// @source: runtime/runner_acquire.go::tryAcquire (cycle-7 extraction)

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/protocols/claimproducer"
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
	// Only the root run of a fan-out tree splits. Children re-use the
	// parent's node_id (per `runtime/fanout_dispatch.go::PlanFanOutChildren`)
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
	// Locate the acquiredLocks entry whose Alias matches the
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
	// `parent.Spec` is `any` — narrow to ClaimSpec; named locks
	// can't be fan-out targets (no producer name).
	parentClaimSpec, ok := parent.Spec.(claimproducer.ClaimSpec)
	if !ok {
		args.Logger.Warn("tryAcquire: fan-out alias references non-claim spec; ignored",
			"node_id", cand.NodeID.String(),
			"alias", fanOutClaim)
		return nil
	}
	frameID := cand.FrameID
	// Substitute partition_request with the runtime-resolved trigger
	// payload. Pre-v1: the trigger-message wiring (E14) passes
	// substitution through at dispatch time; until the
	// substitution-aware caller lands, the literal bytes of the
	// canonicalized partition_request flow through verbatim.
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
		PartitionRequest:    []byte(nodeDef.FanOut.PartitionRequest),
		// Sub-claims inherit the parent's is_held so the rows survive
		// the leaf's active terminal until the parent's recursive
		// resolution walks them. Without this, non-held sub-claim
		// rows drop at active terminal and the parent's aggregation
		// sees an empty children set, Committing prematurely.
		ParentIsHeld: parent.IsHeld,
		// AggregationPolicy is snapshotted onto the parent claim
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
	// resume_reason is read from the persisted wake_reason column,
	// populated by ResumeParkedInTx at wake time. Empty wake_reason
	// (NULL) falls back to external_invalidate — covers older rows
	// upgraded in place pre-v1 and any wake path that forgot to set
	// it (none today; the fallback is defensive).
	wakeReason := WakeExternalInvalidate
	if rm.WakeReason != "" {
		wakeReason = WakeReason(rm.WakeReason)
	}
	out.Resume = &resumeMetadata{
		Payload:      payload,
		SessionToken: rm.SessionToken,
		Reason:       wakeReason,
	}
	// Observe parked duration on resume — measured from when the
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
