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

// @concept: lineage
// @constraint: discriminates the two row shapes persisted on rimsky_lineage;
// claim_terminal (post 2026-05-16 forensics extension renaming claim_commit)
// covers natural Abandon + force-cancelled terminals as well as Commit.
const (
	LineageRecordKindLeafRun       = "leaf_run"
	LineageRecordKindClaimTerminal = "claim_terminal"
)

const (
	LineageOutcomeCommitted      = "committed"
	LineageOutcomeAbandoned      = "abandoned"
	LineageOutcomeForceCancelled = "force_cancelled"
)

type LineageRow struct {
	ID         shared.UUID
	RecordKind string
	InstanceID shared.UUID
	FrameID    shared.UUID
	ObservedAt time.Time
	Record     json.RawMessage
	Outcome    string
}

type LineageQuery struct {
	InstanceID     *shared.UUID
	Kind           string
	ObservedAfter  *time.Time
	ObservedBefore *time.Time
}

type LineageTable interface {
	Insert(ctx context.Context, tx Tx, row LineageRow) error

	GetByRunID(ctx context.Context, runID shared.UUID) ([]LineageRow, error)

	GetByClaimHandleID(ctx context.Context, handleID shared.UUID) ([]LineageRow, error)

	Query(ctx context.Context, q LineageQuery, pag ListPagination) (PaginatedListResult[LineageRow], error)

	QueryByParentRunID(ctx context.Context, parentRunID shared.UUID, limit int) ([]LineageRow, error)

	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)

	CountOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}
