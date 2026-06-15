// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Shared helper for Producer.Abandon on already-Open'd claims.
//
// @concept: terminal-resolution
//
// Two sites need to call Producer.Abandon on a claim whose Open already
// succeeded:
//
//   1. The unified terminal-decision engine
//      (ResolveClaimHandleTerminal in terminal_decision.go), Abandon
//      branch. Runs inside a caller-provided tx; the engine itself
//      owns the row transition that follows (Promote for the terminal
//      sources, claimant-guarded Delete for the OwnershipBail source —
//      the verify-before-run bail in runner_acquire_postcommit.go::
//      handleOrphanedClaim resolves through the engine with that
//      source).
//
//   2. The pre-dispatch acquire/unavailable carve-out
//      (abandonPartialLocks in runner_lifecycle.go). Runs post-rollback
//      from the operator's `error_types:` chain; the
//      rimsky_claim_handles rows have already been removed by the
//      acquisition tx rollback, so no delete is needed. This is the
//      single path that fires Abandon outside the unified engine.
//
// abandonOpenedClaim centralizes the producer-Abandon call so the
// two sites share a single audited site for any future audit emit
// or telemetry. It does NOT touch the rimsky_claim_handles row —
// see the two callers for the row semantics that apply at each site.
//
// Preserves @blessed-invariant 4 (claimant-guarded release): the
// helper never touches the row; callers do.
//
// Preserves @blessed-invariant 20 (claim content inert): scope and
// address pass through opaque; the helper does not log, format with
// %v, validate, transform, or otherwise act on them.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// abandonOpenedClaim fires Producer.Abandon on a claim whose Open
// already succeeded. claim_id is built from the claim_handle_id so
// the producer can correlate state across verbs.
func abandonOpenedClaim(
	ctx context.Context,
	producer locks.ClaimProducer,
	claimHandleID shared.UUID,
	scope, address []byte,
) error {
	claimID := claimproducer.ClaimID(claimHandleID.String())
	// @constraint: stamp the producer name so a host-agent-proxy fronting the
	// claim-producer protocol can route this Abandon by service name.
	ctx = peer.WithServiceName(ctx, producer.Name())
	return producer.Abandon(ctx, claimID, scope, address)
}
