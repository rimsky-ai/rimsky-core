// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unified terminal-decision engine — Phase 6 of the
// layer-crystallization plan. Both the executor-terminal path
// (releaseClaim's non-held branch in runner_terminal.go) and the
// auto-terminal path (CheckAndFireResolution in auto_terminal.go) end
// in the same three-step sequence:
//
//  1. Determine aggregate outcome (success → Commit; failure → Abandon).
//  2. Fire the producer verb at-least-once (claim_id is the idempotency
//     key per foundation contract §4.4 / spec §7.8 obligation #3).
//  3. Delete the rimsky_claim_handles row claimant-guarded (foundation
//     invariant 4).
//
// This file packages those three steps as ResolveClaimHandleTerminal
// so the two source paths share a single audited implementation.
//
// Foundation invariants preserved:
//   - Inv 4 (claimant-guarded release): the Delete is gated on
//     supervisor_id.
//   - Inv 13 (single auto-terminal): callers serialize via SELECT …
//     FOR UPDATE on the claim_handle row before invoking; this engine
//     does not re-take the lock.
//   - Inv 20 (claim content inert): scope/address pass through opaque;
//     no logging, no transformation.

package runtime

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// TerminalSource discriminates the call site so logging and metrics
// can distinguish active-phase vs held-phase resolutions.
type TerminalSource int

const (
	// ActiveTerminal: the executor's terminal-event handler released a
	// non-held claim (spec §7.6).
	ActiveTerminal TerminalSource = iota

	// HeldTerminal: the auto-terminal mechanism fired at holding-
	// subgraph completion (spec §4.10 invariant 13).
	HeldTerminal
)

// AggregateOutcome is the success/failure binary rimsky carries across
// the wire. Per the 2026-04-30 stores-protocol cleanup, store
// disposition (commit vs release vs delete on the producer's own
// state) is governed by per-store config and not by template metadata.
type AggregateOutcome int

const (
	// AggregateCommit: every contributing terminal succeeded → Commit.
	AggregateCommit AggregateOutcome = iota

	// AggregateAbandon: at least one contributing terminal failed →
	// Abandon.
	AggregateAbandon
)

// TerminalCause discriminates how an Abandon resolution was reached so
// the lineage + events projections can distinguish a natural exhaustion
// (give_up, error policy, holder failure) from an operator- /
// sibling-driven force-cancel. Set on `TerminalDecision`; flows into the
// `claim_terminal` lineage row's `outcome` column + the
// `claim_resolution.abandon` event payload.
type TerminalCause string

const (
	// TerminalCauseNatural — the default. Covers AggregateCommit
	// resolutions and AggregateAbandon resolutions that fired because the
	// holding subgraph itself failed.
	TerminalCauseNatural TerminalCause = "natural"

	// TerminalCauseSiblingCancel — Abandon was forced by the parent's
	// `strict.cancel_siblings: true` walk in `cancelInFlightSiblings`.
	// The sibling is being cancelled because a peer sub-claim already
	// resolved as Abandon.
	TerminalCauseSiblingCancel TerminalCause = "sibling_cancel"

	// TerminalCauseDescendantCancel — Abandon was forced by
	// `cancelDescendantClaims`'s recursive descent: a parent row that is
	// itself resolving as Abandon walks its in-flight descendants and
	// fires force-Abandon on each before deleting itself.
	TerminalCauseDescendantCancel TerminalCause = "descendant_cancel"
)

