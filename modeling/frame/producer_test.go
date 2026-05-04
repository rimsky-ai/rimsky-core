package frame_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/frame"
	"github.com/fallguy/rimsky/modeling/internal/pgtest"
)

// enqueueAgainstDriver runs frame.EnqueueOrCoalesce inside a fresh tx
// owned by the persistence driver. The tx commits when fn returns nil
// and rolls back on a non-nil return.
func enqueueAgainstDriver(ctx context.Context, d persistence.Driver,
	instanceID, sourceNodeID uuid.UUID) (uuid.UUID, error) {
	var fid uuid.UUID
	err := d.Store().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := frame.EnqueueOrCoalesce(ctx, d.Store(), tx, instanceID, sourceNodeID)
		if err != nil {
			return err
		}
		fid = got
		return nil
	})
	return fid, err
}

// seedTemplateAndInstance inserts a minimal rimsky_templates row carrying
// a frame_resolution mode in spec, and a child rimsky_instances row.
// Returns (templateHash, instanceID). Goes through ExecForTest because
// the test fixture pins state='deployed' directly.
func seedTemplateAndInstance(t *testing.T, ctx context.Context, d persistence.Driver, mode string) (templateHash string, instanceID uuid.UUID) {
	t.Helper()
	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	templateHash = "sha256-" + suffix
	spec := `{}`
	if mode != "" {
		spec = `{"frame_resolution":"` + mode + `"}`
	}
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_templates (id, spec, state)
        VALUES ($1, $2::jsonb, 'deployed')
    `, templateHash, spec)

	instanceID = uuid.New()
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_instances (id, template_hash, instance_key, params)
        VALUES ($1, $2, $3, '{}'::jsonb)
    `, instanceID, templateHash, "ck-"+instanceID.String()[:8])
	return templateHash, instanceID
}

func TestEnqueueOrCoalesce_SerialQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "serial_queue")

	// Three calls produce three frames.
	for i := 0; i < 3; i++ {
		fid, err := enqueueAgainstDriver(ctx, d, instanceID, uuid.New())
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, fid)
	}

	var (
		count      int
		modeMatch  int
		stateMatch int
		singletons int
	)
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT COUNT(*),
               COUNT(*) FILTER (WHERE mode = 'serial_queue'),
               COUNT(*) FILTER (WHERE state = 'queued'),
               COUNT(*) FILTER (WHERE array_length(source_node_ids, 1) = 1)
        FROM rimsky_frames WHERE instance_id = $1
    `, []any{instanceID}, &count, &modeMatch, &stateMatch, &singletons)
	require.Equal(t, 3, count)
	require.Equal(t, 3, modeMatch)
	require.Equal(t, 3, stateMatch)
	require.Equal(t, 3, singletons)
}

func TestEnqueueOrCoalesce_CoalesceFirstInsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "coalesce")

	fid, err := enqueueAgainstDriver(ctx, d, instanceID, uuid.New())
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, fid)

	var (
		count int
		mode  string
		state string
	)
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT COUNT(*), MAX(mode), MAX(state) FROM rimsky_frames WHERE instance_id = $1
    `, []any{instanceID}, &count, &mode, &state)
	require.Equal(t, 1, count)
	require.Equal(t, "coalesce", mode)
	require.Equal(t, "queued", state)
}

func TestEnqueueOrCoalesce_CoalesceAppendsSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "coalesce")

	src1 := uuid.New()
	src2 := uuid.New()

	fid1, err := enqueueAgainstDriver(ctx, d, instanceID, src1)
	require.NoError(t, err)

	fid2, err := enqueueAgainstDriver(ctx, d, instanceID, src2)
	require.NoError(t, err)

	require.Equal(t, fid1, fid2, "second call should return same frame id (coalesced)")

	var (
		count    int
		srcCount int
	)
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT COUNT(*), COALESCE(MAX(array_length(source_node_ids, 1)), 0)
        FROM rimsky_frames WHERE instance_id = $1
    `, []any{instanceID}, &count, &srcCount)
	require.Equal(t, 1, count)
	require.Equal(t, 2, srcCount)
}

func TestEnqueueOrCoalesce_CoalesceDedupesSameSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, instanceID := seedTemplateAndInstance(t, ctx, d, "coalesce")

	src := uuid.New()

	_, err := enqueueAgainstDriver(ctx, d, instanceID, src)
	require.NoError(t, err)

	_, err = enqueueAgainstDriver(ctx, d, instanceID, src)
	require.NoError(t, err)

	var srcCount int
	pgtest.QueryRowForTest(ctx, t, d, `
        SELECT COALESCE(MAX(array_length(source_node_ids, 1)), 0)
        FROM rimsky_frames WHERE instance_id = $1
    `, []any{instanceID}, &srcCount)
	require.Equal(t, 1, srcCount)
}

func TestEnqueueOrCoalesce_InvalidMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	// Empty string mode — template has no frame_resolution set.
	_, instanceID := seedTemplateAndInstance(t, ctx, d, "")

	_, err := enqueueAgainstDriver(ctx, d, instanceID, uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mode")
}

func TestEnqueueOrCoalesce_InstanceNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d := pgtest.OpenDriver(ctx, t)

	_, err := enqueueAgainstDriver(ctx, d, uuid.New(), uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
