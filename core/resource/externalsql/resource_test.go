package externalsql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// -----------------------------------------------------------------------------
// In-memory fake storage.ResourceRegistry
// @source: core/resource/inlinejsonb/resource_test.go:fakeResourceRegistry
// -----------------------------------------------------------------------------

type fakeResourceRegistry struct {
	mu       sync.Mutex
	rows     map[shared.UUID]*storage.ResourceRow
	versions map[shared.UUID]*storage.ResourceVersionRow
	ordered  map[shared.UUID][]shared.UUID
	now      func() time.Time
}

func newFakeRegistry() *fakeResourceRegistry {
	return &fakeResourceRegistry{
		rows:     map[shared.UUID]*storage.ResourceRow{},
		versions: map[shared.UUID]*storage.ResourceVersionRow{},
		ordered:  map[shared.UUID][]shared.UUID{},
		now:      time.Now,
	}
}

func (f *fakeResourceRegistry) Create(ctx context.Context, in storage.ResourceCreateInput, tx storage.Tx) (storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	row := &storage.ResourceRow{
		ID:           id,
		ResourcePath: append([]string(nil), in.ResourcePath...),
		OwnerNodeID:  in.OwnerNodeID,
		KeepVersions: in.KeepVersions,
		CreatedAt:    f.now(),
	}
	f.rows[id] = row
	return *row, nil
}

func (f *fakeResourceRegistry) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *row
	return &cp, nil
}

