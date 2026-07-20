// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

type LifecycleIdempotencyScopeKind string

const (
	LifecycleIdempotencyScopeTemplate LifecycleIdempotencyScopeKind = "template"
	LifecycleIdempotencyScopeInstance LifecycleIdempotencyScopeKind = "instance"
	LifecycleIdempotencyScopeRunScope LifecycleIdempotencyScopeKind = "run_scope"
)

type LifecycleIdempotencyState string

const (
	LifecycleIdempotencyStateRegistered       LifecycleIdempotencyState = "registered"
	LifecycleIdempotencyStateDeployed         LifecycleIdempotencyState = "deployed"
	LifecycleIdempotencyStateUndeployed       LifecycleIdempotencyState = "undeployed"
	LifecycleIdempotencyStateCreated          LifecycleIdempotencyState = "created"
	LifecycleIdempotencyStateRunScopeTerminal LifecycleIdempotencyState = "run_scope_terminal"
)

type LifecycleIdempotencyRow struct {
	ClaimProducerName string                        `json:"claim_producer_name"`
	ScopeKind         LifecycleIdempotencyScopeKind `json:"scope_kind"`
	ScopeID           string                        `json:"scope_id"`
	State             LifecycleIdempotencyState     `json:"state"`
	LastEventAt       time.Time                     `json:"last_event_at"`
}

type LifecycleIdempotencyTable interface {
	Get(ctx context.Context, claimProducerName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) (*LifecycleIdempotencyRow, error)
	Upsert(ctx context.Context, row LifecycleIdempotencyRow, tx Tx) error
	Delete(ctx context.Context, claimProducerName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	DeleteByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	ListByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) ([]LifecycleIdempotencyRow, error)
	ListByClaimProducer(ctx context.Context, claimProducerName string, tx Tx) ([]LifecycleIdempotencyRow, error)
}
