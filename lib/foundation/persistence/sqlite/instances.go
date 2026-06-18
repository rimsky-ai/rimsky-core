// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var errInstanceIDRequired = errors.New("instances.create: ID is required (zero UUID rejected)")

const instanceCols = `id, template_hash, instance_key, params, attribute_overrides, created_at, terminated_at, attribute_overrides_match_counts, main_run_scope_id, paused, terminate_after_run, service_bindings, created_by_api_key_id`

func (s *instancesImpl) Create(ctx context.Context, in persistence.InstanceCreateInput, tx persistence.Tx) (persistence.InstanceRow, error) {
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	paramsBytes, err := json.Marshal(in.Params)
	if err != nil {
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: marshal params: %w", err)
	}
	if in.AttributeOverrides == nil {
		in.AttributeOverrides = map[string]any{}
	}
	overridesBytes, err := json.Marshal(in.AttributeOverrides)
	if err != nil {
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: marshal attribute_overrides: %w", err)
	}
	if in.AttributeOverridesMatchCounts == nil {
		in.AttributeOverridesMatchCounts = []int64{}
	}
	matchCountsBytes, err := json.Marshal(in.AttributeOverridesMatchCounts)
	if err != nil {
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: marshal attribute_overrides_match_counts: %w", err)
	}
	if in.ID == (foundationshared.UUID{}) {
		return persistence.InstanceRow{}, errInstanceIDRequired
	}

	pausedArg := 0
	if in.Paused {
		pausedArg = 1
	}
	terminateAfterRunArg := 0
	if in.TerminateAfterRun {
		terminateAfterRunArg = 1
	}
	var serviceBindingsArg any
	if len(in.ServiceBindings) > 0 {
		serviceBindingsArg = string(in.ServiceBindings)
	}
	var createdByAPIKeyArg any
	if in.CreatedByAPIKeyID != nil {
		createdByAPIKeyArg = in.CreatedByAPIKeyID.String()
	}
	row := s.q(tx).QueryRowContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, attribute_overrides, created_at, attribute_overrides_match_counts, main_run_scope_id, paused, terminate_after_run, service_bindings, created_by_api_key_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING `+instanceCols,
		in.ID.String(), in.TemplateHash, in.InstanceKey, string(paramsBytes), string(overridesBytes), nowUTC(), string(matchCountsBytes), in.MainRunScopeID.String(), pausedArg, terminateAfterRunArg, serviceBindingsArg, createdByAPIKeyArg,
	)
	out, err := scanInstance(row)
	if err != nil {
		if isUniqueViolation(err) {
			return persistence.InstanceRow{}, foundationshared.Wrap(foundationshared.ErrInstanceKeyConflict,
				"instance_key already registered for template",
				map[string]any{"template_hash": in.TemplateHash, "instance_key": in.InstanceKey})
		}
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: %w", err)
	}
	return out, nil
}

func (s *instancesImpl) Get(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*persistence.InstanceRow, error) {
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

func (s *instancesImpl) Delete(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	if _, err := s.q(tx).ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("instances.delete: defer_foreign_keys: %w", err)
	}
	_, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_instances WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

func (s *instancesImpl) MarkTerminated(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
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

// @concept: attribute (L5 matcher overlay)
func (s *instancesImpl) IncrementAttributeOverrideMatchCounts(
	ctx context.Context,
	instanceID foundationshared.UUID,
	indices []int,
	tx persistence.Tx,
) error {
	if len(indices) == 0 {
		return nil
	}
	q := s.q(tx)
	var arrLen sql.NullInt64
	if err := q.QueryRowContext(ctx,
		`SELECT json_array_length(attribute_overrides_match_counts)
		   FROM rimsky_instances WHERE id = ?`,
		instanceID.String(),
	).Scan(&arrLen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("instances.incrementAttributeOverrideMatchCounts: %w", err)
	}
	maxIdx := int(arrLen.Int64)
	deltas := map[int]int{}
	order := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= maxIdx {
			continue
		}
		if _, seen := deltas[idx]; !seen {
			order = append(order, idx)
		}
		deltas[idx]++
	}
	if len(order) == 0 {
		return nil
	}
	for _, idx := range order {
		path := fmt.Sprintf("$[%d]", idx)
		query := fmt.Sprintf(
			`UPDATE rimsky_instances
			   SET attribute_overrides_match_counts = json_set(
			       attribute_overrides_match_counts,
			       '%s',
			       coalesce(json_extract(attribute_overrides_match_counts, '%s'), 0) + %d
			   )
			 WHERE id = ?`,
			path, path, deltas[idx],
		)
		if _, err := q.ExecContext(ctx, query, instanceID.String()); err != nil {
			return fmt.Errorf("instances.incrementAttributeOverrideMatchCounts: %w", err)
		}
	}
	return nil
}

// @concept: breakpoint
func (s *instancesImpl) SetPaused(ctx context.Context, instanceID foundationshared.UUID, paused bool, tx persistence.Tx) (bool, error) {
	q := s.q(tx)
	var prior int64
	if err := q.QueryRowContext(ctx,
		`SELECT paused FROM rimsky_instances WHERE id = ?`,
		instanceID.String(),
	).Scan(&prior); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, foundationshared.ErrInstanceNotFound
		}
		return false, fmt.Errorf("instances.setPaused.select: %w", err)
	}
	priorBool := prior != 0
	if priorBool == paused {
		return priorBool, nil
	}
	pausedArg := 0
	if paused {
		pausedArg = 1
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE rimsky_instances SET paused = ? WHERE id = ?`,
		pausedArg, instanceID.String(),
	); err != nil {
		return false, fmt.Errorf("instances.setPaused.update: %w", err)
	}
	return priorBool, nil
}

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
		     SELECT 1 FROM rimsky_lifecycle_idempotencies l
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
		idStr                string
		templateHash         string
		instanceKey          sql.NullString
		paramsStr            string
		overridesStr         string
		createdAtStr         string
		terminatedAtStr      sql.NullString
		matchCountsStr       string
		mainRunScopeIDStr    string
		pausedInt            int64
		terminateAfterRunInt int64
		serviceBindingsStr   sql.NullString
		createdByAPIKeyIDStr sql.NullString
	)
	if err := sc.Scan(&idStr, &templateHash, &instanceKey, &paramsStr, &overridesStr, &createdAtStr, &terminatedAtStr, &matchCountsStr, &mainRunScopeIDStr, &pausedInt, &terminateAfterRunInt, &serviceBindingsStr, &createdByAPIKeyIDStr); err != nil {
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
	overrides := map[string]any{}
	if overridesStr != "" {
		if err := json.Unmarshal([]byte(overridesStr), &overrides); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides: %w", err)
		}
	}
	mc := []int64{}
	if matchCountsStr != "" {
		if err := json.Unmarshal([]byte(matchCountsStr), &mc); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides_match_counts: %w", err)
		}
	}
	createdAt, err := parseTime(createdAtStr)
	if err != nil {
		return persistence.InstanceRow{}, err
	}
	var mainRunScopeID foundationshared.UUID
	if mainRunScopeIDStr != "" {
		parsed, err := uuid.Parse(mainRunScopeIDStr)
		if err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("scanInstance: bad main_run_scope_id %q: %w", mainRunScopeIDStr, err)
		}
		mainRunScopeID = parsed
	}
	out := persistence.InstanceRow{
		ID:                            id,
		TemplateHash:                  templateHash,
		Params:                        m,
		AttributeOverrides:            overrides,
		AttributeOverridesMatchCounts: mc,
		MainRunScopeID:                mainRunScopeID,
		CreatedAt:                     createdAt,
		Paused:                        pausedInt != 0,
		TerminateAfterRun:             terminateAfterRunInt != 0,
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
	if serviceBindingsStr.Valid && serviceBindingsStr.String != "" {
		out.ServiceBindings = json.RawMessage(serviceBindingsStr.String)
	}
	if createdByAPIKeyIDStr.Valid && createdByAPIKeyIDStr.String != "" {
		parsed, err := uuid.Parse(createdByAPIKeyIDStr.String)
		if err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("scanInstance: bad created_by_api_key_id %q: %w", createdByAPIKeyIDStr.String, err)
		}
		out.CreatedByAPIKeyID = &parsed
	}
	return out, nil
}