// TerminalDecision is the input to ResolveClaimHandleTerminal.
type TerminalDecision struct {
	// ClaimHandleID is the rimsky_claim_handles row to resolve.
	ClaimHandleID shared.UUID

	// SupervisorID guards the claimant-side Delete (foundation invariant 4).
	SupervisorID string

	// Source distinguishes the call site for logging / metrics.
	Source TerminalSource

	// Outcome drives the producer verb selection.
	Outcome AggregateOutcome

	// Producer is the ClaimProducer client to dial. Either supplied by
	// the caller (active path: lk.Producer from the AcquiredLock) or
	// looked up by the caller from the registry (held path: by
	// claim_handle.producer_name).
	Producer locks.ClaimProducer

	// Scope and Address are the producer-supplied bytes from the
	// claim_handle row. Inert in rimsky per invariant 20.
	Scope   []byte
	Address []byte

	// Lifetime is the row's `lifetime` column ("subgraph" | "durable").
	// On `AggregateCommit` with `Lifetime == "durable"`, the engine
	// promotes the row by setting `held_durable=TRUE` and skips the
	// claimant-guarded delete; the row then survives auto-terminal and
	// is released by the instance-termination cleanup path
	// (`ReleaseHeldDurableClaims`). Empty defaults to "subgraph". Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Held-durable claim lifecycle.
	Lifetime string

	// CandidateHandle, when non-empty, signals that the row was
	// acquired against a `DataProcessing`-capable producer via
	// `BeginCandidate`. The engine routes the per-candidate disposition
	// (`CommitCandidate` / `AbandonCandidate`) via `args.DataProcessors`
	// BEFORE the standard `ClaimProducer.Commit` / `Abandon` so the
	// producer can stage the per-version metadata that the standard
	// verb then finalizes. Empty → no candidate dispatch fires (standard
	// non-data-processing path). Inert in rimsky per
	// @blessed-invariant 20-class.
	CandidateHandle []byte

	// ProducerName resolves the DataProcessing client for the
	// candidate dispatch above. Required when CandidateHandle is set;
	// ignored otherwise.
	ProducerName string

	// LineageHint carries the per-claim observability metadata the
	// engine needs to emit a `claim_terminal` lineage record at the
	// producer-verb fire. Optional — when unset (zero values), the
	// lineage write + claim_resolution.* event are skipped (used by
	// code paths that have already emitted their own lineage or have
	// nothing observable to record). Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Content lineage + the 2026-05-16 forensics extension.
	LineageHint ClaimLineageHint

	// ParentClaimHandleID, when non-nil, identifies the row's parent
	// claim handle in a sub-claim chain (fan-out / data-processing).
	// After the standard Delete path completes, the engine recurses
	// into `resolveParentClaimChain` so the parent's aggregation walk
	// fires regardless of whether the just-resolved sub-claim was
	// released via the held or the non-held branch. Without this
	// recurse, a fan-out child whose leaf alias is NON-held would
	// drop the sub-claim row via `releaseClaim`'s non-held branch and
	// the parent's `ListChildClaimHandles` walk would never see it,
	// stranding the parent's auto-terminal. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution + §Fan-out template DSL.
	ParentClaimHandleID *shared.UUID

	// Cause discriminates Abandon provenance for forensics. Default
	// (`""` / `TerminalCauseNatural`) is the natural-exhaustion path;
	// the cancel walkers (`cancelInFlightSiblings` /
	// `cancelDescendantClaims`) set `TerminalCauseSiblingCancel` /
	// `TerminalCauseDescendantCancel` on the recursive
	// `ResolveClaimHandleTerminal` calls they fire. Ignored on
	// `AggregateCommit` (those always emit `outcome: committed`).
	Cause TerminalCause
}

// ClaimLineageHint carries the per-claim metadata the terminal-decision
// engine needs to emit a lineage record alongside the producer verb.
type ClaimLineageHint struct {
	InstanceID   shared.UUID
	FrameID      shared.UUID
	RunID        shared.UUID
	NodeID       shared.UUID
	ProducerName string
	// VersionID, when non-empty, is persisted on the lineage row's
	// payload. Populated on durable-claim Commit by the producer's
	// returned version identifier (col:rimsky_claim_handles.version_id).
	VersionID string
}

