// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"time"

	"github.com/fallguy/rimsky/foundation/shared"
)

// DispatchRow is the claimable unit of work (see spec §9.6 / §11.1).
//
// ExecutorName is nullable: native (claim-only) nodes have no executor and
// are run by the supervisor's omnibus runner directly (spec §7.3). The
// concurrency_tags field is gone — per-node concurrency now lives in the
// template's `locks: [...]` declarations enforced via rimsky_claim_handles.
//
// RequiredStores is denormalised from the template at enqueue time and
// drives the §6.2 supervisor-pool specialisation predicate. LastHeartbeatAt
// drives the §7.5 dispatch-claim sweep predicate (claim age tracks
// heartbeat liveness rather than initial-claim time).
//
// Lives in foundation/persistence (and not graph/shared) because the rows
// it describes are produced and consumed entirely below the modeling layer
// — every observability-row reader (control-api) reaches it through the
// persistence interfaces.
type DispatchRow struct {
	ID              shared.UUID `json:"id"`
	NodeID          shared.UUID `json:"node_id"`
	ExecutorName    *string     `json:"executor_name,omitempty"`
	RequiredStores  []string    `json:"required_stores,omitempty"`
	EnqueuedAt      time.Time   `json:"enqueued_at"`
	ClaimedBy       *string     `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time  `json:"claimed_at,omitempty"`
	LastHeartbeatAt *time.Time  `json:"last_heartbeat_at,omitempty"`
	// FrameID is the frame this dispatch row belongs to (per
	// docs/history/2026-04-26-frame-resolution-design.md §10.2). NOT NULL
	// in storage; blessed-invariant 19 forbids in-flight dispatch rows
	// without a frame_id.
	FrameID shared.UUID `json:"frame_id"`
}
