package frame_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/internal/pgtest"
)

// seedTemplateAndInstance inserts a minimal rimsky_templates row carrying a
// frame_resolution mode in spec, and a child rimsky_instances row. Returns
// (templateHash, instanceID).
func seedTemplateAndInstance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, mode string) (templateHash string, instanceID uuid.UUID) {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash = "sha256-" + suffix
	spec := `{}`
	if mode != "" {
		spec = `{"frame_resolution":"` + mode + `"}`
	}
	_, err := pool.Exec(ctx, `
        INSERT INTO rimsky_templates (id, spec, state)
        VALUES ($1, $2::jsonb, 'deployed')
    `, templateHash, spec)
	require.NoError(t, err)

	instanceID = uuid.New()
	_, err = pool.Exec(ctx, `
        INSERT INTO rimsky_instances (id, template_hash, instance_key, params)
        VALUES ($1, $2, $3, '{}'::jsonb)
    `, instanceID, templateHash, "ck-"+instanceID.String()[:8])
	require.NoError(t, err)
	return templateHash, instanceID
}

func TestEnqueueOrCoalesce_SerialQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "serial_queue")

	// Three calls produce three frames.
	for i := 0; i < 3; i++ {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		fid, err := frame.EnqueueOrCoalesce(ctx, tx, instanceID, uuid.New())
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, fid)
		require.NoError(t, tx.Commit(ctx))
	}

	var (
		count      int
		modeMatch  int
		stateMatch int
		singletons int
	)
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT COUNT(*),
               COUNT(*) FILTER (WHERE mode = 'serial_queue'),
               COUNT(*) FILTER (WHERE state = 'queued'),
               COUNT(*) FILTER (WHERE array_length(source_node_ids, 1) = 1)
        FROM rimsky_frames WHERE instance_id = $1
    `, instanceID).Scan(&count, &modeMatch, &stateMatch, &singletons))
	require.Equal(t, 3, count)
	require.Equal(t, 3, modeMatch)
	require.Equal(t, 3, stateMatch)
	require.Equal(t, 3, singletons)
}

func TestEnqueueOrCoalesce_CoalesceFirstInsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "coalesce")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	fid, err := frame.EnqueueOrCoalesce(ctx, tx, instanceID, uuid.New())
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, fid)
	require.NoError(t, tx.Commit(ctx))

	var (
		count int
		mode  string
		state string
	)
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT COUNT(*), MAX(mode), MAX(state) FROM rimsky_frames WHERE instance_id = $1
    `, instanceID).Scan(&count, &mode, &state))
	require.Equal(t, 1, count)
	require.Equal(t, "coalesce", mode)
	require.Equal(t, "queued", state)
}

func TestEnqueueOrCoalesce_CoalesceAppendsSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "coalesce")

	src1 := uuid.New()
	src2 := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	fid1, err := frame.EnqueueOrCoalesce(ctx, tx, instanceID, src1)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	fid2, err := frame.EnqueueOrCoalesce(ctx, tx, instanceID, src2)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t, fid1, fid2, "second call should return same frame id (coalesced)")

	var (
		count    int
		srcCount int
	)
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT COUNT(*), COALESCE(MAX(array_length(source_node_ids, 1)), 0)
        FROM rimsky_frames WHERE instance_id = $1
    `, instanceID).Scan(&count, &srcCount))
	require.Equal(t, 1, count)
	require.Equal(t, 2, srcCount)
}

func TestEnqueueOrCoalesce_CoalesceDedupesSameSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "coalesce")

	src := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = frame.EnqueueOrCoalesce(ctx, tx, instanceID, src)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = frame.EnqueueOrCoalesce(ctx, tx, instanceID, src)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var srcCount int
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT COALESCE(MAX(array_length(source_node_ids, 1)), 0)
        FROM rimsky_frames WHERE instance_id = $1
    `, instanceID).Scan(&srcCount))
	require.Equal(t, 1, srcCount)
}

func TestEnqueueOrCoalesce_InvalidMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// Empty string mode — template has no frame_resolution set.
	_, instanceID := seedTemplateAndInstance(t, ctx, pool, "")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = frame.EnqueueOrCoalesce(ctx, tx, instanceID, uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mode")
}

func TestEnqueueOrCoalesce_InstanceNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = frame.EnqueueOrCoalesce(ctx, tx, uuid.New(), uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
