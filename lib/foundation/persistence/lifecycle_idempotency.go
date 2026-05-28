// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

// LifecycleIdempotencyScopeKind enumerates the scopes a lifecycle
// idempotency row may track (template-scope, instance-scope, or
// run-scope for the host-agent OnRunScopeTerminal fan-out).
type LifecycleIdempotencyScopeKind string

const (
	LifecycleIdempotencyScopeTemplate LifecycleIdempotencyScopeKind = "template"
	LifecycleIdempotencyScopeInstance LifecycleIdempotencyScopeKind = "instance"
	// LifecycleIdempotencyScopeRunScope tracks per-RunScope terminal
	// fan-out idempotency for OnRunScopeTerminal subscribers. Per spec
	// 2026-05-24-host-agent-and-proxy-design.md.
	LifecycleIdempotencyScopeRunScope LifecycleIdempotencyScopeKind = "run_scope"
)

// LifecycleIdempotencyState enumerates the persisted lifecycle
// states for the rimsky_lifecycle_idempotencies table.
type LifecycleIdempotencyState string

const (
	LifecycleIdempotencyStateRegistered LifecycleIdempotencyState = "registered"
	LifecycleIdempotencyStateDeployed   LifecycleIdempotencyState = "deployed"
	LifecycleIdempotencyStateUndeployed LifecycleIdempotencyState = "undeployed"
	LifecycleIdempotencyStateCreated    LifecycleIdempotencyState = "created"
	// LifecycleIdempotencyStateRunScopeTerminal marks a run-scope-scoped
	// row whose OnRunScopeTerminal fan-out has been delivered. Per spec
	// 2026-05-24-host-agent-and-proxy-design.md.
	LifecycleIdempotencyStateRunScopeTerminal LifecycleIdempotencyState = "run_scope_terminal"
)

// LifecycleIdempotencyRow mirrors a row of rimsky_lifecycle_idempotencies.
type LifecycleIdempotencyRow struct {
	StoreRegistrationName string                        `json:"store_registration_name"`
	ScopeKind             LifecycleIdempotencyScopeKind `json:"scope_kind"`
	ScopeID               string                        `json:"scope_id"`
	State                 LifecycleIdempotencyState     `json:"state"`
	LastEventAt           time.Time                     `json:"last_event_at"`
}

// LifecycleIdempotencyTable is the rimsky_lifecycle_idempotencies
// accessor. Used by control-api's lifecycle fan-out helpers to record
// per-peer progress and short-circuit re-fires that would be no-ops.
type LifecycleIdempotencyTable interface {
	Get(ctx context.Context, storeName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) (*LifecycleIdempotencyRow, error)
	Upsert(ctx context.Context, row LifecycleIdempotencyRow, tx Tx) error
	Delete(ctx context.Context, storeName string, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	DeleteByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) error
	ListByScope(ctx context.Context, scopeKind LifecycleIdempotencyScopeKind, scopeID string, tx Tx) ([]LifecycleIdempotencyRow, error)
	// ListByStore returns every lifecycle row for a given store
	// registration (across all scopes). Used by the observability
	// per-store detail endpoint.
	ListByStore(ctx context.Context, storeName string, tx Tx) ([]LifecycleIdempotencyRow, error)
}
