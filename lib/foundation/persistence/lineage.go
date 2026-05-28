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

// LineageRecordKind discriminates the two row shapes persisted on
// table:rimsky_lineage. Per spec §Content lineage + the 2026-05-16
// forensics extension (claim_commit → claim_terminal rename so the
// projection covers natural Abandon + force-cancelled terminals as well
// as Commit).
const (
	LineageRecordKindLeafRun       = "leaf_run"
	LineageRecordKindClaimTerminal = "claim_terminal"
)

// LineageOutcome discriminates the per-terminal disposition for
// `claim_terminal` rows. `committed` covers the success branch; the two
// Abandon variants distinguish a natural exhaustion (give_up, error
// policy) from an operator-/sibling-driven force-cancel so post-mortem
// queries can reconstruct the actual flow.
const (
	LineageOutcomeCommitted      = "committed"
	LineageOutcomeAbandoned      = "abandoned"
	LineageOutcomeForceCancelled = "force_cancelled"
)

// LineageRow is the per-row representation of table:rimsky_lineage.
//
// The append-only projection covers two record kinds (record_kind
// column): "leaf_run" — emitted at every leaf-run terminal —
// "claim_terminal" — emitted at every claim handle Commit / Abandon /
// force-cancelled resolution. The per-kind payload lives in the `record`
// JSONB column; rimsky writes it verbatim and consumers (openlineage
// subscriber, asset history endpoint) decode it. The `outcome` column
// discriminates `claim_terminal` rows into committed / abandoned /
// force_cancelled.
//
// The shape of `record` for each kind is documented in spec §Content
// lineage / Leaf-run record shape and Claim-terminal record shape.
type LineageRow struct {
	ID         shared.UUID
	RecordKind string // "leaf_run" | "claim_terminal"
	InstanceID shared.UUID
	FrameID    shared.UUID
	ObservedAt time.Time
	Record     json.RawMessage // per-kind payload; opaque to rimsky downstream
	// Outcome carries the per-terminal disposition for `claim_terminal`
	// rows: one of `committed | abandoned | force_cancelled`. Empty on
	// `leaf_run` rows (the column is not meaningful for computational
	// terminals — every leaf-run row persists with outcome="" verbatim).
	// Persisted as a column (rather than nested in the JSON payload) so
	// analytical queries can filter without JSON extraction.
	Outcome string
}

// LineageQuery is the filter used by LineageTable.Query.
type LineageQuery struct {
	InstanceID     *shared.UUID
	Kind           string
	ObservedAfter  *time.Time
	ObservedBefore *time.Time
}

// LineageTable is the per-row-type Table accessor for
// table:rimsky_lineage. The projection is append-only — never updated,
// only inserted (and bulk-deleted by the retention sweep in E10).
type LineageTable interface {
	// Insert appends a lineage row.
	Insert(ctx context.Context, tx Tx, row LineageRow) error

	// GetByRunID returns leaf_run rows for a specific run_id ordered by
	// observed_at ascending. The query reads the record->>'run_id'
	// JSONB path.
	GetByRunID(ctx context.Context, runID shared.UUID) ([]LineageRow, error)

	// GetByClaimHandleID returns claim_terminal rows for a specific claim
	// handle ordered by observed_at ascending. Used by the asset
	// materialization-history endpoint.
	GetByClaimHandleID(ctx context.Context, handleID shared.UUID) ([]LineageRow, error)

	// Query returns rows matching the filter, paginated.
	Query(ctx context.Context, q LineageQuery, pag ListPagination) (PaginatedListResult[LineageRow], error)

	// QueryByParentRunID returns leaf_run rows whose
	// `record->>'parent_run_id'` matches parentRunID, ordered by
	// observed_at ascending. Used by the descendant-walk endpoint
	// (`GET /lineage/runs/{run_id}/descendants?depth=N`) to find
	// children of the seed run without page-scanning the entire
	// projection. Postgres uses a JSONB key lookup; SQLite uses
	// `json_extract`. `limit` caps the per-call result set; pre-v1
	// callers pass `lineageWalkPerFrontierLimit` (1000) which covers
	// realistic fan-out widths.
	QueryByParentRunID(ctx context.Context, parentRunID shared.UUID, limit int) ([]LineageRow, error)

	// DeleteOlderThan deletes lineage rows whose observed_at is
	// before cutoff AND whose corresponding run or claim_handle is no
	// longer present. Used by the retention sweep (E10).
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}
