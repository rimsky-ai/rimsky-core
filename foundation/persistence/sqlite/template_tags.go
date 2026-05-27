// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// template_tags.go — SQLite-backed persistence.TemplateTagTable.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
)

const templateTagCols = `tag, template_id, updated_at`

func (s *templateTagsImpl) Upsert(ctx context.Context, tag, templateID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_template_tags (tag, template_id, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(tag) DO UPDATE
		   SET template_id = excluded.template_id,
		       updated_at = excluded.updated_at`,
		tag, templateID, nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("template_tags.upsert: %w", err)
	}
	return nil
}

func (s *templateTagsImpl) Get(ctx context.Context, tag string, tx persistence.Tx) (*persistence.TemplateTagRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+templateTagCols+` FROM rimsky_template_tags WHERE tag = ?`,
		tag,
	)
	r, err := scanTemplateTag(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("template_tags.get: %w", err)
	}
	return &r, nil
}

func (s *templateTagsImpl) ListByTemplate(ctx context.Context, templateID string, tx persistence.Tx) ([]persistence.TemplateTagRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+templateTagCols+` FROM rimsky_template_tags
		 WHERE template_id = ?
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
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor any
	if pag.Cursor != "" {
		cursor = pag.Cursor
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+templateTagCols+`
		 FROM rimsky_template_tags
		 WHERE (? IS NULL OR tag > ?)
		 ORDER BY tag ASC
		 LIMIT ?`,
		cursor, cursor, limit,
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

func (s *templateTagsImpl) Delete(ctx context.Context, tag string, tx persistence.Tx) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_template_tags WHERE tag = ?`,
		tag,
	)
	if err != nil {
		return false, fmt.Errorf("template_tags.delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("template_tags.delete: rows-affected: %w", err)
	}
	return n > 0, nil
}

func (s *templateTagsImpl) CountByTemplate(ctx context.Context, templateID string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_template_tags WHERE template_id = ?`,
		templateID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("template_tags.countByTemplate: %w", err)
	}
	return n, nil
}

func scanTemplateTag(sc scannable) (persistence.TemplateTagRow, error) {
	var (
		tag          string
		templateID   string
		updatedAtStr string
	)
	if err := sc.Scan(&tag, &templateID, &updatedAtStr); err != nil {
		return persistence.TemplateTagRow{}, err
	}
	updatedAt, err := parseTime(updatedAtStr)
	if err != nil {
		return persistence.TemplateTagRow{}, err
	}
	return persistence.TemplateTagRow{
		Tag:        tag,
		TemplateID: templateID,
		UpdatedAt:  updatedAt,
	}, nil
}
