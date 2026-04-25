// TemplateStore — port of rimsky/src/storage/postgres/template-store.ts.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// TemplateStore is the Postgres-backed storage.TemplateStore.
type TemplateStore struct {
	pool *pgxpool.Pool
}

var _ storage.TemplateStore = (*TemplateStore)(nil)

// Deploy inserts a template row; on (name, version) conflict the existing
// spec is compared canonical-JSON-equal, and the existing row is returned
// if identical (idempotent re-deploy). Divergent re-deploy returns
// ErrTemplateInUse.
func (s *TemplateStore) Deploy(ctx context.Context, spec nodepkg.TemplateSpec, tx storage.Tx) (storage.TemplateSummary, error) {
	ex := q(tx, s.pool)
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return storage.TemplateSummary{}, fmt.Errorf("templates.deploy: marshal spec: %w", err)
	}

	var (
		id         shared.UUID
		name       string
		version    string
		deployedAt time.Time
	)
	err = ex.QueryRow(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec)
		 VALUES (gen_random_uuid(), $1, $2, $3)
		 ON CONFLICT (name, version) DO NOTHING
		 RETURNING id, name, version, deployed_at`,
		spec.Name, spec.Version, specBytes,
	).Scan(&id, &name, &version, &deployedAt)
	if err == nil {
		return storage.TemplateSummary{
			ID: id, Name: name, Version: version, DeployedAt: deployedAt,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return storage.TemplateSummary{}, fmt.Errorf("templates.deploy: insert: %w", err)
	}

	// Conflict — compare specs.
	var existingSpec []byte
	if err := ex.QueryRow(ctx,
		`SELECT id, name, version, spec, deployed_at FROM rimsky_templates
		 WHERE name = $1 AND version = $2`,
		spec.Name, spec.Version,
	).Scan(&id, &name, &version, &existingSpec, &deployedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.TemplateSummary{}, shared.Wrap(shared.ErrTemplateInUse,
				"template deploy race", map[string]any{
					"name": spec.Name, "version": spec.Version,
				})
		}
		return storage.TemplateSummary{}, fmt.Errorf("templates.deploy: select conflict: %w", err)
	}
	if !sameSpec(existingSpec, specBytes) {
		return storage.TemplateSummary{}, shared.Wrap(shared.ErrTemplateInUse,
			"template (name, version) already deployed with a different spec; bump version to change",
			map[string]any{"name": spec.Name, "version": spec.Version})
	}
	return storage.TemplateSummary{
		ID: id, Name: name, Version: version, DeployedAt: deployedAt,
	}, nil
}

// Get returns a TemplateRow (summary + spec) or nil when not found.
func (s *TemplateStore) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.TemplateRow, error) {
	ex := q(tx, s.pool)
	var (
		outID      shared.UUID
		name       string
		version    string
		specBytes  []byte
		deployedAt time.Time
	)
	err := ex.QueryRow(ctx,
		`SELECT id, name, version, spec, deployed_at FROM rimsky_templates WHERE id = $1`,
		id,
	).Scan(&outID, &name, &version, &specBytes, &deployedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates.get: %w", err)
	}
	var spec nodepkg.TemplateSpec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return nil, fmt.Errorf("templates.get: unmarshal spec: %w", err)
	}
	return &storage.TemplateRow{
		TemplateSummary: storage.TemplateSummary{
			ID: outID, Name: name, Version: version, DeployedAt: deployedAt,
		},
		Spec: spec,
	}, nil
}

// List returns a page of template summaries ordered (deployed_at DESC, id DESC).
func (s *TemplateStore) List(
	ctx context.Context,
	filter storage.TemplateListFilter,
	pag storage.ListPagination,
	tx storage.Tx,
) (storage.PaginatedListResult[storage.TemplateSummary], error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *shared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return storage.PaginatedListResult[storage.TemplateSummary]{}, fmt.Errorf("templates.list: bad cursor: %w", err)
		}
		cursor = &u
	}
	var nameFilter *string
	if filter.Name != "" {
		nameFilter = &filter.Name
	}

	rows, err := ex.Query(ctx,
		`SELECT id, name, version, deployed_at
		 FROM rimsky_templates
		 WHERE ($1::text IS NULL OR name = $1)
		   AND (
		     $2::uuid IS NULL
		     OR (deployed_at, id) < (
		       (SELECT deployed_at FROM rimsky_templates WHERE id = $2::uuid),
		       $2::uuid
		     )
		   )
		 ORDER BY deployed_at DESC, id DESC
		 LIMIT $3`,
		nameFilter, cursor, limit,
	)
	if err != nil {
		return storage.PaginatedListResult[storage.TemplateSummary]{}, fmt.Errorf("templates.list: %w", err)
	}
	defer rows.Close()

	var out []storage.TemplateSummary
	for rows.Next() {
		var r storage.TemplateSummary
		if err := rows.Scan(&r.ID, &r.Name, &r.Version, &r.DeployedAt); err != nil {
			return storage.PaginatedListResult[storage.TemplateSummary]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return storage.PaginatedListResult[storage.TemplateSummary]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return storage.PaginatedListResult[storage.TemplateSummary]{Rows: out, NextCursor: nextCursor}, nil
}

// Delete removes a template. Returns ErrTemplateInUse if any instance
// references it; ErrTemplateNotFound if the id doesn't exist.
func (s *TemplateStore) Delete(ctx context.Context, id shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	var one int
	err := ex.QueryRow(ctx,
		`SELECT 1 FROM rimsky_instances WHERE template_id = $1 LIMIT 1`, id,
	).Scan(&one)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("templates.delete: check in-use: %w", err)
	}
	if err == nil {
		return shared.Wrap(shared.ErrTemplateInUse, "template has active instances",
			map[string]any{"id": id})
	}
	tag, err := ex.Exec(ctx, `DELETE FROM rimsky_templates WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("templates.delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.Wrap(shared.ErrTemplateNotFound, "template not found",
			map[string]any{"id": id})
	}
	return nil
}

// ---- helpers shared by this package ----

func sameSpec(a, b []byte) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	return canonicalJSON(va) == canonicalJSON(vb)
}

func canonicalJSON(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b := make([]byte, 0, 64)
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			kb, _ := json.Marshal(k)
			b = append(b, kb...)
			b = append(b, ':')
			b = append(b, canonicalJSON(x[k])...)
		}
		b = append(b, '}')
		return string(b)
	case []any:
		b := make([]byte, 0, 64)
		b = append(b, '[')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, canonicalJSON(e)...)
		}
		b = append(b, ']')
		return string(b)
	default:
		bs, _ := json.Marshal(v)
		return string(bs)
	}
}
