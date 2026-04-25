package externalsql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// sqlResource is a Resource whose committed payloads live in a caller-owned
// SQL table. Commits stage into a side table and then atomically swap it into
// place. All registry bookkeeping (current/previous pointers, version rows)
// is delegated to the injected storage.ResourceRegistry.
type sqlResource struct {
	resourceID  shared.UUID
	path        []string
	ownerNodeID shared.UUID
	pool        *pgxpool.Pool
	rules       []qualityrule.Spec
	storage     storage.ResourceRegistry
	cfg         instanceConfig
}

// Path implements resource.Resource.
func (r *sqlResource) Path() []string { return r.path }

// OwnerNodeID implements resource.Resource.
func (r *sqlResource) OwnerNodeID() shared.UUID { return r.ownerNodeID }

// probeExists issues a 0-row select to verify the target table exists. Called
// once at Factory.Create so config errors surface early.
func (r *sqlResource) probeExists(ctx context.Context) error {
	_, err := r.pool.Exec(ctx,
		fmt.Sprintf(`SELECT 1 FROM %s.%s LIMIT 0`, q(r.cfg.Schema), q(r.cfg.Table)))
	return err
}

// CurrentVersion implements resource.Resource.
func (r *sqlResource) CurrentVersion(ctx context.Context) (*resource.Version, error) {
	row, err := r.storage.Get(ctx, r.resourceID, nil)
	if err != nil || row == nil || row.CurrentVersionID == nil {
		return nil, err
	}
	v, err := r.storage.GetVersion(ctx, *row.CurrentVersionID, nil)
	if err != nil || v == nil {
		return nil, err
	}
	return toResourceVersion(*v), nil
}

// PreviousVersion implements resource.Resource.
func (r *sqlResource) PreviousVersion(ctx context.Context) (*resource.Version, error) {
	row, err := r.storage.Get(ctx, r.resourceID, nil)
	if err != nil || row == nil || row.PreviousVersionID == nil {
		return nil, err
	}
	v, err := r.storage.GetVersion(ctx, *row.PreviousVersionID, nil)
	if err != nil || v == nil {
		return nil, err
	}
	return toResourceVersion(*v), nil
}

// ListVersions implements resource.Resource.
func (r *sqlResource) ListVersions(ctx context.Context, limit int) ([]*resource.Version, error) {
	rows, err := r.storage.ListVersions(ctx, r.resourceID, nil)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]*resource.Version, 0, len(rows))
	for _, v := range rows {
		out = append(out, toResourceVersion(v))
	}
	return out, nil
}

