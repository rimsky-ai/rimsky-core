// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type InstanceRow struct {
	ID                 shared.UUID    `json:"id"`
	TemplateHash       string         `json:"template_hash"`
	InstanceKey        *string        `json:"instance_key"`
	Params             map[string]any `json:"params"`
	AttributeOverrides map[string]any `json:"attribute_overrides"`
	// @concept: attribute
	AttributeOverridesMatchCounts []int64    `json:"attribute_overrides_match_counts,omitempty"`
	CreatedAt                     time.Time  `json:"created_at"`
	TerminatedAt                  *time.Time `json:"terminated_at"`
	// @concept: breakpoint
	Paused            bool            `json:"paused"`
	ServiceBindings   json.RawMessage `json:"service_bindings,omitempty"`
	CreatedByAPIKeyID *shared.UUID    `json:"created_by_api_key_id,omitempty"`
	// @concept: instance
	MessageQueueMode string `json:"message_queue_mode"`
}

type InstanceTable interface {
	Create(ctx context.Context, args InstanceCreateInput, tx Tx) (InstanceRow, error)
	Get(ctx context.Context, id shared.UUID, tx Tx) (*InstanceRow, error)
	GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx Tx) (*InstanceRow, error)
	FindAnyByInstanceKey(ctx context.Context, instanceKey string, tx Tx) (*InstanceRow, error)
	List(ctx context.Context, filter InstanceListFilter, pag ListPagination, tx Tx) (PaginatedListResult[InstanceRow], error)
	Delete(ctx context.Context, id shared.UUID, tx Tx) error
	MarkTerminated(ctx context.Context, id shared.UUID, tx Tx) error
	CountActiveByTemplate(ctx context.Context, templateHash string, tx Tx) (int, error)
	ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx Tx) ([]InstanceRow, error)
	CountByActive(ctx context.Context, tx Tx) (active int, terminated int, err error)
	IncrementAttributeOverrideMatchCounts(ctx context.Context, instanceID shared.UUID, indices []int, tx Tx) error
	// @concept: breakpoint
	SetPaused(ctx context.Context, instanceID shared.UUID, paused bool, tx Tx) (priorValue bool, err error)
}

type InstanceCreateInput struct {
	ID                            shared.UUID
	TemplateHash                  string
	InstanceKey                   *string
	Params                        map[string]any
	AttributeOverrides            map[string]any
	AttributeOverridesMatchCounts []int64
	// @concept: breakpoint
	Paused            bool
	ServiceBindings   json.RawMessage
	CreatedByAPIKeyID *shared.UUID
	// @concept: instance
	MessageQueueMode string
}

type InstanceListFilter struct {
	TemplateHash string
	Active       *bool
}
