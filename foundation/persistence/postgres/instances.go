// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// InstanceStore — Postgres-backed persistence.InstanceStore. Per docs/specs/
// 2026-05-01-control-plane-and-store-lifecycle-design.md §2:
// rimsky_instances binds to template_hash (TEXT) instead of template_id
// (UUID); consumer_key renamed to instance_key (nullable).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// errInstanceIDRequired is returned by Create when in.ID is the zero UUID.
// Callers must pass a pre-generated UUID (e.g. uuid.New()) so the row's
// identity is established by the caller, not silently filled in by persistence.
var errInstanceIDRequired = errors.New("instances.create: ID is required (zero UUID rejected)")

const instanceCols = `id, template_hash, instance_key, params, created_at, terminated_at`

// Create inserts a new rimsky_instances row. The caller supplies a
// pre-generated UUID. Returns ErrInstanceKeyConflict when (template_hash,
// instance_key) already exists.
func (s *instancesImpl) Create(ctx context.Context, in persistence.InstanceCreateInput, tx persistence.Tx) (persistence.InstanceRow, error) {
	ex := s.q(tx)
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	paramsBytes, err := json.Marshal(in.Params)
	if err != nil {
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: marshal params: %w", err)
	}
	if in.ID == (shared.UUID{}) {
		return persistence.InstanceRow{}, errInstanceIDRequired
	}
	id := in.ID
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+instanceCols,
		id, in.TemplateHash, in.InstanceKey, paramsBytes,
	)
	out, err := scanInstance(row)
	if err != nil {
		if isUniqueViolation(err) {
			return persistence.InstanceRow{}, shared.Wrap(shared.ErrInstanceKeyConflict,
				"instance_key already registered for template",
				map[string]any{"template_hash": in.TemplateHash, "instance_key": in.InstanceKey})
		}
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: %w", err)
	}
	return out, nil
}

func (s *instancesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.InstanceRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances WHERE id = $1`, id)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.get: %w", err)
	}
	return &out, nil
}

// FindAnyByInstanceKey resolves an instance by instance_key alone. The
// (template_hash, instance_key) uniqueness constraint guarantees at
// most one row, but in case of multi-template overlap the most-recent
// row by created_at wins.
func (s *instancesImpl) FindAnyByInstanceKey(ctx context.Context, instanceKey string, tx persistence.Tx) (*persistence.InstanceRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances
		 WHERE instance_key = $1
		 ORDER BY created_at DESC
		 LIMIT 1`,
		instanceKey,
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.findAnyByInstanceKey: %w", err)
	}
	return &out, nil
}

