// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// templates.go — SQLite-backed persistence.TemplateTable. Mirrors
// foundation/persistence/postgres/templates.go method-for-method with SQLite
// dialect translations per spec §6.3.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

const templateCols = `id, spec, state, registered_at, source`

func (s *templatesImpl) Insert(ctx context.Context, in persistence.TemplateInsertInput, tx persistence.Tx) error {
	specBytes, err := json.Marshal(in.Spec)
	if err != nil {
		return fmt.Errorf("templates.insert: marshal spec: %w", err)
	}
	source := in.Source
	if source == "" {
		source = "direct"
	}
	state := in.State
	if state == "" {
		state = persistence.TemplateStateRegistered
	}
	_, err = s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, registered_at, source)
		 VALUES (?, ?, ?, ?, ?)`,
		in.ID, string(specBytes), string(state), nowUTC(), source,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return shared.Wrap(shared.ErrTemplateInUse, "template already registered",
				map[string]any{"id": in.ID})
		}
		return fmt.Errorf("templates.insert: %w", err)
	}
	return nil
}

func (s *templatesImpl) GetByHash(ctx context.Context, hash string, tx persistence.Tx) (*persistence.TemplateRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+templateCols+` FROM rimsky_templates WHERE id = ?`, hash,
	)
	out, err := scanTemplate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates.getByHash: %w", err)
	}
	return &out, nil
}

// LockForUpdate omits FOR UPDATE under SQLite; the surrounding
// BEGIN IMMEDIATE writer-slot hold subsumes per-row locking.
func (s *templatesImpl) LockForUpdate(ctx context.Context, hash string, tx persistence.Tx) (*persistence.TemplateRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+templateCols+` FROM rimsky_templates WHERE id = ?`, hash,
	)
	out, err := scanTemplate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates.lockForUpdate: %w", err)
	}
	return &out, nil
}

func (s *templatesImpl) List(
	ctx context.Context,
	filter persistence.TemplateListFilter,
	pag persistence.ListPagination,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var stateFilter any
	if filter.State != "" {
		stateFilter = string(filter.State)
	}
	var cursor any
	if pag.Cursor != "" {
		cursor = pag.Cursor
	}
	var tagFilter any
	if filter.Tag != "" {
		tagFilter = filter.Tag
	}

	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+templateCols+`
		 FROM rimsky_templates t
		 WHERE (? IS NULL OR state = ?)
		   AND (
		     ? IS NULL
		     OR (registered_at, id) < (
		       (SELECT registered_at FROM rimsky_templates WHERE id = ?),
		       ?
		     )
		   )
		   AND (
		     ? IS NULL
		     OR EXISTS (
		       SELECT 1 FROM rimsky_template_tags tt
		       WHERE tt.template_id = t.id AND tt.tag = ?
		     )
		   )
		 ORDER BY registered_at DESC, id DESC
		 LIMIT ?`,
		stateFilter, stateFilter, cursor, cursor, cursor, tagFilter, tagFilter, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.TemplateRow]{}, fmt.Errorf("templates.list: %w", err)
	}
	defer rows.Close()

	var out []persistence.TemplateRow
	for rows.Next() {
		r, err := scanTemplate(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.TemplateRow]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.TemplateRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID
	}
	return persistence.PaginatedListResult[persistence.TemplateRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (s *templatesImpl) UpdateState(ctx context.Context, hash string, newState persistence.TemplateState, tx persistence.Tx) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_templates SET state = ? WHERE id = ?`,
		string(newState), hash,
	)
	if err != nil {
		return fmt.Errorf("templates.updateState: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("templates.updateState: rows-affected: %w", err)
	}
	if n == 0 {
		return shared.Wrap(shared.ErrTemplateNotFound, "template not found",
			map[string]any{"id": hash})
	}
	return nil
}

func (s *templatesImpl) DeleteByHash(ctx context.Context, hash string, tx persistence.Tx) error {
	if _, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_idempotencies
		 WHERE scope_kind = 'template' AND scope_id = ?`, hash); err != nil {
		return fmt.Errorf("templates.deleteByHash: cleanup lifecycle rows: %w", err)
	}
	res, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_templates WHERE id = ?`, hash)
	if err != nil {
		if isFKViolation(err) {
			return shared.Wrap(shared.ErrTemplateInUse,
				"template has active references (tag or instance)",
				map[string]any{"id": hash})
		}
		return fmt.Errorf("templates.deleteByHash: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("templates.deleteByHash: rows-affected: %w", err)
	}
	if n == 0 {
		return shared.Wrap(shared.ErrTemplateNotFound, "template not found",
			map[string]any{"id": hash})
	}
	return nil
}

func scanTemplate(sc scannable) (persistence.TemplateRow, error) {
	var (
		id              string
		specStr         string
		stateStr        string
		registeredAtStr string
		source          string
	)
	if err := sc.Scan(&id, &specStr, &stateStr, &registeredAtStr, &source); err != nil {
		return persistence.TemplateRow{}, err
	}
	var spec spec.TemplateSpec
	if err := json.Unmarshal([]byte(specStr), &spec); err != nil {
		return persistence.TemplateRow{}, fmt.Errorf("unmarshal spec: %w", err)
	}
	registeredAt, err := parseTime(registeredAtStr)
	if err != nil {
		return persistence.TemplateRow{}, err
	}
	return persistence.TemplateRow{
		ID:           id,
		Spec:         spec,
		State:        persistence.TemplateState(stateStr),
		RegisteredAt: registeredAt,
		Source:       source,
	}, nil
}
