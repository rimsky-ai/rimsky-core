package claimstorepg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/store"
)

// TerminalOutcome discriminates the two terminal classes the §5.6.4
// resolution algorithm distinguishes: a node that committed (uses
// `on_commit`) vs. a node that gave up (uses `on_give_up`).
type TerminalOutcome string

const (
	// TerminalCommit means the holder node committed; its on_commit
	// action drives the resolution.
	TerminalCommit TerminalOutcome = "commit"
	// TerminalGiveUp means the holder node gave up; its on_give_up
	// action drives the resolution.
	TerminalGiveUp TerminalOutcome = "give_up"
)

// ResolveOnTerminal implements the spec §5.6.4 reference-counted claim
// resolution algorithm. It runs inside the caller's outer release
// transaction (via store.TxFromContext) so its mutations to
// rimsky_claim_holders + the items-table row commit or roll back together
// with the lock-holder delete.
//
// Algorithm (mapping the spec §5.6.4 pseudocode lines 153–194):
//
//   - SELECT the active holder row for (claim_id, holder_node_id) FOR
//     UPDATE. If absent, no-op.
//   - Pick `action` from on_commit (terminal=commit) or on_give_up
//     (terminal=give_up). Mark the row state='completed', actual_action=action.
//   - If action='delete': delegate to resolveDelete (first-delete-wins).
//   - Otherwise: delegate to resolveRelease (last-released-wins).
//
// Two refinements vs. the bare spec pseudocode, both driven by ring-buffer
// claim_id reuse (the items-table item_id is the claim_id, and a ring
// buffer recycles items via release_to_back):
//
//  1. The "current row" SELECT filters `state='active'`. The unique
//     index on (claim_id, holder_node_id) is partial on state='active'
//     (§9.9.3). Historical 'completed' rows from prior cycles coexist
//     with the live row, so an unfiltered SELECT would either pick a
//     stale completed row or error with "more than one row".
//
//  2. The sibling-count predicates in resolveDelete / resolveRelease
//     scope by frame_id (`frame_id IS NOT DISTINCT FROM R.frame_id`).
//     v1 frame_id is observability-only at the schema level (§10.4
//     frame-resolution spec — "v1 logic does not key on frame_id" —
//     which constrains index keying, not in-tx correctness scoping).
//     Without frame scoping, a prior cycle's completed 'delete' row
//     would falsely match the "did anyone delete?" predicate for a
//     fresh cycle on a reused claim_id and suppress the legitimate
//     release, leaving the items-table row stuck in_progress.
//
// Returns nil on success (including the no-op branch). Returns a non-nil
// error on SQL failure; the caller rolls back the outer tx.
func (s *Store) ResolveOnTerminal(
	ctx context.Context,
	claimID string,
	holderNodeID string,
	terminalOutcome TerminalOutcome,
) error {
	tx, ok := store.TxFromContext(ctx)
	if !ok {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal requires an open pgx.Tx via store.WithTx", s.name)
	}

	// Step 1: SELECT FOR UPDATE the active holder row for this
	// (claim_id, holder_node_id). If absent we no-op. The pseudocode's
	// "CONTINUE" maps to "return nil" here because this method handles a
	// single (claim_id, holder_node_id) pair per call — the supervisor's
	// outer logic loops over multiple claims as needed.
	//
	// `state = 'active'` is required in the predicate: ring-buffer claim
	// stores reuse `claim_id` (= `item_id`) across cycles, so historical
	// 'completed' holder rows for the same (claim_id, holder_node_id) can
	// coexist with the current cycle's 'active' row. The unique index is
	// partial-on-active (§9.9.3) so at most one 'active' row exists at
	// a time; selecting unconditionally would error with "more than one
	// row" when prior cycles' completed rows are still in the table.
	//
	// We also read `frame_id` so the §5.6.4 sibling-count predicates in
	// resolveDelete / resolveRelease can scope to the current cycle's
	// holders — without that scoping, prior cycles' completed rows leak
	// into "did anyone delete?" checks and could falsely suppress the
	// release for a fresh cycle on a reused claim_id.
	var (
		holderRowID string
		onCommit    string
		onGiveUp    string
		frameID     *string
	)
	row := tx.QueryRow(ctx,
		`SELECT id, on_commit, on_give_up, frame_id::text
		   FROM rimsky_claim_holders
		  WHERE claim_id = $1 AND holder_node_id = $2 AND state = 'active'
		  FOR UPDATE`,
		claimID, holderNodeID,
	)
	if err := row.Scan(&holderRowID, &onCommit, &onGiveUp, &frameID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not a holder, or already collapsed by an earlier sweep — no-op.
		}
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: select holder row: %w", s.name, err)
	}

	// Step 2: pick the action from the terminal outcome.
	var action string
	switch terminalOutcome {
	case TerminalCommit:
		action = onCommit
	case TerminalGiveUp:
		action = onGiveUp
	default:
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: unknown terminal outcome %q", s.name, terminalOutcome)
	}
	if !validReleaseAction(action) {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: holder row %s carries invalid action %q", s.name, holderRowID, action)
	}

	// Step 3: mark this row completed with the chosen action recorded.
	if _, err := tx.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = 'completed', completed_at = now(), actual_action = $1
		  WHERE id = $2`,
		action, holderRowID,
	); err != nil {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: update holder row: %w", s.name, err)
	}

	// Step 4: branch on action. Sibling counts in the resolveDelete /
	// resolveRelease branches scope by frame_id so prior cycles on a
	// reused claim_id (ring buffer) don't leak into the predicate.
	if action == "delete" {
		return s.resolveDelete(ctx, tx, claimID, holderRowID, frameID)
	}
	return s.resolveRelease(ctx, tx, claimID, action, frameID)
}

// resolveDelete implements the first-delete-wins branch of §5.6.4. If a
// sibling in the SAME frame already wrote actual_action='delete', this
// row is bookkeeping only. Otherwise we DELETE the items-table row and
// collapse all same-frame sibling 'active' rows by setting their
// actual_action='delete_won'.
//
// Sibling scoping by frame_id is required for ring-buffer claim stores
// where claim_id is reused across cycles: a prior cycle's completed
// 'delete' row would otherwise falsely match here and skip the
// items-table delete on the new cycle.
func (s *Store) resolveDelete(ctx context.Context, tx pgx.Tx, claimID, holderRowID string, frameID *string) error {
	// Check for a prior winning delete by some sibling within the same frame.
	var dummy int
	priorErr := tx.QueryRow(ctx,
		`SELECT 1 FROM rimsky_claim_holders
		  WHERE claim_id = $1 AND state = 'completed'
		    AND actual_action = 'delete' AND id <> $2
		    AND frame_id IS NOT DISTINCT FROM $3::uuid
		  LIMIT 1`,
		claimID, holderRowID, frameID,
	).Scan(&dummy)
	switch {
	case errors.Is(priorErr, pgx.ErrNoRows):
		// We're the first; perform the items-table delete and collapse siblings.
	case priorErr != nil:
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: detect prior delete: %w", s.name, priorErr)
	default:
		// A sibling already deleted; our row is bookkeeping. Nothing else to do.
		return nil
	}

	// Items-table row delete.
	delQ := fmt.Sprintf(`DELETE FROM %s WHERE item_id = $1`, s.itemsTable)
	if _, err := tx.Exec(ctx, delQ, claimID); err != nil {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: delete items-table row: %w", s.name, err)
	}

	// Collapse any still-active siblings by marking them 'delete_won'.
	// Scoped to the same frame_id so we don't accidentally reach across
	// cycles (a future cycle's 'active' row on the same claim_id would
	// belong to a different frame).
	if _, err := tx.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = 'completed', completed_at = now(), actual_action = 'delete_won'
		  WHERE claim_id = $1 AND state = 'active' AND id <> $2
		    AND frame_id IS NOT DISTINCT FROM $3::uuid`,
		claimID, holderRowID, frameID,
	); err != nil {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: collapse siblings: %w", s.name, err)
	}
	return nil
}