// ResolveClaimHandleTerminal fires the producer verb for the given
// outcome and deletes the claim_handle row claimant-guarded. Runs
// inside the caller's tx so the verb + the row delete commit
// atomically with whatever else the caller is mutating.
//
// Per spec §7.3 / foundation contract §4.4 the producer's verb runs
// in its own (producer-side) transaction; rimsky's bookkeeping tx
// commits the claim_handle DELETE independently. At-least-once
// delivery + claim_id idempotency on the producer side handles
// transient failures.
//
// The caller MUST have already serialized concurrent terminations on
// the same claim_handle (e.g. via SELECT … FOR UPDATE for held-phase
// resolutions, or via the per-row tx natural ordering for active-
// phase resolutions). This function does not re-take any lock.
func ResolveClaimHandleTerminal(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	td TerminalDecision,
) error {
	if td.Producer == nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: producer is nil for claim_handle %s", td.ClaimHandleID)
	}
	// DataProcessing-candidate dispatch (sub-claim path). Fires BEFORE
	// the standard ClaimProducer verb so the producer can stage the
	// per-candidate version metadata that Commit then finalizes. Empty
	// CandidateHandle / nil registry → no-op (standard non-DataProcessing
	// path). Spec §Protocol surfaces / DataProcessing.
	var versionID string
	if len(td.CandidateHandle) > 0 && td.ProducerName != "" && args.DataProcessors != nil {
		if dp, ok := args.DataProcessors.Get(td.ProducerName); ok {
			switch td.Outcome {
			case AggregateCommit:
				cOut, cErr := dp.CommitCandidate(ctx, CommitCandidateInput{
					ProducerName:    td.ProducerName,
					ClaimHandleID:   td.ClaimHandleID.String(),
					CandidateHandle: td.CandidateHandle,
				})
				if cErr != nil {
					return fmt.Errorf("ResolveClaimHandleTerminal: CommitCandidate(%s): %w",
						td.ProducerName, cErr)
				}
				versionID = cOut.VersionID
				if versionID != "" {
					if err := args.ClaimHandles.SetVersionID(ctx, td.ClaimHandleID, td.SupervisorID, versionID, tx); err != nil {
						return fmt.Errorf("ResolveClaimHandleTerminal: SetVersionID: %w", err)
					}
				}
			case AggregateAbandon:
				if err := dp.AbandonCandidate(ctx, AbandonCandidateInput{
					ProducerName:    td.ProducerName,
					ClaimHandleID:   td.ClaimHandleID.String(),
					CandidateHandle: td.CandidateHandle,
				}); err != nil {
					return fmt.Errorf("ResolveClaimHandleTerminal: AbandonCandidate(%s): %w",
						td.ProducerName, err)
				}
			}
		}
	}
	claimID := locks.ClaimID(td.ClaimHandleID.String())
	var verbErr error
	switch td.Outcome {
	case AggregateCommit:
		verbErr = td.Producer.Commit(ctx, claimID, td.Scope, td.Address)
	case AggregateAbandon:
		verbErr = abandonOpenedClaim(ctx, td.Producer, td.ClaimHandleID, td.Scope, td.Address)
	default:
		return fmt.Errorf("ResolveClaimHandleTerminal: unknown outcome %v", td.Outcome)
	}
	if verbErr != nil {
		return fmt.Errorf("ResolveClaimHandleTerminal: producer verb (%s, source=%d): %w",
			outcomeVerbName(td.Outcome), td.Source, verbErr)
	}
	// Emit a `claim_terminal` lineage row + a `claim_resolution.*` event
	// after the producer verb fires. Every terminal — Commit, natural
	// Abandon, force-cancelled Abandon — lands a row so post-mortem
	// queries cover the full claim lifecycle. Per the 2026-05-16
	// forensics extension this replaces the prior Commit-only
	// `claim_commit` emit. Best-effort: both writes are observability
	// metadata, not control-plane state — failures are logged but do not
	// roll back the surrounding terminal-decision tx.
	//
	// @blessed-invariant 20 (claim content inert): payloads carry only
	// the scope_data hash + rimsky-side identifiers; the raw scope /
	// address / candidate / version bytes never appear in the event log
	// or lineage projection.
	emitTerminalForensics(ctx, args, tx, td, versionID)
	// Held-durable promotion: on AggregateCommit with `lifetime: durable`,
	// flip held_durable=TRUE and KEEP the row. The row then survives
	// auto-terminal so the asset endpoints (`GET /instances/{id}/assets`)
	// can list it and the instance-termination cleanup
	// (`ReleaseHeldDurableClaims`) can issue the producer Release. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Held-durable claim lifecycle.
	//
	// On AggregateAbandon (or any other lifetime) the row drops via the
	// claimant-guarded Delete below — Abandon means "this version failed;
	// the producer should discard its candidate" and there is nothing to
	// promote to durable.
	if td.Outcome == AggregateCommit && td.Lifetime == "durable" {
		if err := args.ClaimHandles.SetHeldDurable(ctx, td.ClaimHandleID, td.SupervisorID, true, tx); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: SetHeldDurable: %w", err)
		}
	} else {
		// Recursive descendant cancellation. Spec §435 requires that on
		// AggregateAbandon, each in-flight descendant of the row being
		// resolved is force-Abandoned via the same `ResolveClaimHandleTerminal`
		// recursion. This MUST fire BEFORE the row's own Delete: the FK
		// `ON DELETE SET NULL` on `parent_claim_handle_id` would otherwise
		// orphan the descendants — they'd survive in-flight without their
		// parent's auto-terminal ever firing their `Producer.Abandon`, and
		// their running holders would never transition to
		// `failed{error_class: "sibling_failed"}`. Walks recursively
		// through the descendant tree so a sibling that is itself a fan-
		// out parent (fan-out of fan-out) has its grandchildren cancelled
		// too. Same filters as `cancelInFlightSiblings` apply: skip held-
		// durable rows (durable-Commit contract), skip mismatched-
		// supervisor rows (`invariant:4` claimant-guard). Spec
		// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
		// §435 — `strict.cancel_siblings: true` recursive descent.
		if td.Outcome == AggregateAbandon {
			if err := cancelDescendantClaims(ctx, args, tx, td.ClaimHandleID); err != nil {
				return fmt.Errorf("ResolveClaimHandleTerminal: cancelDescendantClaims: %w", err)
			}
		}
		if err := args.ClaimHandles.Delete(ctx, td.ClaimHandleID, td.SupervisorID, tx); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: Delete: %w", err)
		}
	}
	// Recursive claim-tree resolution for sub-claims. When this row is
	// a sub-claim (fan-out leaf / data-processing candidate), the
	// parent's `ListChildClaimHandles` walk needs to see the child's
	// outcome regardless of whether the just-resolved child was
	// released via the non-held Delete branch above OR promoted to
	// held-durable via the SetHeldDurable branch. The held branch in
	// `runtime/auto_terminal.go::CheckAndFireResolution` already
	// invokes `resolveParentClaimChain` after a held-terminal Delete;
	// mirroring it here keeps the recursion firing for both branches.
	// In particular, a durable-Commit child MUST bump the parent's
	// committed_children_count and trigger the recursive walker so
	// best_effort / first aggregation policies (which compute
	// `committed > 0 → Commit; else Abandon`) see the child's success
	// and a fan-out parent whose last unresolved child is durable-
	// Commit can still resolve. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution + §Fan-out template DSL +
	// §State aggregation rules.
	//
	// Before recursing, bump the parent's per-outcome counter so the
	// recursive walker can compute a true aggregate Commit/Abandon
	// decision across ALL children's outcomes — not just the
	// seedOutcome of the just-resolved child (cycle 4 issue C).
	// Claimant-guarded on the same supervisor that holds the parent.
	if td.ParentClaimHandleID != nil {
		outcomeKey := "commit"
		if td.Outcome == AggregateAbandon {
			outcomeKey = "abandon"
		}
		if err := args.ClaimHandles.BumpChildOutcomeCount(ctx, *td.ParentClaimHandleID, td.SupervisorID, outcomeKey, 1, tx); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: BumpChildOutcomeCount: %w", err)
		}
		// Proactive sibling cancellation under `strict.cancel_siblings:
		// true`. When this child resolved to Abandon and the parent's
		// snapshotted aggregation policy declares strict + cancel_siblings,
		// walk the parent's other still-in-flight sub-claims and force-
		// Abandon each via a recursive `ResolveClaimHandleTerminal` call.
		// The recursion runs the standard counter-bump + parent-walk path
		// so the parent's aggregate verdict ends up consistent regardless
		// of how many siblings the cancellation reached.
		//
		// Recursion is bounded by claim-tree depth: each force-Abandoned
		// sibling that itself has sub-claim children will cascade-cancel
		// its descendants via `cancelDescendantClaims`, which runs
		// inside the recursive `ResolveClaimHandleTerminal` frame
		// BEFORE the sibling's own Delete fires (so the FK
		// `parent_claim_handle_id ON DELETE SET NULL` doesn't orphan
		// the grandchildren). That helper applies the same filters
		// (skip held-durable, skip mismatched-supervisor) and is itself
		// recursive — handles arbitrary tree depth.
		//
		// Spec
		// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
		// §Error policy / `strict.cancel_siblings: true`.
		if td.Outcome == AggregateAbandon {
			if err := cancelInFlightSiblings(ctx, args, tx, *td.ParentClaimHandleID, td.ClaimHandleID); err != nil {
				return fmt.Errorf("ResolveClaimHandleTerminal: cancelInFlightSiblings: %w", err)
			}
		}
		if err := resolveParentClaimChain(ctx, args, tx, *td.ParentClaimHandleID, td.Outcome); err != nil {
			return fmt.Errorf("ResolveClaimHandleTerminal: recurse parent: %w", err)
		}
	}
	return nil
}

