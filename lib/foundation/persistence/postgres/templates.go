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

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const templateCols = `id, spec, state, registered_at, source`

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
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO NOTHING`,
		in.ID, specBytes, string(state), source,
	)
	if err != nil {
		return fmt.Errorf("templates.insert: %w", err)
	}
	return nil
}

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
	var cursorRegisteredAt any
	var cursorID any
	if pag.Cursor != "" {
		registeredAt, id, err := decodeTemplateCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.TemplateRow]{}, persistence.ErrInvalidCursor
		}
		cursorRegisteredAt = registeredAt
		cursorID = id
	}
	var tagFilter *string
	if filter.Tag != "" {
		v := filter.Tag
		tagFilter = &v
	}

	rows, err := ex.Query(ctx,
		`SELECT `+templateCols+`
		 FROM rimsky_templates t
		 WHERE ($1::text IS NULL OR state = $1)
		   AND (
		     $2::timestamptz IS NULL
		     OR (registered_at, id) < ($2, $5::text)
		   )
		   AND (
		     $4::text IS NULL
		     OR EXISTS (
		       SELECT 1 FROM rimsky_template_tags tt
		       WHERE tt.template_id = t.id AND tt.tag = $4
		     )
		   )
		 ORDER BY registered_at DESC, id DESC
		 LIMIT $3`,
		stateFilter, cursorRegisteredAt, limit, tagFilter, cursorID,
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
		last := out[len(out)-1]
		nextCursor = encodeTemplateCursor(last.RegisteredAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.TemplateRow]{Rows: out, NextCursor: nextCursor}, nil
}

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

func (s *templatesImpl) DeleteByHash(ctx context.Context, hash string, tx persistence.Tx) error {
	ex := s.q(tx)
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
	var spec spec.TemplateSpec
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

type templateCursor struct {
	R time.Time `json:"r"`
	I string    `json:"i"`
}

func encodeTemplateCursor(registeredAt time.Time, id string) string {
	b, _ := json.Marshal(templateCursor{R: registeredAt, I: id})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeTemplateCursor(s string) (time.Time, string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	var c templateCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, "", err
	}
	return c.R, c.I, nil
}
