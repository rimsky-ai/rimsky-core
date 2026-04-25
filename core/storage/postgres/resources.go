// ResourceRegistry — port of rimsky/src/storage/postgres/resource-registry.ts.
// Adapted for the cell→node rename: `owner_cell_id` is now `owner_node_id`.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type ResourceRegistry struct {
	pool *pgxpool.Pool
}

var _ storage.ResourceRegistry = (*ResourceRegistry)(nil)

const resourceCols = `
  id, resource_path, owner_node_id, current_version_id, previous_version_id,
  keep_versions, created_at
`

const versionCols = `
  id, resource_id, produced_by, data, data_ref, change_summary, committed_at
`

func (s *ResourceRegistry) Create(ctx context.Context, in storage.ResourceCreateInput, tx storage.Tx) (storage.ResourceRow, error) {
	ex := q(tx, s.pool)
	keep := in.KeepVersions
	if keep == 0 {
		keep = 2
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_resources (id, resource_path, owner_node_id, keep_versions)
		 VALUES (gen_random_uuid(), $1, $2, $3)
		 RETURNING `+resourceCols,
		in.ResourcePath, in.OwnerNodeID, keep,
	)
	return scanResource(row)
}

func (s *ResourceRegistry) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.ResourceRow, error) {
	ex := q(tx, s.pool)
	row := ex.QueryRow(ctx,
		`SELECT `+resourceCols+` FROM rimsky_resources WHERE id = $1`, id)
	r, err := scanResource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *ResourceRegistry) ListByOwner(ctx context.Context, ownerNodeID shared.UUID, tx storage.Tx) ([]storage.ResourceRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+resourceCols+` FROM rimsky_resources
		 WHERE owner_node_id = $1 ORDER BY created_at ASC`, ownerNodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.ResourceRow
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ResourceRegistry) CommitVersion(ctx context.Context, resourceID shared.UUID, in storage.ResourceCommitInput, tx storage.Tx) (storage.ResourceVersionRow, error) {
	if tx != nil {
		return s.commitVersionOn(ctx, q(tx, s.pool), resourceID, in)
	}
	pgT, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	defer func() { _ = pgT.Rollback(ctx) }()
	out, err := s.commitVersionOn(ctx, pgT, resourceID, in)
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	if err := pgT.Commit(ctx); err != nil {
		return storage.ResourceVersionRow{}, err
	}
	return out, nil
}

func (s *ResourceRegistry) commitVersionOn(ctx context.Context, ex querier, resourceID shared.UUID, in storage.ResourceCommitInput) (storage.ResourceVersionRow, error) {
	var (
		currentVersion *shared.UUID
		keepVersions   int
	)
	err := ex.QueryRow(ctx,
		`SELECT current_version_id, keep_versions FROM rimsky_resources
		 WHERE id = $1 FOR UPDATE`, resourceID,
	).Scan(&currentVersion, &keepVersions)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ResourceVersionRow{}, shared.Wrap(errors.New("resource not found"),
				"resource not found", map[string]any{"resource_id": resourceID})
		}
		return storage.ResourceVersionRow{}, err
	}
	var producedBy any
	if in.ProducedBy != (shared.UUID{}) {
		producedBy = in.ProducedBy
	}
	var dataArg any
	if len(in.Data) > 0 {
		dataArg = in.Data
	}
	var dataRefArg any
	if len(in.DataRef) > 0 {
		// data_ref is JSONB (migration 002); pgx v5 accepts []byte directly
		// for JSONB columns as long as the bytes are valid JSON.
		dataRefArg = in.DataRef
	}
	var changeArg any
	if in.ChangeSummary != "" {
		changeArg = in.ChangeSummary
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_resource_versions
		   (id, resource_id, produced_by, data, data_ref, change_summary)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		 RETURNING `+versionCols,
		resourceID, producedBy, dataArg, dataRefArg, changeArg,
	)
	newVersion, err := scanVersion(row)
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}

	_, err = ex.Exec(ctx,
		`UPDATE rimsky_resources
		   SET previous_version_id = $2,
		       current_version_id = $3
		 WHERE id = $1`,
		resourceID, currentVersion, newVersion.ID,
	)
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	if _, err := s.gcOldVersionsOn(ctx, ex, resourceID, keepVersions); err != nil {
		return storage.ResourceVersionRow{}, err
	}
	return newVersion, nil
}