// Commit implements resource.Resource.
//
// Pipeline:
//  1. Coerce req.Result to a list of rows ([]map[string]any). Non-list inputs
//     become a "_shape" quality failure.
//  2. Run quality rules against the new rows, using the previous-table rows
//     as PreviousData so ratio rules can compare row counts across commits.
//  3. On pass with Changed=false → delegate to NoOpCommit, no table writes.
//  4. On pass with Changed=true → in a single pgx transaction: create
//     staging table if missing, TRUNCATE it, bulk-insert rows via
//     jsonb_populate_recordset, then ALTER-TABLE swap current↔staging↔previous.
//  5. Record a version manifest in rimsky_resource_versions with a DataRef
//     describing the new on-disk table.
func (r *sqlResource) Commit(ctx context.Context, req resource.CommitRequest) (*resource.CommitResult, error) {
	rows, err := toRows(req.Result)
	if err != nil {
		return &resource.CommitResult{
			Accepted: false,
			QualityErrors: []qualityrule.Failure{{
				RuleType: "_shape",
				Severity: shared.SeverityError,
				Details:  err.Error(),
			}},
		}, nil
	}

	// PreviousData for ratio rules: read the current (pre-swap) table. If the
	// table is empty (first commit) we pass nil, matching the semantics of
	// row_count_ratio's "skip if no previous" branch.
	prevRows, _ := r.fetchAll(ctx, r.cfg.Table)
	var prevData any
	if len(prevRows) > 0 {
		prevData = prevRows
	}

	errsList, _, err := qualityrule.EvaluateAll(ctx, r.rules, qualityrule.EvalInput{
		NewData:      rows,
		PreviousData: prevData,
	})
	if err != nil {
		return nil, fmt.Errorf("externalsql: quality rules: %w", err)
	}
	if len(errsList) > 0 {
		return &resource.CommitResult{Accepted: false, QualityErrors: errsList}, nil
	}

	if !req.Changed {
		if err := r.storage.NoOpCommit(ctx, r.resourceID, nil); err != nil {
			return nil, err
		}
		return &resource.CommitResult{Accepted: true}, nil
	}

	// Transactional stage + swap.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("externalsql: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureTableLike(ctx, tx, r.cfg.Schema, r.cfg.StagingTable, r.cfg.Table); err != nil {
		return nil, fmt.Errorf("externalsql: ensure staging: %w", err)
	}
	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`TRUNCATE %s.%s`, q(r.cfg.Schema), q(r.cfg.StagingTable))); err != nil {
		return nil, fmt.Errorf("externalsql: truncate staging: %w", err)
	}
	if err := bulkInsertRows(ctx, tx, r.cfg.Schema, r.cfg.StagingTable, rows); err != nil {
		return nil, fmt.Errorf("externalsql: bulk insert: %w", err)
	}
	if err := swapTables(ctx, tx, r.cfg.Schema, r.cfg.Table, r.cfg.StagingTable, r.cfg.PreviousTable); err != nil {
		return nil, fmt.Errorf("externalsql: swap: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("externalsql: commit tx: %w", err)
	}

	dataRef, _ := json.Marshal(map[string]any{
		"schema":    r.cfg.Schema,
		"table":     r.cfg.Table,
		"row_count": len(rows),
	})
	vr, err := r.storage.CommitVersion(ctx, r.resourceID, storage.ResourceCommitInput{
		ProducedBy:    req.ProducedBy,
		DataRef:       dataRef,
		ChangeSummary: req.ChangeSummary,
	}, nil)
	if err != nil {
		return nil, err
	}
	return &resource.CommitResult{Accepted: true, Version: toResourceVersion(vr)}, nil
}

// NoOpCommit implements resource.Resource.
func (r *sqlResource) NoOpCommit(ctx context.Context) error {
	return r.storage.NoOpCommit(ctx, r.resourceID, nil)
}

// RestoreVersion implements resource.Resource. Only Kind=="previous" is
// supported: swap the current and previous tables. By-id restore would
// require per-version staging retention, which is out of scope for v1;
// we return ErrRollbackUnsupported for Kind=="id".
func (r *sqlResource) RestoreVersion(ctx context.Context, target resource.VersionRef) (*resource.Version, error) {
	switch target.Kind {
	case "previous":
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("externalsql: begin restore: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := swapCurrentAndPrevious(ctx, tx, r.cfg.Schema, r.cfg.Table, r.cfg.PreviousTable); err != nil {
			return nil, fmt.Errorf("externalsql: restore swap: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("externalsql: restore commit: %w", err)
		}
		vr, err := r.storage.RestoreVersion(ctx, r.resourceID, "previous", shared.UUID{}, nil)
		if err != nil {
			return nil, fmt.Errorf("externalsql: registry restore: %w", err)
		}
		return toResourceVersion(vr), nil
	case "id":
		return nil, fmt.Errorf("externalsql: restore by id: %w", resource.ErrRollbackUnsupported)
	}
	return nil, fmt.Errorf("externalsql: unknown VersionRef.Kind %q", target.Kind)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// toRows coerces Commit input (any JSON-ish value) into []map[string]any. The
// executor is expected to hand back either a slice of record-like maps or a
// JSON array encoded via Go types. []any whose elements are all maps is
// accepted for JSON round-trip convenience.
func toRows(v any) ([]map[string]any, error) {
	switch x := v.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), x...), nil
	case []any:
		out := make([]map[string]any, 0, len(x))
		for i, it := range x {
			m, ok := it.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("row %d is not a map[string]any (got %T)", i, it)
			}
			out = append(out, m)
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("result is nil (expected list of rows)")
	}
	// Tolerant fallback: JSON-roundtrip so callers handing back a typed struct
	// or map-of-arrays get a sensible error instead of a panic.
	raw, merr := json.Marshal(v)
	if merr != nil {
		return nil, fmt.Errorf("result is not a list of rows (unmarshalable: %s)", merr)
	}
	var asList []map[string]any
	if jerr := json.Unmarshal(raw, &asList); jerr == nil {
		return asList, nil
	}
	return nil, fmt.Errorf("result is not a list of rows (got %T)", v)
}

