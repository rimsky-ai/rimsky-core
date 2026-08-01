// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var errInstanceIDRequired = errors.New("instances.create: ID is required (zero UUID rejected)")

var errInstanceIDConflict = errors.New("instances.create: instance id already exists")

const instanceCols = `id, template_hash, instance_key, params, attribute_overrides, created_at, terminated_at, paused, service_bindings, created_by_api_key_id, target_routing_identity, message_queue_mode`

func (s *instancesImpl) Create(ctx context.Context, in persistence.InstanceCreateInput, tx persistence.Tx) (persistence.InstanceRow, error) {
	ex := s.q(tx)
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
	if in.ID == (foundationshared.UUID{}) {
		return persistence.InstanceRow{}, errInstanceIDRequired
	}
	id := in.ID
	var serviceBindings []byte
	if len(in.ServiceBindings) > 0 {
		serviceBindings = in.ServiceBindings
	}
	messageQueueMode := in.MessageQueueMode
	if messageQueueMode == "" {
		messageQueueMode = "backlog"
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, attribute_overrides, paused, service_bindings, created_by_api_key_id, target_routing_identity, message_queue_mode)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING `+instanceCols,
		id, in.TemplateHash, in.InstanceKey, paramsBytes, overridesBytes, in.Paused, serviceBindings, in.CreatedByAPIKeyID, in.TargetRoutingIdentity, messageQueueMode,
	)
	out, err := scanInstance(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "rimsky_instances_pkey":
				return persistence.InstanceRow{}, errInstanceIDConflict
			case "rimsky_instances_template_hash_instance_key_key":
				return persistence.InstanceRow{}, foundationshared.Wrap(foundationshared.ErrInstanceKeyConflict,
					"instance_key already registered for template",
					map[string]any{"template_hash": in.TemplateHash, "instance_key": in.InstanceKey})
			}
		}
		return persistence.InstanceRow{}, fmt.Errorf("instances.create: %w", err)
	}
	return out, nil
}

func (s *instancesImpl) Get(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*persistence.InstanceRow, error) {
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
	var cursorCreatedAt any
	var cursorID any
	if pag.Cursor != "" {
		createdAt, id, err := decodeInstanceCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.InstanceRow]{}, fmt.Errorf("instances.list: bad cursor: %w", err)
		}
		cursorCreatedAt = createdAt
		cursorID = id
	}
	var tmplHash *string
	if filter.TemplateHash != "" {
		v := filter.TemplateHash
		tmplHash = &v
	}
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
		     $3::timestamptz IS NULL
		     OR (created_at, id) < ($3, $5::uuid)
		   )
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		tmplHash, activeArg, cursorCreatedAt, limit, cursorID,
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
		last := out[len(out)-1]
		nextCursor = encodeInstanceCursor(last.CreatedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.InstanceRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (s *instancesImpl) Delete(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	// @concept: run-scope
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_instances WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

func (s *instancesImpl) MarkTerminated(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
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

// @concept: instance
func (s *instancesImpl) SetPaused(ctx context.Context, instanceID foundationshared.UUID, paused bool, tx persistence.Tx) (bool, error) {
	ex := s.q(tx)
	var prior bool
	if err := ex.QueryRow(ctx,
		`SELECT paused FROM rimsky_instances WHERE id = $1 FOR UPDATE`,
		instanceID,
	).Scan(&prior); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, foundationshared.ErrInstanceNotFound
		}
		return false, fmt.Errorf("instances.setPaused.select: %w", err)
	}
	if prior == paused {
		return prior, nil
	}
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_instances SET paused = $2 WHERE id = $1`,
		instanceID, paused,
	); err != nil {
		return false, fmt.Errorf("instances.setPaused.update: %w", err)
	}
	return prior, nil
}

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
		     SELECT 1 FROM rimsky_lifecycle_idempotencies l
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

type scannable interface {
	Scan(dst ...any) error
}

func scanInstance(sc scannable) (persistence.InstanceRow, error) {
	var (
		id                    foundationshared.UUID
		templateHash          string
		instanceKey           *string
		params                []byte
		overrides             []byte
		createdAt             time.Time
		terminatedAt          *time.Time
		paused                bool
		serviceBindingsByte   []byte
		createdByAPIKeyID     *foundationshared.UUID
		targetRoutingIdentity string
		messageQueueMode      string
	)
	if err := sc.Scan(&id, &templateHash, &instanceKey, &params, &overrides, &createdAt, &terminatedAt, &paused, &serviceBindingsByte, &createdByAPIKeyID, &targetRoutingIdentity, &messageQueueMode); err != nil {
		return persistence.InstanceRow{}, err
	}
	m := map[string]any{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal params: %w", err)
		}
	}
	ov := map[string]any{}
	if len(overrides) > 0 {
		if err := json.Unmarshal(overrides, &ov); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides: %w", err)
		}
	}
	var serviceBindings json.RawMessage
	if len(serviceBindingsByte) > 0 {
		serviceBindings = json.RawMessage(serviceBindingsByte)
	}
	return persistence.InstanceRow{
		ID:                    id,
		TemplateHash:          templateHash,
		InstanceKey:           instanceKey,
		Params:                m,
		AttributeOverrides:    ov,
		CreatedAt:             createdAt,
		TerminatedAt:          terminatedAt,
		Paused:                paused,
		ServiceBindings:       serviceBindings,
		CreatedByAPIKeyID:     createdByAPIKeyID,
		TargetRoutingIdentity: targetRoutingIdentity,
		MessageQueueMode:      messageQueueMode,
	}, nil
}

func scanInstanceRows(rows pgx.Rows) (persistence.InstanceRow, error) {
	return scanInstance(rows)
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

type instanceCursor struct {
	C time.Time `json:"c"`
	I string    `json:"i"`
}

func encodeInstanceCursor(createdAt time.Time, id foundationshared.UUID) string {
	b, _ := json.Marshal(instanceCursor{C: createdAt, I: id.String()})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeInstanceCursor(s string) (time.Time, foundationshared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, foundationshared.UUID{}, err
	}
	var c instanceCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, foundationshared.UUID{}, err
	}
	id, err := uuid.Parse(c.I)
	if err != nil {
		return time.Time{}, foundationshared.UUID{}, err
	}
	return c.C, foundationshared.UUID(id), nil
}
