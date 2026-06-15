// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const templateTagCols = `tag, template_id, updated_at`

func (s *templateTagsImpl) Upsert(ctx context.Context, tag, templateID string, tx persistence.Tx) error {
	ex := s.q(tx)
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

func (s *templateTagsImpl) Get(ctx context.Context, tag string, tx persistence.Tx) (*persistence.TemplateTagRow, error) {
	ex := s.q(tx)
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

func (s *templateTagsImpl) ListByTemplate(ctx context.Context, templateID string, tx persistence.Tx) ([]persistence.TemplateTagRow, error) {
	ex := s.q(tx)
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

	var out []persistence.TemplateTagRow
	for rows.Next() {
		r, err := scanTemplateTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *templateTagsImpl) List(
	ctx context.Context,
	pag persistence.ListPagination,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.TemplateTagRow], error) {
	ex := s.q(tx)
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
		return persistence.PaginatedListResult[persistence.TemplateTagRow]{}, fmt.Errorf("template_tags.list: %w", err)
	}
	defer rows.Close()

	var out []persistence.TemplateTagRow
	for rows.Next() {
		r, err := scanTemplateTag(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.TemplateTagRow]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.TemplateTagRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].Tag
	}
	return persistence.PaginatedListResult[persistence.TemplateTagRow]{Rows: out, NextCursor: nextCursor}, nil
}

// Delete removes a tag row. Returns (true, nil) when a row was deleted,
// (false, nil) when the tag did not exist.
func (s *templateTagsImpl) Delete(ctx context.Context, tag string, tx persistence.Tx) (bool, error) {
	ex := s.q(tx)
	cmd, err := ex.Exec(ctx,
		`DELETE FROM rimsky_template_tags WHERE tag = $1`,
		tag,
	)
	if err != nil {
		return false, fmt.Errorf("template_tags.delete: %w", err)
	}
	return cmd.RowsAffected() > 0, nil
}

func (s *templateTagsImpl) CountByTemplate(ctx context.Context, templateID string, tx persistence.Tx) (int, error) {
	ex := s.q(tx)
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

func scanTemplateTag(sc scannable) (persistence.TemplateTagRow, error) {
	var (
		tag        string
		templateID string
		updatedAt  time.Time
	)
	if err := sc.Scan(&tag, &templateID, &updatedAt); err != nil {
		return persistence.TemplateTagRow{}, err
	}
	return persistence.TemplateTagRow{
		Tag:        tag,
		TemplateID: templateID,
		UpdatedAt:  updatedAt,
	}, nil
}