// fetchAll reads every row from a schema-qualified table and returns them as
// []map[string]any using jsonb aggregation. Returns (nil, nil) if the table
// does not exist.
func (r *sqlResource) fetchAll(ctx context.Context, table string) ([]map[string]any, error) {
	var jsonBytes []byte
	err := r.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM %s.%s t`,
			q(r.cfg.Schema), q(table))).Scan(&jsonBytes)
	if err != nil {
		// Likely: table does not exist on a first commit. Surface as empty.
		return nil, err
	}
	if len(jsonBytes) == 0 {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ensureTableLike creates <schema>.<name> as a clone of <schema>.<like> if it
// does not already exist (INCLUDING ALL preserves defaults and not-null, but
// not FKs).
func ensureTableLike(ctx context.Context, tx pgx.Tx, schema, name, like string) error {
	_, err := tx.Exec(ctx,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (LIKE %s.%s INCLUDING ALL)`,
			q(schema), q(name), q(schema), q(like)))
	return err
}

// bulkInsertRows inserts a batch of JSON-like rows into <schema>.<table> using
// jsonb_populate_recordset so the client does not need to know column names.
// Rows are marshaled as a single JSONB array.
func bulkInsertRows(ctx context.Context, tx pgx.Tx, schema, table string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal rows: %w", err)
	}
	_, err = tx.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s.%s
		             SELECT * FROM jsonb_populate_recordset(NULL::%s.%s, $1::jsonb)`,
			q(schema), q(table), q(schema), q(table)),
		payload)
	return err
}

// swapTables performs an atomic rename cycle:
//
//	current → __rimsky_tmp_swap
//	staging → current
//	previous → dropped
//	__rimsky_tmp_swap → previous
//	staging → recreated as (LIKE current INCLUDING ALL)
//
// Runs within the caller's transaction so readers see either the old or new
// current table, never an intermediate state.
func swapTables(ctx context.Context, tx pgx.Tx, schema, current, staging, previous string) error {
	tmp := "__rimsky_tmp_swap"
	stmts := []string{
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(current), q(tmp)),
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(staging), q(current)),
		fmt.Sprintf(`DROP TABLE IF EXISTS %s.%s`, q(schema), q(previous)),
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(tmp), q(previous)),
		fmt.Sprintf(`CREATE TABLE %s.%s (LIKE %s.%s INCLUDING ALL)`,
			q(schema), q(staging), q(schema), q(current)),
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("swap step %q: %w", firstWords(s, 5), err)
		}
	}
	return nil
}

// swapCurrentAndPrevious swaps only the current ↔ previous tables; used by
// RestoreVersion(previous). Leaves the staging table untouched.
func swapCurrentAndPrevious(ctx context.Context, tx pgx.Tx, schema, current, previous string) error {
	tmp := "__rimsky_tmp_swap"
	stmts := []string{
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(current), q(tmp)),
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(previous), q(current)),
		fmt.Sprintf(`ALTER TABLE %s.%s RENAME TO %s`, q(schema), q(tmp), q(previous)),
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("restore swap step %q: %w", firstWords(s, 5), err)
		}
	}
	return nil
}

// q wraps a Postgres identifier in double quotes. The Factory rejects any
// identifier containing a literal `"` so this is injection-safe within its
// intended usage.
func q(ident string) string {
	return `"` + ident + `"`
}

// firstWords is a tiny diagnostic helper: return the first n whitespace-split
// tokens of s for inclusion in error messages without dumping full SQL.
func firstWords(s string, n int) string {
	parts := strings.Fields(s)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " ")
}

// toResourceVersion adapts a storage.ResourceVersionRow into a resource.Version.
// Duplicated from inlinejsonb.toResourceVersion.
// @source: core/resource/inlinejsonb/resource.go:toResourceVersion
func toResourceVersion(v storage.ResourceVersionRow) *resource.Version {
	return &resource.Version{
		ID:             v.ID,
		ProducedByNode: v.ProducedBy,
		Data:           v.Data,
		DataRef:        v.DataRef,
		ChangeSummary:  v.ChangeSummary,
		CommittedAt:    v.CommittedAt,
	}
}

// Ensure errors package import stays reachable.
var _ = errors.New