// resolveRelease implements the last-released-wins branch of §5.6.4. We
// fire ReleaseClaimItem only when ALL same-frame siblings are completed
// AND none of them deleted (the 'delete_won' sentinel counts as
// "deleted" for this purpose: it marks a sibling collapsed by an earlier
// winning delete).
//
// Sibling scoping by frame_id is required for ring-buffer claim stores
// where claim_id is reused across cycles: a prior cycle's completed
// 'delete' / 'delete_won' would otherwise falsely match the
// "did anyone delete?" predicate and suppress the legitimate release for
// the current cycle, leaving the items-table row stuck in_progress.
func (s *Store) resolveRelease(ctx context.Context, tx pgx.Tx, claimID, action string, frameID *string) error {
	var activeCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_holders
		  WHERE claim_id = $1 AND state = 'active'
		    AND frame_id IS NOT DISTINCT FROM $2::uuid`,
		claimID, frameID,
	).Scan(&activeCount); err != nil {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: count active siblings: %w", s.name, err)
	}
	var deleteCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_holders
		  WHERE claim_id = $1 AND state = 'completed'
		    AND actual_action IN ('delete', 'delete_won')
		    AND frame_id IS NOT DISTINCT FROM $2::uuid`,
		claimID, frameID,
	).Scan(&deleteCount); err != nil {
		return fmt.Errorf("claim_store %q: ResolveOnTerminal: count delete siblings: %w", s.name, err)
	}
	if activeCount > 0 || deleteCount > 0 {
		return nil // not the last released, or a delete already won.
	}

	// We're the last released and no delete won. Fire the store-side
	// reposition — the same context still carries the open tx so
	// ReleaseClaimItem participates in the same transaction.
	return s.ReleaseClaimItem(ctx, claimID, action)
}