// cancelInFlightSiblings implements the `strict.cancel_siblings: true`
// proactive cancellation walk. Reads the parent's snapshotted
// aggregation policy; if it declares `strict` + `cancel_siblings: true`,
// walks the parent's other sub-claim children and force-Abandons each
// in-flight sibling via a recursive `ResolveClaimHandleTerminal` call.
//
// Filters applied to each sibling row:
//
//   - triggering child (`triggerID`) is skipped — it's already resolving.
//   - held-durable siblings (`held_durable = TRUE`) are skipped: a
//     durable child that already promoted to Committed must not be
//     Abandoned, that would violate the durable-Commit contract.
//   - mismatched-supervisor siblings are skipped: a force-Abandon on
//     someone else's claim would violate `invariant:4` (claimant-guarded
//     release).
//
// The function is a no-op when:
//   - the parent's row is already gone (`Get` returns nil),
//   - the policy is missing, malformed, or not `strict` + `cancel_siblings`,
//   - `ListChildClaimHandles` returns no remaining siblings.
//
// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelInFlightSiblings(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentID shared.UUID, triggerID shared.UUID,
) error {
	parent, err := args.ClaimHandles.Get(ctx, parentID, tx)
	if err != nil {
		return fmt.Errorf("cancelInFlightSiblings: Get parent: %w", err)
	}
	if parent == nil {
		// Parent already resolved (and deleted). Nothing to cancel.
		return nil
	}
	if parent.HeldDurable {
		// Held-durable parent already resolved (Commit promoted, row
		// preserved). Other auto-terminal paths (`CheckAndFireResolution`,
		// `resolveParentClaimChain`) both guard on `HeldDurable` and
		// return nil; mirror the symmetry here so cancel_siblings doesn't
		// retroactively force-Abandon children whose parent already
		// committed durably.
		return nil
	}
	policy, err := persistence.UnmarshalAggregationPolicy(parent.AggregationPolicy)
	if err != nil {
		// Malformed `aggregation_policy` JSONB. Surface the misconfiguration
		// via a Warn line citing the parent's id so the operator can repair
		// the row, then return nil so the surrounding terminal-decision tx
		// still commits — the parent's `aggregateParentOutcome` walker
		// applies the safe default at the post-resolution aggregator and
		// the runtime stays consistent. Without the log line the operator
		// would never learn that the policy is unparseable.
		if args.Logger != nil {
			args.Logger.Warn("cancelInFlightSiblings: malformed aggregation_policy on parent claim_handle; treating as no cancel_siblings",
				"parent_claim_handle_id", parentID.String(),
				"error", err.Error())
		}
		return nil
	}
	if policy.Kind != spec.AggregationKindStrict || !policy.CancelSiblings {
		return nil
	}
	siblings, err := args.ClaimHandles.ListChildClaimHandles(ctx, parentID, tx)
	if err != nil {
		return fmt.Errorf("cancelInFlightSiblings: ListChildClaimHandles: %w", err)
	}
	for _, sib := range siblings {
		if sib.ID == triggerID {
			// Skip the just-resolving child itself.
			continue
		}
		if sib.HeldDurable {
			// Durable siblings that already promoted to Committed must
			// stay. Abandoning a durable-Commit would violate the
			// contract that durable claims persist past auto-terminal
			// until explicit release.
			continue
		}
		if sib.HolderSupervisorID != args.SupervisorID {
			// Claimant-guard: we cannot force-resolve someone else's
			// claim. The original supervisor's terminal path will
			// handle it through its own resolution.
			continue
		}
		// LockForUpdate the sibling row before the recursive force-Abandon.
		// `ResolveClaimHandleTerminal`'s documented locking precondition
		// (the contract on this function's signature) requires callers to
		// serialize concurrent terminations via `SELECT … FOR UPDATE` for
		// held-phase resolutions. Without this lock, a parallel worker on
		// the same supervisor could be terminating the sibling natively
		// (Commit/Abandon via the executor path) while our cancel walker
		// fires a force-Abandon for the same `claim_id` — the producer
		// would see two distinct verbs (Commit and Abandon) for the same
		// claim and claim_id idempotency cannot reconcile them. The lock
		// is held for the duration of the recursive `ResolveClaimHandleTerminal`
		// call below; concurrent terminators block on the row until our
		// recursive Delete commits the tx.
		current, err := args.ClaimHandles.LockForUpdate(ctx, sib.ID, tx)
		if err != nil {
			return fmt.Errorf("cancelInFlightSiblings: LockForUpdate sibling %s: %w",
				sib.ID, err)
		}
		if current == nil || current.HeldDurable {
			// Re-check the locked row. The recursive walker (via the
			// sibling's own descendant-cancellation walk) may have
			// already deleted later siblings in the original
			// `ListChildClaimHandles` snapshot; durable promotion on
			// the same row could also have raced ahead. Skip so the
			// producer doesn't see a duplicate Abandon for the same
			// claim_id.
			continue
		}
		producerName := ""
		if sib.ProducerName != nil {
			producerName = *sib.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return fmt.Errorf("cancelInFlightSiblings: unknown producer %q for sibling %s",
				producerName, sib.ID)
		}
		// Build the lineage hint for the sibling's force-Abandon. The
		// resolution emits a `claim_terminal` row with
		// `outcome: force_cancelled` (the Cause field below promotes it
		// from natural Abandon) and a matching `claim_resolution.abandon`
		// event. The hint shape matches the regular resolution.
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    sib.VersionID,
			NodeID:       sib.HolderNodeID,
		}
		if sib.FrameID != nil {
			hint.FrameID = *sib.FrameID
		}
		if sib.NodeRunID != nil {
			hint.RunID = *sib.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, sib.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		// Recurse: the sibling's own children (if any) cascade-cancel
		// through the same path inside this recursive call. Forwarding
		// `ParentClaimHandleID` keeps the parent counter bumping +
		// `resolveParentClaimChain` walking under the sibling's own
		// resolution. `Cause` propagates `sibling_cancel` to the lineage
		// + events projections so post-mortem queries can distinguish a
		// sibling-driven force-Abandon from a natural exhaustion.
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       sib.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             AggregateAbandon,
			Producer:            producer,
			Scope:               []byte(sib.ScopeData),
			Address:             []byte(sib.Address),
			Lifetime:            sib.Lifetime,
			CandidateHandle:     sib.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: sib.ParentClaimHandleID,
			Cause:               TerminalCauseSiblingCancel,
		}); err != nil {
			return fmt.Errorf("cancelInFlightSiblings: force-Abandon sibling %s: %w",
				sib.ID, err)
		}
	}
	return nil
}

