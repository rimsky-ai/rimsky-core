// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/foundation/shared"
)

// InstanceRow mirrors a row of rimsky_instances. An instance binds to a
// template hash at creation; instance_key is nullable.
//
// AttributeOverrides carries optional per-instance JSON overrides that
// rimsky deep-merges into per-node attributes at dispatch time. Shape is
// validated at instance-create by the control-api but the fragment
// values are inert at dispatch (covered by concept:inertness
// structural-inertness discipline). Empty map = no overrides; the
// column has NOT NULL DEFAULT '{}' so dispatch-time reads are
// unconditional.
//
// FrameDeliveryMode selects per-instance message-delivery semantics for
// `DeliverPendingMessages` at frame creation
// (col:rimsky_instances.frame_delivery_mode). One of "serial_queue" or
// "coalesce"; the column default is "coalesce".
type InstanceRow struct {
	ID                 shared.UUID    `json:"id"`
	TemplateHash       string         `json:"template_hash"` // FK to rimsky_templates.id
	InstanceKey        *string        `json:"instance_key"`  // nullable
	Params             map[string]any `json:"params"`
	AttributeOverrides map[string]any `json:"attribute_overrides"`
	// AttributeOverridesMatchCounts is the per-entry match counter for
	// AttributeOverrides.by_match. Indexed by by_match entry position;
	// each int64 counts how many dispatches matched that entry. Length
	// equals len(AttributeOverrides["by_match"]); empty for instances
	// with no by_match entries. Read via GET /instances/{id}; written by
	// the supervisor's IncrementAttributeOverrideMatchCounts call after
	// applyAttributeOverrides returns matched indices.
	//
	// @concept: attribute (L5 matcher overlay)
	AttributeOverridesMatchCounts []int64 `json:"attribute_overrides_match_counts,omitempty"`
	FrameDeliveryMode             string  `json:"frame_delivery_mode"`
	// MainRunScopeID projects rimsky_instances.main_run_scope_id — the
	// instance's main RunScope (FK to rimsky_run_scopes.id). Every
	// instance has exactly one main RunScope, allocated by the create
	// handler before the InstanceRow row is INSERTed. Per
	// concept:run-scope.
	//
	// @concept: run-scope
	MainRunScopeID shared.UUID `json:"main_run_scope_id"`
	CreatedAt      time.Time   `json:"created_at"`
	TerminatedAt   *time.Time  `json:"terminated_at"` // nullable; set at terminal-state detection
}

// InstanceTable is the rimsky_instances accessor.
type InstanceTable interface {
	Create(ctx context.Context, args InstanceCreateInput, tx Tx) (InstanceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*InstanceRow, error)
	GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx Tx) (*InstanceRow, error)
	// FindAnyByInstanceKey looks up an instance by instance_key alone
	// (no template hash). Used by the control-api's `idOrKey` URL
	// resolver. Returns (nil, nil) when no row matches.
	FindAnyByInstanceKey(ctx context.Context, instanceKey string, tx Tx) (*InstanceRow, error)
	List(ctx context.Context, filter InstanceListFilter, pag ListPagination, tx Tx) (PaginatedListResult[InstanceRow], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	MarkTerminated(ctx context.Context, id shared.UUID, tx Tx) error
	CountActiveByTemplate(ctx context.Context, templateHash string, tx Tx) (int, error)
	ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx Tx) ([]InstanceRow, error)
	// CountByActive returns (active, terminated) instance counts for the
	// system summary endpoint. Active = TerminatedAt IS NULL.
	CountByActive(ctx context.Context, tx Tx) (active int, terminated int, err error)
	// IncrementAttributeOverrideMatchCounts atomically increments the
	// counter at each of the given by_match entry positions on the
	// instance's attribute_overrides_match_counts column. Out-of-range
	// indices are silently no-op'd at the persistence layer;
	// observability surface is the application-layer caller. The
	// runtime's `incrementMatchCountersAfterMerge` Warn-logs failures
	// of the entire call but does not enumerate per-index drops.
	//
	// tx is required (non-nil); the backend's q(tx) accessor panics on
	// nil tx per the package's universal convention. Dispatch-path
	// callers wrap with args.Persist.Transaction(...) to open a short
	// dedicated tx (the dispatch-row write has already committed via
	// transitionToRunning before this is invoked, so the increment
	// runs in its own separate tx, not nested with anything).
	//
	// Per spec
	// .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
	// §"Persistence API".
	IncrementAttributeOverrideMatchCounts(ctx context.Context, instanceID shared.UUID, indices []int, tx Tx) error
}

// InstanceCreateInput is the per-row input for Create.
//
// AttributeOverrides is the validated overrides blob. Persistence does
// not re-validate; it serialises and stores. nil/empty are equivalent
// and persisted as `{}`.
//
// FrameDeliveryMode, when empty, falls back to the column default
// ("coalesce"). Otherwise the value is written verbatim — the column's
// CHECK constraint enforces the discriminator vocabulary.
type InstanceCreateInput struct {
	ID                 shared.UUID
	TemplateHash       string
	InstanceKey        *string // nullable
	Params             map[string]any
	AttributeOverrides map[string]any
	// AttributeOverridesMatchCounts is the initial counter array,
	// typically a zero-filled slice of length len(by_match). The control-
	// API handler initialises this from the request body's by_match
	// length; the persistence layer persists it verbatim.
	AttributeOverridesMatchCounts []int64
	FrameDeliveryMode             string
	// MainRunScopeID is the main RunScope's id, allocated by the
	// create handler before Create is called. Required (non-nullable;
	// every instance has exactly one main RunScope). The column
	// rimsky_instances.main_run_scope_id lands via the Phase B
	// migration; in Phase A the wiring is in place but the migration
	// is deferred. Per concept:run-scope.
	//
	// @concept: run-scope
	MainRunScopeID shared.UUID
}

// InstanceListFilter is the observability/list filter for instances.
type InstanceListFilter struct {
	TemplateHash string
	// Active, when non-nil, filters by terminated_at. Active=true →
	// terminated_at IS NULL; Active=false → terminated_at IS NOT NULL.
	Active *bool
}
