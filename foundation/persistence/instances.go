// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// InstanceRow mirrors a row of rimsky_instances. An instance binds to a
// template hash at creation; instance_key is nullable.
type InstanceRow struct {
	ID           shared.UUID    `json:"id"`
	TemplateHash string         `json:"template_hash"` // FK to rimsky_templates.id
	InstanceKey  *string        `json:"instance_key"`  // nullable
	Params       map[string]any `json:"params"`
	CreatedAt    time.Time      `json:"created_at"`
	TerminatedAt *time.Time     `json:"terminated_at"` // nullable; set at terminal-state detection
}

// InstanceStore is the rimsky_instances accessor.
type InstanceStore interface {
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
}

// InstanceCreateInput is the per-row input for Create.
type InstanceCreateInput struct {
	ID           shared.UUID
	TemplateHash string
	InstanceKey  *string // nullable
	Params       map[string]any
}

// InstanceListFilter is the observability/list filter for instances.
type InstanceListFilter struct {
	TemplateHash string
	// Active, when non-nil, filters by terminated_at. Active=true →
	// terminated_at IS NULL; Active=false → terminated_at IS NOT NULL.
	Active *bool
}