func (s *ResourceRegistry) NoOpCommit(ctx context.Context, resourceID shared.UUID, tx storage.Tx) error {
	// Intentionally no-op. Exists for interface parity.
	_ = ctx
	_ = resourceID
	_ = tx
	return nil
}

func (s *ResourceRegistry) GCOldVersions(ctx context.Context, resourceID shared.UUID, keep int, tx storage.Tx) (int, error) {
	ex := q(tx, s.pool)
	return s.gcOldVersionsOn(ctx, ex, resourceID, keep)
}

func (s *ResourceRegistry) gcOldVersionsOn(ctx context.Context, ex querier, resourceID shared.UUID, keep int) (int, error) {
	// Keep set = union of (a) top-N by committed_at DESC, (b) current &
	// previous version ids referenced by the parent row. See TS src for why
	// (b) is required (restore_version then subsequent commit).
	tag, err := ex.Exec(ctx,
		`DELETE FROM rimsky_resource_versions v
		   WHERE v.resource_id = $1
		     AND v.id NOT IN (
		       SELECT id FROM rimsky_resource_versions
		        WHERE resource_id = $1
		        ORDER BY committed_at DESC
		        LIMIT $2
		     )
		     AND v.id NOT IN (
		       SELECT unnest(ARRAY[current_version_id, previous_version_id])
		         FROM rimsky_resources WHERE id = $1
		     )`,
		resourceID, keep,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RestoreVersion swaps current pointer. target=="previous" uses
// previous_version_id; target=="id" uses versionID. target=="current" is a
// no-op-like case that's not currently in the interface (we treat any value
// other than "previous" as "by id").
func (s *ResourceRegistry) RestoreVersion(ctx context.Context, resourceID shared.UUID, target string, versionID shared.UUID, tx storage.Tx) (storage.ResourceVersionRow, error) {
	if tx != nil {
		return s.restoreVersionOn(ctx, q(tx, s.pool), resourceID, target, versionID)
	}
	pgT, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	defer func() { _ = pgT.Rollback(ctx) }()
	out, err := s.restoreVersionOn(ctx, pgT, resourceID, target, versionID)
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	if err := pgT.Commit(ctx); err != nil {
		return storage.ResourceVersionRow{}, err
	}
	return out, nil
}

func (s *ResourceRegistry) restoreVersionOn(ctx context.Context, ex querier, resourceID shared.UUID, target string, versionID shared.UUID) (storage.ResourceVersionRow, error) {
	var targetVersionID shared.UUID
	if target == "previous" {
		var prev *shared.UUID
		err := ex.QueryRow(ctx,
			`SELECT previous_version_id FROM rimsky_resources WHERE id = $1 FOR UPDATE`,
			resourceID,
		).Scan(&prev)
		if err != nil {
			return storage.ResourceVersionRow{}, err
		}
		if prev == nil {
			return storage.ResourceVersionRow{}, shared.Wrap(errors.New("no previous version"),
				"no previous version to restore",
				map[string]any{"resource_id": resourceID})
		}
		targetVersionID = *prev
	} else {
		// Take the row lock even for a by-id restore to serialize with commits.
		var one int
		if err := ex.QueryRow(ctx,
			`SELECT 1 FROM rimsky_resources WHERE id = $1 FOR UPDATE`, resourceID,
		).Scan(&one); err != nil {
			return storage.ResourceVersionRow{}, err
		}
		targetVersionID = versionID
	}

	row := ex.QueryRow(ctx,
		`SELECT `+versionCols+` FROM rimsky_resource_versions
		 WHERE id = $1 AND resource_id = $2`,
		targetVersionID, resourceID,
	)
	ver, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ResourceVersionRow{}, shared.Wrap(errors.New("version not found"),
				"version not found or not owned by resource",
				map[string]any{"resource_id": resourceID, "target_version_id": targetVersionID})
		}
		return storage.ResourceVersionRow{}, err
	}
	_, err = ex.Exec(ctx,
		`UPDATE rimsky_resources SET current_version_id = $2 WHERE id = $1`,
		resourceID, targetVersionID,
	)
	if err != nil {
		return storage.ResourceVersionRow{}, err
	}
	return ver, nil
}

