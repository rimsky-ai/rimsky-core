// InstanceStore — port of rimsky/src/storage/postgres/instance-store.ts.
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type InstanceStore struct {
	pool *pgxpool.Pool
}

var _ storage.InstanceStore = (*InstanceStore)(nil)

const instanceCols = `id, template_id, consumer_key, params, created_at`

func (s *InstanceStore) Create(ctx context.Context, in storage.InstanceCreateInput, tx storage.Tx) (storage.InstanceRow, error) {
	ex := q(tx, s.pool)
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	paramsBytes, err := json.Marshal(in.Params)
	if err != nil {
		return storage.InstanceRow{}, fmt.Errorf("instances.create: marshal params: %w", err)
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params)
		 VALUES (gen_random_uuid(), $1, $2, $3)
		 RETURNING `+instanceCols,
		in.TemplateID, in.ConsumerKey, paramsBytes,
	)
	out, err := scanInstance(row)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.InstanceRow{}, shared.Wrap(shared.ErrConsumerKeyConflict,
				"consumer_key already registered for template",
				map[string]any{"template_id": in.TemplateID, "consumer_key": in.ConsumerKey})
		}
		return storage.InstanceRow{}, fmt.Errorf("instances.create: %w", err)
	}
	return out, nil
}

func (s *InstanceStore) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.InstanceRow, error) {
	ex := q(tx, s.pool)
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

func (s *InstanceStore) GetByConsumerKey(ctx context.Context, templateID shared.UUID, consumerKey string, tx storage.Tx) (*storage.InstanceRow, error) {
	ex := q(tx, s.pool)
	row := ex.QueryRow(ctx,
		`SELECT `+instanceCols+` FROM rimsky_instances
		 WHERE template_id = $1 AND consumer_key = $2`,
		templateID, consumerKey,
	)
	out, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("instances.getByConsumerKey: %w", err)
	}
	return &out, nil
}

func (s *InstanceStore) List(
	ctx context.Context,
	filter storage.InstanceListFilter,
	pag storage.ListPagination,
	tx storage.Tx,
) (storage.PaginatedListResult[storage.InstanceRow], error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *shared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return storage.PaginatedListResult[storage.InstanceRow]{}, fmt.Errorf("instances.list: bad cursor: %w", err)
		}
		cursor = &u
	}
	var tmpl *shared.UUID
	if filter.TemplateID != (shared.UUID{}) {
		tmpl = &filter.TemplateID
	}
	var ckey *string
	if filter.ConsumerKey != "" {
		ckey = &filter.ConsumerKey
	}

	rows, err := ex.Query(ctx,
		`SELECT `+instanceCols+`
		 FROM rimsky_instances
		 WHERE ($1::uuid IS NULL OR template_id = $1)
		   AND ($2::text IS NULL OR consumer_key = $2)
		   AND (
		     $3::uuid IS NULL
		     OR (created_at, id) < (
		       (SELECT created_at FROM rimsky_instances WHERE id = $3::uuid),
		       $3::uuid
		     )
		   )
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		tmpl, ckey, cursor, limit,
	)
	if err != nil {
		return storage.PaginatedListResult[storage.InstanceRow]{}, fmt.Errorf("instances.list: %w", err)
	}
	defer rows.Close()

	var out []storage.InstanceRow
	for rows.Next() {
		r, err := scanInstanceRows(rows)
		if err != nil {
			return storage.PaginatedListResult[storage.InstanceRow]{}, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return storage.PaginatedListResult[storage.InstanceRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return storage.PaginatedListResult[storage.InstanceRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (s *InstanceStore) Delete(ctx context.Context, id shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_instances WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

// ---- helpers ----

// scannable is implemented by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dst ...any) error
}

func scanInstance(sc scannable) (storage.InstanceRow, error) {
	var (
		id         shared.UUID
		templateID shared.UUID
		ckey       string
		params     []byte
		createdAt  time.Time
	)
	if err := sc.Scan(&id, &templateID, &ckey, &params, &createdAt); err != nil {
		return storage.InstanceRow{}, err
	}
	m := map[string]any{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return storage.InstanceRow{}, fmt.Errorf("unmarshal params: %w", err)
		}
	}
	return storage.InstanceRow{
		ID: id, TemplateID: templateID, ConsumerKey: ckey, Params: m, CreatedAt: createdAt,
	}, nil
}

func scanInstanceRows(rows pgx.Rows) (storage.InstanceRow, error) {
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
