// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type DispatchRow struct {
	ID                             shared.UUID       `json:"id"`
	NodeID                         shared.UUID       `json:"node_id"`
	State                          cascade.NodeState `json:"state"`
	ExecutorName                   *string           `json:"executor_name,omitempty"`
	RequiredClaimProducers         []string          `json:"required_claim_producers,omitempty"`
	EnqueuedAt                     time.Time         `json:"enqueued_at"`
	ClaimedBy                      *string           `json:"claimed_by,omitempty"`
	ClaimedAt                      *time.Time        `json:"claimed_at,omitempty"`
	FrameID                        shared.UUID       `json:"frame_id"`
	AsyncAckID                     *string           `json:"async_ack_id,omitempty"`
	AsyncAckRegisteredAt           *time.Time        `json:"async_ack_registered_at,omitempty"`
	LastProgressAt                 *time.Time        `json:"last_progress_at,omitempty"`
	Tags                           []string          `json:"tags,omitempty"`
	EffectiveMaxQuietPeriodSeconds *int              `json:"effective_max_quiet_period_seconds,omitempty"`
	EffectiveMaxRuntimeSeconds     *int              `json:"effective_max_runtime_seconds,omitempty"`
	AsyncAckPrincipal              *string           `json:"async_ack_principal,omitempty"`
	AsyncCallbackURL               *string           `json:"async_callback_url,omitempty"`
}