func (s *instancesImpl) GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx persistence.Tx) (*persistence.InstanceRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances
		 WHERE template_hash = $1 AND instance_key = $2`,
		templateHash, instanceKey,
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.getByInstanceKey: %w", err)
	}
	return &out, nil
}

func (s *instancesImpl) List(
	ctx context.Context,
	filter persistence.InstanceListFilter,
	pag persistence.ListPagination,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	ex := s.q(tx)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *shared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.InstanceRow]{}, fmt.Errorf("instances.list: bad cursor: %w", err)
		}
		cursor = &u
	}
	var tmplHash *string
	if filter.TemplateHash != "" {
		v := filter.TemplateHash
		tmplHash = &v
	}
	// Active filter: nil → no filter; true → terminated_at IS NULL;
	// false → terminated_at IS NOT NULL.
	var activeArg any
	if filter.Active != nil {
		activeArg = *filter.Active
	}

	rows, err := ex.Query(ctx,
		`SELECT `+instanceCols+`
		 FROM rimsky_instances
		 WHERE ($1::text IS NULL OR template_hash = $1)
		   AND (
		     $2::boolean IS NULL
		     OR ($2::boolean = true AND terminated_at IS NULL)
		     OR ($2::boolean = false AND terminated_at IS NOT NULL)
		   )
		   AND (
		     $3::uuid IS NULL
		     OR (created_at, id) < (
		       (SELECT created_at FROM rimsky_instances WHERE id = $3::uuid),
		       $3::uuid
		     )
		   )
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		tmplHash, activeArg, cursor, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.InstanceRow]{}, fmt.Errorf("instances.list: %w", err)
	}
	defer rows.Close()

	var out []persistence.InstanceRow
	for rows.Next() {
		r, err := scanInstanceRows(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.InstanceRow]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.InstanceRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return persistence.PaginatedListResult[persistence.InstanceRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (s *instancesImpl) Delete(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_instances WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

// MarkTerminated sets terminated_at = now() if currently NULL. Idempotent
// — repeated calls do not move the timestamp.
func (s *instancesImpl) MarkTerminated(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_instances SET terminated_at = now()
		 WHERE id = $1 AND terminated_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("instances.markTerminated: %w", err)
	}
	return nil
}

// CountActiveByTemplate returns the count of rimsky_instances rows
// where template_hash = $1 AND terminated_at IS NULL.
func (s *instancesImpl) CountActiveByTemplate(ctx context.Context, templateHash string, tx persistence.Tx) (int, error) {
	ex := s.q(tx)
	var n int
	err := ex.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_instances
		 WHERE template_hash = $1 AND terminated_at IS NULL`,
		templateHash,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("instances.countActiveByTemplate: %w", err)
	}
	return n, nil
}

// CountByActive returns (active, terminated) instance counts.
func (s *instancesImpl) CountByActive(ctx context.Context, tx persistence.Tx) (int, int, error) {
	ex := s.q(tx)
	var active, terminated int
	err := ex.QueryRow(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE terminated_at IS NULL),
		   COUNT(*) FILTER (WHERE terminated_at IS NOT NULL)
		 FROM rimsky_instances`,
	).Scan(&active, &terminated)
	if err != nil {
		return 0, 0, fmt.Errorf("instances.countByActive: %w", err)
	}
	return active, terminated, nil
}

// ListTerminatedWithLifecycleRows returns up to limit instances with
// terminated_at IS NOT NULL that still have at least one matching
// rimsky_lifecycle_idempotency row at scope_kind='instance'.
func (s *instancesImpl) ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx persistence.Tx) ([]persistence.InstanceRow, error) {
	ex := s.q(tx)
	if limit <= 0 {
		limit = 100
	}
	rows, err := ex.Query(ctx,
		`SELECT `+instanceCols+`
		 FROM rimsky_instances i
		 WHERE i.terminated_at IS NOT NULL
		   AND EXISTS (
		     SELECT 1 FROM rimsky_lifecycle_idempotency l
		     WHERE l.scope_kind = 'instance' AND l.scope_id = i.id::text
		   )
		 ORDER BY i.terminated_at ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("instances.listTerminatedWithLifecycleRows: %w", err)
	}
	defer rows.Close()

	var out []persistence.InstanceRow
	for rows.Next() {
		r, err := scanInstanceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- helpers ----

// scannable is implemented by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dst ...any) error
}

func scanInstance(sc scannable) (persistence.InstanceRow, error) {
	var (
		id           shared.UUID
		templateHash string
		instanceKey  *string
		params       []byte
		createdAt    time.Time
		terminatedAt *time.Time
	)
	if err := sc.Scan(&id, &templateHash, &instanceKey, &params, &createdAt, &terminatedAt); err != nil {
		return persistence.InstanceRow{}, err
	}
	m := map[string]any{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal params: %w", err)
		}
	}
	return persistence.InstanceRow{
		ID:           id,
		TemplateHash: templateHash,
		InstanceKey:  instanceKey,
		Params:       m,
		CreatedAt:    createdAt,
		TerminatedAt: terminatedAt,
	}, nil
}

func scanInstanceRows(rows pgx.Rows) (persistence.InstanceRow, error) {
	return scanInstance(rows)
}

// isUniqueViolation checks pgconn.PgError SQLSTATE 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// isFKViolation checks pgconn.PgError SQLSTATE 23503.
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
