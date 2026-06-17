// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// DispatchRow is the claimable unit of work (see spec §9.6 / §11.1).
//
// ExecutorName is nullable: native (claim-only) nodes have no executor and
// are run by the supervisor's omnibus runner directly (spec §7.3). The
// concurrency_tags field is gone — per-node concurrency now lives in the
// template's `locks: [...]` declarations enforced via rimsky_claim_handles.
//
// RequiredStores is denormalised from the template at enqueue time and
// drives the §6.2 supervisor-pool specialisation predicate.
//
// Lives in foundation/persistence (and not graph/shared) because the rows
// it describes are produced and consumed entirely below the modeling layer
// — every observability-row reader (control-api) reaches it through the
// persistence interfaces.
type DispatchRow struct {
	ID             shared.UUID `json:"id"`
	NodeID         shared.UUID `json:"node_id"`
	ExecutorName   *string     `json:"executor_name,omitempty"`
	RequiredStores []string    `json:"required_stores,omitempty"`
	EnqueuedAt     time.Time   `json:"enqueued_at"`
	ClaimedBy      *string     `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time  `json:"claimed_at,omitempty"`
	// FrameID is the frame this dispatch row belongs to. NOT NULL in
	// storage; blessed-invariant 19 forbids in-flight dispatch rows
	// without a frame_id.
	FrameID shared.UUID `json:"frame_id"`
	// AsyncAckID is set when this dispatch is in flight on the async
	// callback path: the executor returned AwaitAsyncCallback and the
	// supervisor persisted the ack id so a later POST /v1/callback/{id}
	// can locate this row durably across supervisor restart. NULL when
	// the dispatch is synchronous or has not yet handed off.
	AsyncAckID *string `json:"async_ack_id,omitempty"`
	// AsyncAckRegisteredAt is the wall-clock at which the supervisor
	// persisted AsyncAckID. NULL when AsyncAckID is NULL.
	AsyncAckRegisteredAt *time.Time `json:"async_ack_registered_at,omitempty"`
	// LastProgressAt is the per-dispatch liveness timestamp bumped by
	// attribute writeback, scratch writeback, and explicit keepalive
	// POSTs. Drives the supervisor's quiet-period sweep for async
	// dispatches; distinct from the per-frame Frame.LastProgressAt
	// which lives on rimsky_frames.
	LastProgressAt *time.Time `json:"last_progress_at,omitempty"`
	// Tags is the set (deduplicated at decode) of subscriber-visible
	// discriminators the emitting executor attached on its settling
	// terminal. Empty when no terminal has settled yet.
	Tags []string `json:"tags,omitempty"`
	// EffectiveMaxQuietPeriodSeconds is the denormalized per-row
	// max_quiet_period, set at AwaitAsyncCallback registration time
	// (resolveMaxQuietPeriod folds per-node template value over
	// deployment default over built-in 0=disabled). NULL when the
	// dispatch never went async, or when the resolved value is the
	// "disabled" 0; SweepExecutorDeadlines treats NULL as no cap.
	EffectiveMaxQuietPeriodSeconds *int `json:"effective_max_quiet_period_seconds,omitempty"`
	// EffectiveMaxRuntimeSeconds is the denormalized per-row
	// max_runtime, set at AwaitAsyncCallback registration time
	// (resolveMaxRuntime folds per-node template value over deployment
	// default over built-in 0=disabled). NULL when the dispatch never
	// went async, or when the resolved value is the "disabled" 0;
	// SweepExecutorDeadlines treats NULL as no cap.
	EffectiveMaxRuntimeSeconds *int `json:"effective_max_runtime_seconds,omitempty"`
}
