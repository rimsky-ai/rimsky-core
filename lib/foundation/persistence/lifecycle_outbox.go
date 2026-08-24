// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery
// @concept: lifecycle-subscriber

package persistence

import (
	"context"
	"time"
)

type LifecycleScopeKind string

const (
	LifecycleScopeTemplate LifecycleScopeKind = "template"
	LifecycleScopeInstance LifecycleScopeKind = "instance"
	LifecycleScopeRunScope LifecycleScopeKind = "run_scope"
)

type LifecycleOutboxRow struct {
	Seq               int64              `json:"seq"`
	ClaimProducerName string             `json:"claim_producer_name"`
	ScopeKind         LifecycleScopeKind `json:"scope_kind"`
	ScopeID           string             `json:"scope_id"`
	Event             string             `json:"event"`
	Payload           []byte             `json:"payload"`
	StagedAt          time.Time          `json:"staged_at"`
	AttemptCount      int                `json:"attempt_count"`
	NextAttemptAt     time.Time          `json:"next_attempt_at"`
	LastError         string             `json:"last_error"`
}

type LifecycleOutboxTable interface {
	Stage(ctx context.Context, row LifecycleOutboxRow, tx Tx) error
	// @decision: lifecycle-subscriber-at-least-once-delivery
	GetBySeq(ctx context.Context, seq int64, tx Tx) (*LifecycleOutboxRow, error)
	DeleteBySeq(ctx context.Context, seq int64, tx Tx) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time, tx Tx) (int64, error)
	// @decision: service-delivery-stall-signal
	RecordAttempt(ctx context.Context, seq int64, nextAttemptAt time.Time, lastError string, tx Tx) error
	// @decision: lifecycle-subscriber-at-least-once-delivery
	ListOldestPendingPerStream(ctx context.Context, limit int, dueAt time.Time, tx Tx) ([]LifecycleOutboxRow, error)
	ListPendingForScope(ctx context.Context, scopeKind LifecycleScopeKind, scopeID string, tx Tx) ([]LifecycleOutboxRow, error)
	// @decision: service-delivery-stall-signal
	ListPendingForService(ctx context.Context, claimProducerName string, limit int, tx Tx) ([]LifecycleOutboxRow, error)
	// @decision: service-delivery-stall-signal
	PendingSummaryByService(ctx context.Context, tx Tx) ([]ServiceOutboxPending, error)
}

// @decision: service-delivery-stall-signal
type ServiceOutboxPending struct {
	Service         string
	PendingCount    int
	OldestPendingAt time.Time
}
