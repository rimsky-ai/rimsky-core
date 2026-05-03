// TemplateStore — Postgres-backed persistence.TemplateStore. Per docs/specs/
// 2026-05-01-control-plane-and-store-lifecycle-design.md §1.2:
// rimsky_templates.id is the content hash ("sha256-<64-hex>"); state
// has three persisted values (registered, deployed, undeployed) —
// deregistered is the absent state, i.e., row deleted. Tags are
// separate (rimsky_template_tags).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// TemplateStore is the Postgres-backed persistence.TemplateStore.

const templateCols = `id, spec, state, registered_at, source`

// Insert writes a new template row. Returns ErrTemplateInUse on
// duplicate id (caller should treat as idempotent re-register and
// short-circuit per spec §1.5).
func (s *templatesImpl) Insert(ctx context.Context, in persistence.TemplateInsertInput, tx persistence.Tx) error {
	ex := s.q(tx)
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
	_, err = ex.Exec(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source)
		 VALUES ($1, $2, $3, $4)`,
		in.ID, specBytes, string(state), source,
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

// GetByHash returns a TemplateRow or nil when not found.
func (s *templatesImpl) GetByHash(ctx context.Context, hash string, tx persistence.Tx) (*persistence.TemplateRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+templateCols+` FROM rimsky_templates WHERE id = $1`, hash,
	)
	out, err := scanTemplate(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates.getByHash: %w", err)
	}
	return &out, nil
}

// LockForUpdate runs SELECT … FOR UPDATE on the template row. Used by
// state-transition handlers to serialize against concurrent transitions.
func (s *templatesImpl) LockForUpdate(ctx context.Context, hash string, tx persistence.Tx) (*persistence.TemplateRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+templateCols+` FROM rimsky_templates WHERE id = $1 FOR UPDATE`, hash,
	)
	out, err := scanTemplate(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates.lockForUpdate: %w", err)
	}
	return &out, nil
}

// List returns a page of templates ordered (registered_at DESC, id DESC).
func (s *templatesImpl) List(
	ctx context.Context,
	filter persistence.TemplateListFilter,
	pag persistence.ListPagination,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	ex := s.q(tx)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var stateFilter *string
	if filter.State != "" {
		v := string(filter.State)
		stateFilter = &v
	}
	var cursor *string
	if pag.Cursor != "" {
		v := pag.Cursor
		cursor = &v
	}

	rows, err := ex.Query(ctx,
		`SELECT `+templateCols+`
		 FROM rimsky_templates
		 WHERE ($1::text IS NULL OR state = $1)
		   AND (
		     $2::text IS NULL
		     OR (registered_at, id) < (
		       (SELECT registered_at FROM rimsky_templates WHERE id = $2),
		       $2
		     )
		   )
		 ORDER BY registered_at DESC, id DESC
		 LIMIT $3`,
		stateFilter, cursor, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.TemplateRow]{}, fmt.Errorf("templates.list: %w", err)
	}
	defer rows.Close()

	var out []persistence.TemplateRow
	for rows.Next() {
		r, err := scanTemplateRows(rows)
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

// UpdateState updates rimsky_templates.state for the given hash.
func (s *templatesImpl) UpdateState(ctx context.Context, hash string, newState persistence.TemplateState, tx persistence.Tx) error {
	ex := s.q(tx)
	tag, err := ex.Exec(ctx,
		`UPDATE rimsky_templates SET state = $2 WHERE id = $1`,
		hash, string(newState),
	)
	if err != nil {
		return fmt.Errorf("templates.updateState: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.Wrap(shared.ErrTemplateNotFound, "template not found",
			map[string]any{"id": hash})
	}
	return nil
}

// DeleteByHash removes the template row. The schema enforces ON DELETE
// RESTRICT against tags and instances; if either still references the
// template, this returns the underlying Postgres FK violation. Callers
// should pre-check those constraints and produce a more useful error.
//
// Per spec §1.6: rimsky_store_lifecycle rows for the (template-scope,
// hash) tuple are cleaned up here transactionally so a follow-on
// re-register starts with empty bookkeeping. The caller's tx (when
// supplied) participates in the same atomic step.
func (s *templatesImpl) DeleteByHash(ctx context.Context, hash string, tx persistence.Tx) error {
	ex := s.q(tx)
	if _, err := ex.Exec(ctx,
		`DELETE FROM rimsky_store_lifecycle
		 WHERE scope_kind = 'template' AND scope_id = $1`, hash); err != nil {
		return fmt.Errorf("templates.deleteByHash: cleanup lifecycle rows: %w", err)
	}
	tag, err := ex.Exec(ctx, `DELETE FROM rimsky_templates WHERE id = $1`, hash)
	if err != nil {
		if isFKViolation(err) {
			return shared.Wrap(shared.ErrTemplateInUse,
				"template has active references (tag or instance)",
				map[string]any{"id": hash})
		}
		return fmt.Errorf("templates.deleteByHash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.Wrap(shared.ErrTemplateNotFound, "template not found",
			map[string]any{"id": hash})
	}
	return nil
}

func scanTemplate(sc scannable) (persistence.TemplateRow, error) {
	var (
		id           string
		specBytes    []byte
		stateStr     string
		registeredAt time.Time
		source       string
	)
	if err := sc.Scan(&id, &specBytes, &stateStr, &registeredAt, &source); err != nil {
		return persistence.TemplateRow{}, err
	}
	var spec nodepkg.TemplateSpec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return persistence.TemplateRow{}, fmt.Errorf("unmarshal spec: %w", err)
	}
	return persistence.TemplateRow{
		ID:           id,
		Spec:         spec,
		State:        persistence.TemplateState(stateStr),
		RegisteredAt: registeredAt,
		Source:       source,
	}, nil
}

func scanTemplateRows(rows pgx.Rows) (persistence.TemplateRow, error) {
	return scanTemplate(rows)
}