// cancelDescendantClaims implements the spec §435 recursive-descent
// requirement for `strict.cancel_siblings: true`. When a row is being
// resolved as `AggregateAbandon` AND that row has in-flight descendants
// (i.e. it is itself a fan-out parent — fan-out of fan-out), each
// descendant must receive its own `Producer.Abandon` and its
// claim_handle row must be Deleted BEFORE the parent's own Delete fires.
//
// Why-before-Delete: `col:rimsky_claim_handles.parent_claim_handle_id`
// has `ON DELETE SET NULL`. Deleting the parent row first would orphan
// the descendants (parent_claim_handle_id becomes NULL) — they'd
// survive in-flight without their parent's auto-terminal ever firing
// their `Producer.Abandon`, and their running holders would never
// transition to `failed{error_class: "sibling_failed"}`. Cancelling the
// descendants first ensures the FK chain stays intact through the
// recursive walk.
//
// Filters applied to each descendant row:
//
//   - held-durable rows (`held_durable = TRUE`) are skipped: a durable
//     child that already promoted to Committed must not be Abandoned —
//     that would violate the durable-Commit contract (`@blessed-
//     invariant`-class symmetry with `cancelInFlightSiblings`).
//   - mismatched-supervisor rows are skipped: a force-Abandon on someone
//     else's claim would violate `invariant:4` (claimant-guarded
//     release).
//
// Each remaining descendant is force-Abandoned via a recursive
// `ResolveClaimHandleTerminal` call. That recursion runs THIS helper
// on its own descendants, so the walk handles arbitrary claim-tree
// depth (bounded by the tree itself).
//
// Re-check semantics: when the row has been deleted between the
// `ListChildClaimHandles` snapshot and the `LockForUpdate` (e.g.
// because the recursive walker has already reached it via another
// path), `LockForUpdate` returns nil and the row is skipped.
//
// @concept: claim-tree
// @concept: fan-out
// @concept: cancel-siblings
func cancelDescendantClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	rowID shared.UUID,
) error {
	descendants, err := args.ClaimHandles.ListChildClaimHandles(ctx, rowID, tx)
	if err != nil {
		return fmt.Errorf("cancelDescendantClaims: ListChildClaimHandles: %w", err)
	}
	for _, d := range descendants {
		if d.HeldDurable {
			continue
		}
		if d.HolderSupervisorID != args.SupervisorID {
			continue
		}
		// LockForUpdate the descendant row before the recursive force-
		// Abandon. Same locking precondition as `cancelInFlightSiblings`:
		// the contract on `ResolveClaimHandleTerminal` requires the
		// caller to serialize concurrent terminations via
		// `SELECT … FOR UPDATE` for held-phase resolutions, so we hold
		// the row lock for the duration of the recursive call below.
		current, err := args.ClaimHandles.LockForUpdate(ctx, d.ID, tx)
		if err != nil {
			return fmt.Errorf("cancelDescendantClaims: LockForUpdate descendant %s: %w",
				d.ID, err)
		}
		if current == nil || current.HeldDurable {
			continue
		}
		producerName := ""
		if d.ProducerName != nil {
			producerName = *d.ProducerName
		}
		producer, ok := args.StoreRegistry.Get(producerName)
		if !ok {
			return fmt.Errorf("cancelDescendantClaims: unknown producer %q for descendant %s",
				producerName, d.ID)
		}
		hint := ClaimLineageHint{
			ProducerName: producerName,
			VersionID:    d.VersionID,
			NodeID:       d.HolderNodeID,
		}
		if d.FrameID != nil {
			hint.FrameID = *d.FrameID
		}
		if d.NodeRunID != nil {
			hint.RunID = *d.NodeRunID
		}
		if acquirer, aErr := args.Persist.Nodes().Get(ctx, d.HolderNodeID, tx); aErr == nil && acquirer != nil {
			hint.InstanceID = acquirer.InstanceID
		}
		// Recurse. The descendant's own descendants are walked inside
		// `ResolveClaimHandleTerminal`'s pre-Delete cancellation step
		// (depth-first). `ParentClaimHandleID` is intentionally `nil`
		// here: the descendant's parent is the row being resolved by
		// the OUTER `ResolveClaimHandleTerminal` frame above us, which
		// will Delete that row after this descendant walk returns.
		// Forwarding `d.ParentClaimHandleID` would re-enter the parent's
		// counter-bump + `resolveParentClaimChain` walk on a row that
		// is mid-resolution, risking a re-entrant Delete / duplicate
		// `Producer.Abandon` on the parent. Skipping is safe because
		// the parent's own resolution drives its grandparent counter
		// after this descendant cancellation completes.
		if err := ResolveClaimHandleTerminal(ctx, args, tx, TerminalDecision{
			ClaimHandleID:       d.ID,
			SupervisorID:        args.SupervisorID,
			Source:              HeldTerminal,
			Outcome:             AggregateAbandon,
			Producer:            producer,
			Scope:               []byte(d.ScopeData),
			Address:             []byte(d.Address),
			Lifetime:            d.Lifetime,
			CandidateHandle:     d.ProducerCandidateHandle,
			ProducerName:        producerName,
			LineageHint:         hint,
			ParentClaimHandleID: nil,
			Cause:               TerminalCauseDescendantCancel,
		}); err != nil {
			return fmt.Errorf("cancelDescendantClaims: force-Abandon descendant %s: %w",
				d.ID, err)
		}
	}
	return nil
}

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
// invariant 21` (messages inert): payloads carry only the scope_data
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
	rec := ClaimTerminalRecord{
		ClaimHandleID:       td.ClaimHandleID,
		RunID:               td.LineageHint.RunID,
		NodeID:              td.LineageHint.NodeID,
		FrameID:             td.LineageHint.FrameID,
		ParentClaimHandleID: td.ParentClaimHandleID,
		ProducerName:        td.LineageHint.ProducerName,
		ScopeDataHash:       HashBytes(td.Scope),
		VersionID:           preferVersionID(versionID, td.LineageHint.VersionID),
		Outcome:             outcome,
	}
	if td.Outcome == AggregateAbandon && td.Cause != "" && td.Cause != TerminalCauseNatural {
		rec.Cause = string(td.Cause)
	}
	if lt := args.Persist.Lineage(); lt != nil {
		if err := WriteClaimTerminalLineage(ctx, tx, lt,
			td.LineageHint.InstanceID, td.LineageHint.FrameID,
			args.Clock.Now(), rec); err != nil && args.Logger != nil {
			args.Logger.Warn("ResolveClaimHandleTerminal: lineage write failed",
				"claim_handle_id", td.ClaimHandleID.String(),
				"outcome", outcome,
				"error", err.Error())
		}
	}
	// Event payload mirrors the lineage shape but excludes node_id (the
	// event row already carries it as a column). The kind discriminates
	// commit vs abandon; the cause field carries the abandon-flavor.
	kind := "claim_resolution.commit"
	payload := map[string]any{
		"claim_handle_id": td.ClaimHandleID.String(),
		"run_id":          td.LineageHint.RunID.String(),
		"frame_id":        td.LineageHint.FrameID.String(),
		"producer_name":   td.LineageHint.ProducerName,
		"scope_data_hash": rec.ScopeDataHash,
		"version_id":      rec.VersionID,
	}
	if td.ParentClaimHandleID != nil {
		payload["parent_claim_handle_id"] = td.ParentClaimHandleID.String()
	}
	if td.Outcome == AggregateAbandon {
		kind = "claim_resolution.abandon"
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
			"kind", kind,
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
