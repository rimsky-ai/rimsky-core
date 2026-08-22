// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber

package persistence

import (
	"context"
	"time"
)

type LifecycleOutboxRow struct {
	Seq               int64                         `json:"seq"`
	ClaimProducerName string                        `json:"claim_producer_name"`
	ScopeKind         LifecycleIdempotencyScopeKind `json:"scope_kind"`
	ScopeID           string                        `json:"scope_id"`
	Event             string                        `json:"event"`
	Payload           []byte                        `json:"payload"`
	StagedAt          time.Time                     `json:"staged_at"`
}

type LifecycleOutboxTable interface {
	Stage(ctx context.Context, row LifecycleOutboxRow, tx Tx) error
	DeleteBySeq(ctx context.Context, seq int64, tx Tx) error
	DeleteByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time, tx Tx) (int64, error)
	ListOldestPendingPerStream(ctx context.Context, limit int, tx Tx) ([]LifecycleOutboxRow, error)
	ListPendingForScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) ([]LifecycleOutboxRow, error)
}
