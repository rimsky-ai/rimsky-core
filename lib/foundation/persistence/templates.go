// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// TemplateState is the persisted template lifecycle state. Templates
// are content-addressed (id is "sha256-<64-hex>" over an RFC 8785
// JCS-canonicalized spec). State is one of three persisted values
// (registered, deployed, undeployed); deregistered is the absent state
// — i.e., row deleted. Tags live in rimsky_template_tags as movable
// aliases.
type TemplateState string

const (
	TemplateStateRegistered TemplateState = "registered"
	TemplateStateDeployed   TemplateState = "deployed"
	TemplateStateUndeployed TemplateState = "undeployed"
)

// TemplateRow mirrors a row of rimsky_templates. JSON tags are
// snake_case per the observability spec §1.2, which the dashboard
// renders directly.
type TemplateRow struct {
	ID           string            `json:"id"`
	Spec         spec.TemplateSpec `json:"spec"`
	State        TemplateState     `json:"state"`
	RegisteredAt time.Time         `json:"registered_at"`
	// @constraint: Source is "direct" today; future package-manager values are reserved.
	Source string `json:"source"`
}

// TemplateInsertInput carries the per-row input for Insert.
type TemplateInsertInput struct {
	ID     string
	Spec   spec.TemplateSpec
	State  TemplateState
	Source string
}

// TemplateTable is the rimsky_templates accessor.
type TemplateTable interface {
	Insert(ctx context.Context, in TemplateInsertInput, tx Tx) error
	GetByHash(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
	List(ctx context.Context, filter TemplateListFilter, pag ListPagination, tx Tx) (PaginatedListResult[TemplateRow], error)
	UpdateState(ctx context.Context, hash string, newState TemplateState, tx Tx) error
	DeleteByHash(ctx context.Context, hash string, tx Tx) error
	LockForUpdate(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
}

// TemplateListFilter is the observability/list filter for templates.
type TemplateListFilter struct {
	// @constraint: empty State means no state filter.
	State TemplateState
	// Tag, when non-empty, restricts to templates carrying the given
	// tag in rimsky_template_tags. Used by the observability /v1/
	// observability/templates?tag=… browse filter (spec §1.2.2).
	Tag string
}

// TemplateTagRow mirrors a row of rimsky_template_tags. Tags are
// movable aliases pointing at template hashes; the rimsky_template_tags
// schema uses template_id as the FK column and updated_at as the
// upsert-timestamp.
type TemplateTagRow struct {
	Tag        string    `json:"tag"`
	TemplateID string    `json:"template_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TemplateTagTable is the rimsky_template_tags accessor.
type TemplateTagTable interface {
	Upsert(ctx context.Context, tag, templateID string, tx Tx) error
	Get(ctx context.Context, tag string, tx Tx) (*TemplateTagRow, error)
	ListByTemplate(ctx context.Context, templateID string, tx Tx) ([]TemplateTagRow, error)
	List(ctx context.Context, pag ListPagination, tx Tx) (PaginatedListResult[TemplateTagRow], error)
	Delete(ctx context.Context, tag string, tx Tx) (deleted bool, err error)
	CountByTemplate(ctx context.Context, templateID string, tx Tx) (int, error)
}
