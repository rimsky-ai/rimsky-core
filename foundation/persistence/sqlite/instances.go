// instances.go — SQLite-backed persistence.InstanceStore.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

var errInstanceIDRequired = errors.New("instances.create: ID is required (zero UUID rejected)")

const instanceCols = `id, template_hash, instance_key, params, created_at, terminated_at`

func (s *instancesImpl) Create(ctx context.Context, in persistence.InstanceCreateInput, tx persistence.Tx) (persistence.InstanceRow, error) {
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

	row := s.q(tx).QueryRowContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 RETURNING `+instanceCols,
		in.ID.String(), in.TemplateHash, in.InstanceKey, string(paramsBytes), nowUTC(),
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
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances WHERE id = ?`, id.String(),
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.get: %w", err)
	}
	return &out, nil
}

// FindAnyByInstanceKey resolves an instance by instance_key alone.
// Used by the control-api's idOrKey resolver where the template hash
// is not part of the URL.
func (s *instancesImpl) FindAnyByInstanceKey(ctx context.Context, instanceKey string, tx persistence.Tx) (*persistence.InstanceRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances
		 WHERE instance_key = ?
		 ORDER BY created_at DESC
		 LIMIT 1`, instanceKey,
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.findAnyByInstanceKey: %w", err)
	}
	return &out, nil
}

func (s *instancesImpl) GetByInstanceKey(ctx context.Context, templateHash string, instanceKey string, tx persistence.Tx) (*persistence.InstanceRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances
		 WHERE template_hash = ? AND instance_key = ?`,
		templateHash, instanceKey,
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor any
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.InstanceRow]{}, fmt.Errorf("instances.list: bad cursor: %w", err)
		}
		cursor = u.String()
	}
	var tmplHash any
	if filter.TemplateHash != "" {
		tmplHash = filter.TemplateHash
	}
	// Active filter: nil → no filter; true → terminated_at IS NULL;
	// false → terminated_at IS NOT NULL.
	var activeArg any
	if filter.Active != nil {
		if *filter.Active {
			activeArg = 1
		} else {
			activeArg = 0
		}
	}

	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+instanceCols+`
		 FROM rimsky_instances
		 WHERE (? IS NULL OR template_hash = ?)
		   AND (
		     ? IS NULL
		     OR (? = 1 AND terminated_at IS NULL)
		     OR (? = 0 AND terminated_at IS NOT NULL)
		   )
		   AND (
		     ? IS NULL
		     OR (created_at, id) < (
		       (SELECT created_at FROM rimsky_instances WHERE id = ?),
		       ?
		     )
		   )
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		tmplHash, tmplHash, activeArg, activeArg, activeArg, cursor, cursor, cursor, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.InstanceRow]{}, fmt.Errorf("instances.list: %w", err)
	}
	defer rows.Close()

	var out []persistence.InstanceRow
	for rows.Next() {
		r, err := scanInstance(rows)
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
	_, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_instances WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

func (s *instancesImpl) MarkTerminated(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_instances SET terminated_at = ?
		 WHERE id = ? AND terminated_at IS NULL`,
		nowUTC(), id.String(),
	)
	if err != nil {
		return fmt.Errorf("instances.markTerminated: %w", err)
	}
	return nil
}

func (s *instancesImpl) CountActiveByTemplate(ctx context.Context, templateHash string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_instances
		 WHERE template_hash = ? AND terminated_at IS NULL`,
		templateHash,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("instances.countActiveByTemplate: %w", err)
	}
	return n, nil
}

// CountByActive returns (active, terminated) instance counts.
func (s *instancesImpl) CountByActive(ctx context.Context, tx persistence.Tx) (int, int, error) {
	var active, terminated int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT
		   COUNT(CASE WHEN terminated_at IS NULL THEN 1 END),
		   COUNT(CASE WHEN terminated_at IS NOT NULL THEN 1 END)
		 FROM rimsky_instances`,
	).Scan(&active, &terminated)
	if err != nil {
		return 0, 0, fmt.Errorf("instances.countByActive: %w", err)
	}
	return active, terminated, nil
}

func (s *instancesImpl) ListTerminatedWithLifecycleRows(ctx context.Context, limit int, tx persistence.Tx) ([]persistence.InstanceRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+instanceCols+`
		 FROM rimsky_instances i
		 WHERE i.terminated_at IS NOT NULL
		   AND EXISTS (
		     SELECT 1 FROM rimsky_lifecycle_idempotency l
		     WHERE l.scope_kind = 'instance' AND l.scope_id = i.id
		   )
		 ORDER BY i.terminated_at ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("instances.listTerminatedWithLifecycleRows: %w", err)
	}
	defer rows.Close()

	var out []persistence.InstanceRow
	for rows.Next() {
		r, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanInstance(sc scannable) (persistence.InstanceRow, error) {
	var (
		idStr           string
		templateHash    string
		instanceKey     sql.NullString
		paramsStr       string
		createdAtStr    string
		terminatedAtStr sql.NullString
	)
	if err := sc.Scan(&idStr, &templateHash, &instanceKey, &paramsStr, &createdAtStr, &terminatedAtStr); err != nil {
		return persistence.InstanceRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.InstanceRow{}, fmt.Errorf("scanInstance: bad id %q: %w", idStr, err)
	}
	m := map[string]any{}
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &m); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal params: %w", err)
		}
	}
	createdAt, err := parseTime(createdAtStr)
	if err != nil {
		return persistence.InstanceRow{}, err
	}
	out := persistence.InstanceRow{
		ID:           id,
		TemplateHash: templateHash,
		Params:       m,
		CreatedAt:    createdAt,
	}
	if instanceKey.Valid {
		k := instanceKey.String
		out.InstanceKey = &k
	}
	if terminatedAtStr.Valid {
		t, err := parseTime(terminatedAtStr.String)
		if err != nil {
			return persistence.InstanceRow{}, err
		}
		out.TerminatedAt = &t
	}
	return out, nil
}
