// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// InstanceTable — Postgres-backed persistence.InstanceTable.
// rimsky_instances binds to template_hash (TEXT); instance_key is nullable.
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
	foundationshared "github.com/fallguy/rimsky/foundation/shared"
)

// errInstanceIDRequired is returned by Create when in.ID is the zero UUID.
// Callers must pass a pre-generated UUID (e.g. uuid.New()) so the row's
// identity is established by the caller, not silently filled in by persistence.
var errInstanceIDRequired = errors.New("instances.create: ID is required (zero UUID rejected)")

const instanceCols = `id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, created_at, terminated_at, attribute_overrides_match_counts, main_run_scope_id`

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
	id := in.ID
	// Empty string → fall through to the column DEFAULT 'coalesce'. Any
	// other value is sent verbatim; the CHECK constraint enforces the
	// {serial_queue, coalesce} vocabulary so a bad value surfaces as a
	// 23514 from Postgres.
	var deliveryMode any
	if in.FrameDeliveryMode != "" {
		deliveryMode = in.FrameDeliveryMode
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, attribute_overrides_match_counts, main_run_scope_id)
		 VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'coalesce'), $7, $8)
		 RETURNING `+instanceCols,
		id, in.TemplateHash, in.InstanceKey, paramsBytes, overridesBytes, deliveryMode, matchCountsBytes, in.MainRunScopeID,
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
	var cursor *foundationshared.UUID
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

func (s *instancesImpl) Delete(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	// Per concept:run-scope every node_run lives under a run_scope that
	// lives under an instance. The schema (migrations 007/008) declares
	// ON DELETE CASCADE on rimsky_run_scopes.instance_id,
	// rimsky_run_scopes.parent_run_id, rimsky_run_scopes.parent_run_scope_id,
	// and rimsky_node_runs.run_scope_id — so deleting the instance row
	// walks the entire scope/dispatch tree atomically inside the DB.
	//
	// rimsky_instances.main_run_scope_id is DEFERRABLE INITIALLY
	// DEFERRED so the simultaneous deletion of instance and its main
	// scope satisfies the FK at commit time.
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_instances WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("instances.delete: %w", err)
	}
	return nil
}

// MarkTerminated sets terminated_at = now() if currently NULL. Idempotent
// — repeated calls do not move the timestamp.
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

// IncrementAttributeOverrideMatchCounts aggregates per-index counts
// from the input slice (duplicates count per-occurrence: e.g.
// `[0, 0, 1]` adds 2 to index 0 and 1 to index 1), then issues ONE
// jsonb_set UPDATE per unique index inside the caller-supplied tx.
// Out-of-range indices are silently no-ops (jsonb_set with
// create_missing=false leaves the array unchanged when the path
// doesn't exist).
//
// The per-index aggregation step is required because PostgreSQL's
// expression evaluator does NOT guarantee that two textually-distinct
// `jsonb_set(col, '{N}', ...)` subexpressions referring to the SAME
// path will compose left-to-right — duplicated paths in a chained
// expression can collapse so only one increment lands. Aggregating
// in Go and emitting one jsonb_set per unique index sidesteps the
// issue without changing the per-occurrence semantic.
//
// Per-iteration UPDATEs (not a chained single-statement jsonb_set):
// the textually-chained variant grows O(2^N) characters in the number
// of unique indices because each iteration substitutes the prior
// setExpr twice (target + value subexpression). For N≈20 in a single
// dispatch that's ~1MB of SQL, which trips Postgres' max_stack_depth
// and statement-size limits. The per-iteration form has O(1) SQL per
// statement at the cost of N round-trips inside the same tx.
//
// tx must be non-nil — s.q(tx) panics on nil per the package's
// universal convention. Callers wrap with args.Persist.Transaction.
//
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
	// Aggregate per-index deltas while preserving the first-appearance
	// order so the per-index UPDATE sequence is deterministic.
	//
	// Pre-filter negative indices: Postgres' jsonb_set with create_missing
	// =false silently no-ops for out-of-range POSITIVE indices, but treats
	// NEGATIVE indices as offsets-from-end (`{-1}` modifies the last
	// element). The runtime never produces negative indices (matched
	// slice indexes into `entries`), so this is defensive parity with the
	// SQLite mirror's pre-filter — both drivers silently skip idx < 0 so
	// callers observe the same out-of-range semantics regardless of
	// backend.
	deltas := map[int]int{}
	order := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 {
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
	ex := s.q(tx)
	// Issue ONE jsonb_set UPDATE per unique index. We MUST NOT inline
	// the prior setExpr into the next setExpr's template (the obvious
	// "chain everything into one statement" approach), because the
	// resulting SQL string grows O(2^N) characters in the number of
	// unique indices — each iteration substitutes the prior setExpr
	// twice (once as the jsonb_set target, once inside the value
	// subexpression). For an instance with ~20 matched entries in a
	// single dispatch that hits Postgres' max_stack_depth (default
	// 2MB) and statement-size limits before the query reaches the
	// planner.
	//
	// The per-iteration UPDATE has constant-size SQL and the same
	// per-occurrence semantic. The tx is already open (caller wraps
	// with args.Persist.Transaction); the per-row updates are
	// serialised at the same row lock, so concurrent callers don't
	// see partial increments outside their own tx boundary.
	//
	// jsonb_set's path arg is text[] where numeric strings index into
	// arrays. The `->>` read side, however, requires an integer
	// literal for array indexing (text args index by key name and
	// return NULL for arrays); hence the asymmetric `ARRAY['%d']` for
	// the write path vs `->>%d` for the read. Both are integer-valued
	// and locally produced (idx came from the runtime's matched-
	// indices slice; delta is a count), so SQL injection is not a
	// concern.
	for _, idx := range order {
		// Cast to `bigint` (not `int`): the Go-side counter is `int64`
		// (`InstanceRow.AttributeOverridesMatchCounts`), and a long-lived
		// instance with a single matcher firing per dispatch on a busy
		// producer could plausibly exceed PostgreSQL `int`'s ~2.1B
		// (32-bit signed) ceiling. `bigint` (64-bit signed) matches the
		// Go column's range so the database arithmetic doesn't overflow
		// before the Go decoder ever sees the value.
		query := fmt.Sprintf(
			`UPDATE rimsky_instances
			   SET attribute_overrides_match_counts = jsonb_set(
			       attribute_overrides_match_counts,
			       ARRAY['%d'],
			       to_jsonb(coalesce((attribute_overrides_match_counts->>%d)::bigint, 0) + %d),
			       false
			   )
			 WHERE id = $1`,
			idx, idx, deltas[idx],
		)
		if _, err := ex.Exec(ctx, query, instanceID); err != nil {
			return fmt.Errorf("instances.incrementAttributeOverrideMatchCounts: %w", err)
		}
	}
	return nil
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
// rimsky_lifecycle_idempotencies row at scope_kind='instance'.
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

// ---- helpers ----

// scannable is implemented by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dst ...any) error
}

func scanInstance(sc scannable) (persistence.InstanceRow, error) {
	var (
		id             foundationshared.UUID
		templateHash   string
		instanceKey    *string
		params         []byte
		overrides      []byte
		deliveryMode   string
		createdAt      time.Time
		terminatedAt   *time.Time
		matchCounts    []byte
		mainRunScopeID foundationshared.UUID
	)
	if err := sc.Scan(&id, &templateHash, &instanceKey, &params, &overrides, &deliveryMode, &createdAt, &terminatedAt, &matchCounts, &mainRunScopeID); err != nil {
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
	mc := []int64{}
	if len(matchCounts) > 0 {
		if err := json.Unmarshal(matchCounts, &mc); err != nil {
			return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides_match_counts: %w", err)
		}
	}
	return persistence.InstanceRow{
		ID:                            id,
		TemplateHash:                  templateHash,
		InstanceKey:                   instanceKey,
		Params:                        m,
		AttributeOverrides:            ov,
		AttributeOverridesMatchCounts: mc,
		FrameDeliveryMode:             deliveryMode,
		MainRunScopeID:                mainRunScopeID,
		CreatedAt:                     createdAt,
		TerminatedAt:                  terminatedAt,
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
