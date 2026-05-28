// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "errors"

// ClaimHandleState is the rimsky_claim_handles.state enum.
//
// active: currently held by a supervisor, heartbeating.
// committed: producer Commit fired; row preserved past terminal.
// abandoned: producer Abandon fired (natural or force-cancel); row preserved.
//
// State transitions are claimant-guarded; revival transitions are not
// permitted at the Go layer. See @blessed-invariant 4 (post-refactor
// text) for the two guard shapes (active-row mutations carry the per-row
// holder_supervisor_id guard; non-active-row deletions are guarded by
// absence + the scheduler-tick advisory lock).
//
// @concept: claim-handle
type ClaimHandleState string

const (
	ClaimHandleStateActive    ClaimHandleState = "active"
	ClaimHandleStateCommitted ClaimHandleState = "committed"
	ClaimHandleStateAbandoned ClaimHandleState = "abandoned"
)

// ErrIllegalClaimHandleTransition is returned by ClaimHandleTable.Promote
// when the affected-rows count is 0 — the row was not in the expected
// active state or was held by a different supervisor.
//
// Mirror of cascade.ErrIllegalTransition for node-runs.
var ErrIllegalClaimHandleTransition = errors.New("rimsky: illegal claim-handle state transition")
