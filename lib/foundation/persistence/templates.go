// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type TemplateState string

const (
	TemplateStateRegistered TemplateState = "registered"
	TemplateStateDeployed   TemplateState = "deployed"
	TemplateStateUndeployed TemplateState = "undeployed"
)

type TemplateRow struct {
	ID           string            `json:"id"`
	Spec         spec.TemplateSpec `json:"spec"`
	State        TemplateState     `json:"state"`
	RegisteredAt time.Time         `json:"registered_at"`
	Source string `json:"source"`
}

type TemplateInsertInput struct {
	ID     string
	Spec   spec.TemplateSpec
	State  TemplateState
	Source string
}

type TemplateTable interface {
	Insert(ctx context.Context, in TemplateInsertInput, tx Tx) error
	GetByHash(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
	List(ctx context.Context, filter TemplateListFilter, pag ListPagination, tx Tx) (PaginatedListResult[TemplateRow], error)
	UpdateState(ctx context.Context, hash string, newState TemplateState, tx Tx) error
	DeleteByHash(ctx context.Context, hash string, tx Tx) error
	LockForUpdate(ctx context.Context, hash string, tx Tx) (*TemplateRow, error)
}

type TemplateListFilter struct {
	State TemplateState
	Tag string
}

type TemplateTagRow struct {
	Tag        string    `json:"tag"`
	TemplateID string    `json:"template_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TemplateTagTable interface {
	Upsert(ctx context.Context, tag, templateID string, tx Tx) error
	Get(ctx context.Context, tag string, tx Tx) (*TemplateTagRow, error)
	ListByTemplate(ctx context.Context, templateID string, tx Tx) ([]TemplateTagRow, error)
	List(ctx context.Context, pag ListPagination, tx Tx) (PaginatedListResult[TemplateTagRow], error)
	Delete(ctx context.Context, tag string, tx Tx) (deleted bool, err error)
	CountByTemplate(ctx context.Context, templateID string, tx Tx) (int, error)
}