func (s *ResourceRegistry) GetVersion(ctx context.Context, versionID shared.UUID, tx storage.Tx) (*storage.ResourceVersionRow, error) {
	ex := q(tx, s.pool)
	row := ex.QueryRow(ctx,
		`SELECT `+versionCols+` FROM rimsky_resource_versions WHERE id = $1`, versionID)
	v, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *ResourceRegistry) ListVersions(ctx context.Context, resourceID shared.UUID, tx storage.Tx) ([]storage.ResourceVersionRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+versionCols+` FROM rimsky_resource_versions
		 WHERE resource_id = $1 ORDER BY committed_at DESC`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.ResourceVersionRow
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *ResourceRegistry) ListVersionsPaged(ctx context.Context, resourceID shared.UUID, pag storage.ListPagination, tx storage.Tx) (storage.PaginatedListResult[storage.ResourceVersionRow], error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *shared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return storage.PaginatedListResult[storage.ResourceVersionRow]{}, fmt.Errorf("listVersionsPaged: bad cursor: %w", err)
		}
		cursor = &u
	}
	rows, err := ex.Query(ctx,
		`SELECT `+versionCols+` FROM rimsky_resource_versions
		 WHERE resource_id = $1
		   AND (
		     $2::uuid IS NULL
		     OR (committed_at, id) < (
		       (SELECT committed_at FROM rimsky_resource_versions WHERE id = $2::uuid),
		       $2::uuid
		     )
		   )
		 ORDER BY committed_at DESC, id DESC
		 LIMIT $3`,
		resourceID, cursor, limit,
	)
	if err != nil {
		return storage.PaginatedListResult[storage.ResourceVersionRow]{}, err
	}
	defer rows.Close()
	var out []storage.ResourceVersionRow
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return storage.PaginatedListResult[storage.ResourceVersionRow]{}, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return storage.PaginatedListResult[storage.ResourceVersionRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return storage.PaginatedListResult[storage.ResourceVersionRow]{Rows: out, NextCursor: nextCursor}, nil
}

// ---- helpers ----

func scanResource(sc scannable) (storage.ResourceRow, error) {
	var (
		r         storage.ResourceRow
		current   *shared.UUID
		previous  *shared.UUID
		createdAt time.Time
	)
	if err := sc.Scan(
		&r.ID, &r.ResourcePath, &r.OwnerNodeID,
		&current, &previous, &r.KeepVersions, &createdAt,
	); err != nil {
		return storage.ResourceRow{}, err
	}
	r.CurrentVersionID = current
	r.PreviousVersionID = previous
	r.CreatedAt = createdAt
	return r, nil
}

func scanVersion(sc scannable) (storage.ResourceVersionRow, error) {
	var (
		v             storage.ResourceVersionRow
		producedBy    *shared.UUID
		data          []byte
		dataRef       []byte // JSONB column (migration 002); pgx v5 scans JSONB into []byte
		changeSummary *string
		committedAt   time.Time
	)
	if err := sc.Scan(
		&v.ID, &v.ResourceID, &producedBy,
		&data, &dataRef, &changeSummary, &committedAt,
	); err != nil {
		return storage.ResourceVersionRow{}, err
	}
	v.ProducedBy = producedBy
	v.Data = data
	v.DataRef = dataRef
	if changeSummary != nil {
		v.ChangeSummary = *changeSummary
	}
	v.CommittedAt = committedAt
	return v, nil
}
