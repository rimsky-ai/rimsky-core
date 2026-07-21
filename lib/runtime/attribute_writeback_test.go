// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func newWritebackServer(t *testing.T, backend persistence.Tables, q persistence.Queue, supervisorID string) http.Handler {
	t.Helper()
	c := &runtime.CallbackServer{
		Persist:      backend,
		Queue:        q,
		Logger:       shared.SilentLogger{},
		SupervisorID: supervisorID,
	}
	r := chi.NewRouter()
	r.Post("/v1/runs/{run_id}/attributes", runtime.HandleAttributeWritebackForTest(c))
	return r
}

func postWriteback(router http.Handler, runID, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/attributes", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAttributeWriteback_AppliesDeltaAndBumpsProgressInOneTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	_, workerNode, _, _, runID := seedDispositionFixture(ctx, t, d, "attribute-writeback-route")

	const supID = "sup-writeback"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := d.Queue().ClaimDispatchRow(ctx, tx, runID, supID)
		if err != nil {
			return err
		}
		require.True(t, ok)
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, tx, runID, supID)
		if err != nil {
			return err
		}
		require.True(t, promoted)
		return nil
	}))

	pgdbtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_node_runs SET last_progress_at = NULL WHERE id = $1`, runID)

	router := newWritebackServer(t, backend, d.Queue(), supID)
	token := supID + ":" + runID.String()

	rec := postWriteback(router, runID.String(), token, `{"attributes_delta":{"progress_note":"halfway"}}`)
	require.Equal(t, http.StatusNoContent, rec.Code, "body=%s", rec.Body.String())

	var attrs *persistence.NodeAttributesRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := backend.NodeAttributes().GetByRun(ctx, runID, tx)
		attrs = row
		return err
	}))
	require.NotNil(t, attrs, "the writeback must land on the per-run attribute ledger")
	require.Equal(t, "halfway", attrs.Data["progress_note"])

	var lastProgress *time.Time
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT last_progress_at FROM rimsky_node_runs WHERE id = $1`, []any{runID}, &lastProgress)
	require.NotNil(t, lastProgress,
		"the attribute writeback must bump last_progress_at in the same transaction as the write")

	rec = postWriteback(router, runID.String(), token, `{"attributes_delta":{"progress_note":"done","extra":1}}`)
	require.Equal(t, http.StatusNoContent, rec.Code, "body=%s", rec.Body.String())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := backend.NodeAttributes().GetByRun(ctx, runID, tx)
		attrs = row
		return err
	}))
	require.Equal(t, "done", attrs.Data["progress_note"], "a second writeback merges as a delta")

	_ = workerNode
}

func TestAttributeWriteback_ContractStatuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	_, _, _, _, runID := seedDispositionFixture(ctx, t, d, "attribute-writeback-statuses")

	const supID = "sup-writeback-statuses"
	router := newWritebackServer(t, backend, d.Queue(), supID)
	token := supID + ":" + runID.String()

	rec := postWriteback(router, runID.String(), "", `{"attributes_delta":{"k":"v"}}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "missing cancel_token must 401")

	rec = postWriteback(router, runID.String(), "sup-other:"+runID.String(), `{"attributes_delta":{"k":"v"}}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "foreign cancel_token must 401")

	rec = postWriteback(router, "not-a-uuid", supID+":not-a-uuid", `{"attributes_delta":{"k":"v"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "invalid run id must 400")

	rec = postWriteback(router, runID.String(), token, `{`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "invalid json must 400")

	rec = postWriteback(router, runID.String(), token, `{"attributes_delta":{}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "empty delta must 400")

	rec = postWriteback(router, runID.String(), token, `{"attributes_delta":{"k":"v"}}`)
	require.Equal(t, http.StatusConflict, rec.Code,
		"a writeback for a run that is not running/held must be refused (state stale); body=%s", rec.Body.String())

	unknown := "00000000-0000-0000-0000-00000000dead"
	rec = postWriteback(router, unknown, supID+":"+unknown, `{"attributes_delta":{"k":"v"}}`)
	require.Equal(t, http.StatusNotFound, rec.Code, "unknown run must 404")
}