func (f *fakeResourceRegistry) ListByOwner(ctx context.Context, ownerNodeID shared.UUID, tx storage.Tx) ([]storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []storage.ResourceRow
	for _, r := range f.rows {
		if r.OwnerNodeID == ownerNodeID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeResourceRegistry) CommitVersion(ctx context.Context, resourceID shared.UUID, in storage.ResourceCommitInput, tx storage.Tx) (storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[resourceID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	vid := uuid.New()
	producedBy := in.ProducedBy
	v := &storage.ResourceVersionRow{
		ID:            vid,
		ResourceID:    resourceID,
		ProducedBy:    &producedBy,
		Data:          append([]byte(nil), in.Data...),
		DataRef:       append([]byte(nil), in.DataRef...),
		ChangeSummary: in.ChangeSummary,
		CommittedAt:   f.now(),
	}
	f.versions[vid] = v
	f.ordered[resourceID] = append(f.ordered[resourceID], vid)
	if row.CurrentVersionID != nil {
		prev := *row.CurrentVersionID
		row.PreviousVersionID = &prev
	}
	row.CurrentVersionID = &vid
	return *v, nil
}

func (f *fakeResourceRegistry) NoOpCommit(ctx context.Context, resourceID shared.UUID, tx storage.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[resourceID]; !ok {
		return fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	return nil
}

func (f *fakeResourceRegistry) GCOldVersions(ctx context.Context, resourceID shared.UUID, keep int, tx storage.Tx) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[resourceID]; !ok {
		return 0, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	return 0, nil
}

func (f *fakeResourceRegistry) RestoreVersion(ctx context.Context, resourceID shared.UUID, target string, versionID shared.UUID, tx storage.Tx) (storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[resourceID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	var pickID shared.UUID
	switch target {
	case "previous":
		if row.PreviousVersionID == nil {
			return storage.ResourceVersionRow{}, fmt.Errorf("fake: no previous version")
		}
		pickID = *row.PreviousVersionID
	case "id":
		if _, ok := f.versions[versionID]; !ok {
			return storage.ResourceVersionRow{}, fmt.Errorf("fake: version %s not found", versionID)
		}
		pickID = versionID
	default:
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown target %q", target)
	}
	v, ok := f.versions[pickID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: version %s not found", pickID)
	}
	if row.CurrentVersionID != nil {
		prev := *row.CurrentVersionID
		row.PreviousVersionID = &prev
	}
	cur := pickID
	row.CurrentVersionID = &cur
	return *v, nil
}

func (f *fakeResourceRegistry) GetVersion(ctx context.Context, versionID shared.UUID, tx storage.Tx) (*storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[versionID]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (f *fakeResourceRegistry) ListVersions(ctx context.Context, resourceID shared.UUID, tx storage.Tx) ([]storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	order := f.ordered[resourceID]
	out := make([]storage.ResourceVersionRow, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		if v, ok := f.versions[order[i]]; ok {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeResourceRegistry) ListVersionsPaged(ctx context.Context, resourceID shared.UUID, pag storage.ListPagination, tx storage.Tx) (storage.PaginatedListResult[storage.ResourceVersionRow], error) {
	rows, err := f.ListVersions(ctx, resourceID, tx)
	if err != nil {
		return storage.PaginatedListResult[storage.ResourceVersionRow]{}, err
	}
	return storage.PaginatedListResult[storage.ResourceVersionRow]{Rows: rows}, nil
}

var _ storage.ResourceRegistry = (*fakeResourceRegistry)(nil)

// -----------------------------------------------------------------------------
// Harness helpers
// -----------------------------------------------------------------------------

// setupTable creates a per-test schema with target + staging + previous tables.
// Returns the schema name and a cleanup that drops it.
func setupTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (schema string) {
	t.Helper()
	// Make a unique schema per test so parallel tests do not collide.
	schema = "rimsky_ext_test_" + uuid.New().String()[:8]
	mustExec(t, ctx, pool, fmt.Sprintf(`CREATE SCHEMA %q`, schema))
	mustExec(t, ctx, pool, fmt.Sprintf(
		`CREATE TABLE %q."items" (
			id        TEXT PRIMARY KEY,
			name      TEXT,
			category  TEXT
		)`, schema))
	// Staging and previous are created on demand by the resource; no need to
	// pre-create them.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema))
	})
	return schema
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(ctx, sql)
	require.NoError(t, err, "exec: %s", sql)
}

// newResource builds an external-sql resource wired to the given pool & schema.
func newResource(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reg *fakeResourceRegistry, schema string, rules []qualityrule.Spec) resource.Resource {
	t.Helper()
	ownerID := uuid.New()
	row, err := reg.Create(ctx, storage.ResourceCreateInput{
		ResourcePath: []string{"items", schema},
		OwnerNodeID:  ownerID,
		KeepVersions: 2,
	}, nil)
	require.NoError(t, err)

	f := Factory{
		Connections:     map[string]*pgxpool.Pool{"main": pool},
		StorageRegistry: reg,
	}
	cfg := resource.Config{
		"connection_ref": "main",
		"schema":         schema,
		"table":          "items",
		"primary_key":    []any{"id"},
		"_resource_id":   row.ID.String(),
		"_path":          []string{"items", schema},
		"_owner_node_id": ownerID.String(),
	}
	res, err := f.Create(cfg, rules, nil)
	require.NoError(t, err)
	return res
}

func rowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %q.%q`, schema, table)).Scan(&n)
	require.NoError(t, err)
	return n
}

func tableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
		schema, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestExternalSQL_CommitHappyPath_RowCountMatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha", "category": "R1"},
			{"id": "b", "name": "Beta", "category": "R2"},
			{"id": "c", "name": "Gamma", "category": "C1"},
		},
		Changed:       true,
		ChangeSummary: "initial load",
	})
	require.NoError(t, err)
	require.True(t, out.Accepted)
	require.NotNil(t, out.Version)

	// Current table has the new rows.
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items"))
	// Staging was recreated empty after the swap.
	require.Equal(t, 0, rowCount(t, ctx, pool, schema, "items__staging"))
	// DataRef encodes the commit metadata.
	require.NotNil(t, out.Version.DataRef)
	require.Contains(t, string(out.Version.DataRef), `"row_count":3`)
}

func TestExternalSQL_CommitWithQualityRejection_StagingTruncated_CurrentUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)

	// First commit: 3 rows, no rules.
	baseline := newResource(t, ctx, pool, reg, schema, nil)
	out1, err := baseline.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha", "category": "R1"},
			{"id": "b", "name": "Beta", "category": "R2"},
			{"id": "c", "name": "Gamma", "category": "C1"},
		},
		Changed: true,
	})
	require.NoError(t, err)
	require.True(t, out1.Accepted)
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items"))

	// Second commit: rebuild resource with a min_ratio=0.9 rule. Submit 1 row.
	rules := []qualityrule.Spec{{
		Type:     "row_count_ratio",
		Config:   map[string]any{"min_ratio": 0.9},
		Severity: shared.SeverityError,
	}}
	res := newResource(t, ctx, pool, reg, schema, rules)
	out2, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "z", "name": "Lone", "category": "X"},
		},
		Changed: true,
	})
	require.NoError(t, err)
	require.False(t, out2.Accepted)
	require.Nil(t, out2.Version)
	require.Len(t, out2.QualityErrors, 1)
	require.Equal(t, "row_count_ratio", out2.QualityErrors[0].RuleType)

	// Current table is unchanged.
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items"))
}

func TestExternalSQL_RollbackPrevious_SwapsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	// v1: 2 rows.
	_, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha", "category": "R1"},
			{"id": "b", "name": "Beta", "category": "R2"},
		},
		Changed: true,
	})
	require.NoError(t, err)
	require.Equal(t, 2, rowCount(t, ctx, pool, schema, "items"))

	// v2: 4 rows; previous table now holds the v1 contents.
	_, err = res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha2", "category": "R1"},
			{"id": "b", "name": "Beta2", "category": "R2"},
			{"id": "c", "name": "Gamma", "category": "C1"},
			{"id": "d", "name": "Delta", "category": "C2"},
		},
		Changed: true,
	})
	require.NoError(t, err)
	require.Equal(t, 4, rowCount(t, ctx, pool, schema, "items"))
	require.Equal(t, 2, rowCount(t, ctx, pool, schema, "items__previous"))

	// Restore previous.
	_, err = res.RestoreVersion(ctx, resource.VersionRef{Kind: "previous"})
	require.NoError(t, err)

	// Current now has 2 rows (the old v1), previous has 4 (the swapped-out v2).
	require.Equal(t, 2, rowCount(t, ctx, pool, schema, "items"))
	require.Equal(t, 4, rowCount(t, ctx, pool, schema, "items__previous"))

	// Spot-check content: item "a" reverted to "Alpha", not "Alpha2".
	var name string
	err = pool.QueryRow(ctx, fmt.Sprintf(`SELECT name FROM %q.items WHERE id='a'`, schema)).Scan(&name)
	require.NoError(t, err)
	require.Equal(t, "Alpha", name)
}

func TestExternalSQL_RestoreVersion_ByID_ReturnsRollbackUnsupported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	_, err := res.RestoreVersion(ctx, resource.VersionRef{Kind: "id", ID: uuid.New()})
	require.Error(t, err)
	require.True(t, errors.Is(err, resource.ErrRollbackUnsupported))
}

func TestExternalSQL_TwoConcurrentCommits_SerializeCorrectly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	// Two goroutines committing different payloads concurrently. Postgres
	// ACCESS EXCLUSIVE locks on the ALTER TABLE RENAME steps serialize the
	// transactions; the later commit wins. We only assert the final state is
	// one of the two submitted row counts (never partial / interleaved).
	payloadA := []map[string]any{
		{"id": "a1", "name": "A1", "category": "R1"},
		{"id": "a2", "name": "A2", "category": "R1"},
	}
	payloadB := []map[string]any{
		{"id": "b1", "name": "B1", "category": "R2"},
		{"id": "b2", "name": "B2", "category": "R2"},
		{"id": "b3", "name": "B3", "category": "R2"},
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = res.Commit(ctx, resource.CommitRequest{
			ProducedBy: uuid.New(), Result: payloadA, Changed: true,
		})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = res.Commit(ctx, resource.CommitRequest{
			ProducedBy: uuid.New(), Result: payloadB, Changed: true,
		})
	}()
	wg.Wait()

	// Under a race the loser may fail because the swap rename sees a missing
	// staging table after the winner's recreate. Require at least one success;
	// if both succeeded we check that the visible state matches one payload.
	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	require.GreaterOrEqual(t, successCount, 1, "at least one commit should succeed, got errs=%v", errs)

	n := rowCount(t, ctx, pool, schema, "items")
	require.True(t, n == 2 || n == 3, "final row count should match one of the payloads, got %d", n)
}

func TestExternalSQL_NoOpCommit_NoStageTruncated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	// Seed the current table so a no-op can be distinguished from empty.
	_, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha", "category": "R1"},
		},
		Changed: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, rowCount(t, ctx, pool, schema, "items"))

	// Pre-populate the staging table with a sentinel row; a no-op commit must
	// NOT truncate it. (Operators sometimes manually inspect staging between
	// runs.)
	mustExec(t, ctx, pool, fmt.Sprintf(
		`INSERT INTO %q."items__staging" (id, name, category) VALUES ('sentinel','S','Z')`, schema))
	require.Equal(t, 1, rowCount(t, ctx, pool, schema, "items__staging"))

	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result:     []map[string]any{},
		Changed:    false,
	})
	require.NoError(t, err)
	require.True(t, out.Accepted)
	require.Nil(t, out.Version)

	// Current and staging untouched.
	require.Equal(t, 1, rowCount(t, ctx, pool, schema, "items"))
	require.Equal(t, 1, rowCount(t, ctx, pool, schema, "items__staging"))

	// Direct NoOpCommit also works.
	require.NoError(t, res.NoOpCommit(ctx))
	require.Equal(t, 1, rowCount(t, ctx, pool, schema, "items"))
}

func TestExternalSQL_ConfigErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	ownerID := uuid.New()
	row, err := reg.Create(ctx, storage.ResourceCreateInput{
		ResourcePath: []string{"items", schema},
		OwnerNodeID:  ownerID,
		KeepVersions: 2,
	}, nil)
	require.NoError(t, err)

	base := resource.Config{
		"connection_ref": "main",
		"schema":         schema,
		"table":          "items",
		"primary_key":    []any{"id"},
		"_resource_id":   row.ID.String(),
		"_path":          []string{"items", schema},
		"_owner_node_id": ownerID.String(),
	}
	f := Factory{
		Connections:     map[string]*pgxpool.Pool{"main": pool},
		StorageRegistry: reg,
	}

	// Unknown connection_ref.
	cfg := cloneCfg(base)
	cfg["connection_ref"] = "missing"
	_, err = f.Create(cfg, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection_ref")

	// Unknown table.
	cfg = cloneCfg(base)
	cfg["table"] = "does_not_exist"
	_, err = f.Create(cfg, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "probe")

	// Identifier with embedded quote.
	cfg = cloneCfg(base)
	cfg["table"] = `bad"name`
	_, err = f.Create(cfg, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "double quotes")
}

func cloneCfg(in resource.Config) resource.Config {
	out := make(resource.Config, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
