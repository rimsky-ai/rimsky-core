// TemplateTagsStore — Postgres-backed storage.TemplateTagsStore. Per
// docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md §1.1:
// rimsky_template_tags is the movable-alias table mapping a tag string
// to a template hash (with FK ON DELETE RESTRICT).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
)

type TemplateTagsStore struct {
	pool *pgxpool.Pool
}

var _ storage.TemplateTagsStore = (*TemplateTagsStore)(nil)

const templateTagCols = `tag, template_id, updated_at`

func (s *TemplateTagsStore) Upsert(ctx context.Context, tag, templateID string, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_template_tags (tag, template_id, updated_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (tag) DO UPDATE
		   SET template_id = EXCLUDED.template_id,
		       updated_at = now()`,
		tag, templateID,
	)
	if err != nil {
		return fmt.Errorf("template_tags.upsert: %w", err)
	}
	return nil
}

func (s *TemplateTagsStore) Get(ctx context.Context, tag string, tx storage.Tx) (*storage.TemplateTagRow, error) {
	ex := q(tx, s.pool)
	row := ex.QueryRow(ctx,
		`SELECT `+templateTagCols+` FROM rimsky_template_tags WHERE tag = $1`,
		tag,
	)
	r, err := scanTemplateTag(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("template_tags.get: %w", err)
	}
	return &r, nil
}

func (s *TemplateTagsStore) ListByTemplate(ctx context.Context, templateID string, tx storage.Tx) ([]storage.TemplateTagRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+templateTagCols+` FROM rimsky_template_tags
		 WHERE template_id = $1
		 ORDER BY tag ASC`,
		templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("template_tags.listByTemplate: %w", err)
	}
	defer rows.Close()

	var out []storage.TemplateTagRow
	for rows.Next() {
		r, err := scanTemplateTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *TemplateTagsStore) List(
	ctx context.Context,
	pag storage.ListPagination,
	tx storage.Tx,
) (storage.PaginatedListResult[storage.TemplateTagRow], error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *string
	if pag.Cursor != "" {
		v := pag.Cursor
		cursor = &v
	}
	rows, err := ex.Query(ctx,
		`SELECT `+templateTagCols+`
		 FROM rimsky_template_tags
		 WHERE ($1::text IS NULL OR tag > $1)
		 ORDER BY tag ASC
		 LIMIT $2`,
		cursor, limit,
	)
	if err != nil {
		return storage.PaginatedListResult[storage.TemplateTagRow]{}, fmt.Errorf("template_tags.list: %w", err)
	}
	defer rows.Close()

	var out []storage.TemplateTagRow
	for rows.Next() {
		r, err := scanTemplateTag(rows)
		if err != nil {
			return storage.PaginatedListResult[storage.TemplateTagRow]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return storage.PaginatedListResult[storage.TemplateTagRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].Tag
	}
	return storage.PaginatedListResult[storage.TemplateTagRow]{Rows: out, NextCursor: nextCursor}, nil
}

// Delete removes a tag row. Returns (true, nil) when a row was deleted,
// (false, nil) when the tag did not exist.
func (s *TemplateTagsStore) Delete(ctx context.Context, tag string, tx storage.Tx) (bool, error) {
	ex := q(tx, s.pool)
	cmd, err := ex.Exec(ctx,
		`DELETE FROM rimsky_template_tags WHERE tag = $1`,
		tag,
	)
	if err != nil {
		return false, fmt.Errorf("template_tags.delete: %w", err)
	}
	return cmd.RowsAffected() > 0, nil
}

func (s *TemplateTagsStore) CountByTemplate(ctx context.Context, templateID string, tx storage.Tx) (int, error) {
	ex := q(tx, s.pool)
	var n int
	err := ex.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_template_tags WHERE template_id = $1`,
		templateID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("template_tags.countByTemplate: %w", err)
	}
	return n, nil
}

func scanTemplateTag(sc scannable) (storage.TemplateTagRow, error) {
	var (
		tag        string
		templateID string
		updatedAt  time.Time
	)
	if err := sc.Scan(&tag, &templateID, &updatedAt); err != nil {
		return storage.TemplateTagRow{}, err
	}
	return storage.TemplateTagRow{
		Tag:        tag,
		TemplateID: templateID,
		UpdatedAt:  updatedAt,
	}, nil
}
