// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type adminHarness struct {
	driver  persistence.Database
	persist persistence.Tables
}

func newAdminHarness(t *testing.T) *adminHarness {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	return &adminHarness{driver: d, persist: d.Tables()}
}

func buildRouter(register func(chi.Router, AppDeps), deps AppDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1", func(v1 chi.Router) {
		register(v1, deps)
	})
	return r
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func TestClaimHoldersRoute(t *testing.T) {
	t.Parallel()
	h := newAdminHarness(t)
	ctx := context.Background()

	deps := AppDeps{
		Persist: h.persist,
		Logger:  shared.SilentLogger{},
	}
	router := buildRouter(registerClaimsRoutes, deps)

	status, _ := doJSON(t, router, http.MethodGet, "/v1/lock-holders/not-a-uuid/claim-holders", nil)
	require.Equal(t, http.StatusBadRequest, status)

	emptyID := uuid.New().String()
	status, body := doJSON(t, router, http.MethodGet, "/v1/lock-holders/"+emptyID+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var emptyResp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &emptyResp))
	require.Empty(t, emptyResp.Holders)

	holderNodeID := seedThrowawayNode(t, h)
	holderRunID := seedRunForNode(ctx, t, h, holderNodeID)
	lockHolderID := seedScopeClaimHandle(ctx, t, h, holderNodeID)
	claimHolderID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            claimHolderID,
			ClaimHandleID: lockHolderID,
			HolderRunID:   holderRunID,
		}, tx)
	}))

	status, body = doJSON(t, router, http.MethodGet, "/v1/lock-holders/"+lockHolderID.String()+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var resp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Holders, 1)
	require.Equal(t, claimHolderID.String(), resp.Holders[0]["id"])
	require.Equal(t, lockHolderID.String(), resp.Holders[0]["claim_handle_id"])
	require.Equal(t, holderRunID.String(), resp.Holders[0]["holder_run_id"])
	require.Equal(t, "active", resp.Holders[0]["state"])
}

func seedThrowawayNode(t *testing.T, h *adminHarness) shared.UUID {
	t.Helper()
	ctx := context.Background()

	suffix := uuid.NewString()
	suffix = strings.ReplaceAll(suffix, "-", "")
	suffix = (suffix + suffix)[:64]
	tplHash := "sha256-" + suffix
	instID := uuid.New()
	nodeID := uuid.New()

	pgtest.ExecForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_templates (id, spec, state, registered_at)
		 VALUES ($1, '{}'::jsonb, 'deployed', now())`,
		tplHash,
	)
	mainScopeID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		ck := "ck-" + uuid.NewString()
		_, err := h.persist.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instID,
			TemplateHash:   tplHash,
			InstanceKey:    &ck,
			MainRunScopeID: mainScopeID,
		}, tx)
		return err
	}))
	pgtest.ExecForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor,
		   created_at, updated_at
		 ) VALUES (
		   $1, $2, 'root', 'worker',
		   now(), now()
		 )`,
		nodeID, instID,
	)
	return nodeID
}

func seedRunForNode(ctx context.Context, t *testing.T, h *adminHarness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var instID, frameID, mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT instance_id FROM rimsky_nodes WHERE id = $1`,
		[]any{nodeID}, &instID,
	)
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instID}, &mainScopeID,
	)
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_messages(id, instance_id, type, sender, sender_kind, received_at)
		 VALUES ($1, $2, 'test/seed', 'test', 'operator', now())`,
		msgID, instID,
	)
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_frames(instance_id, triggering_message_id, state, queued_at, started_at, last_progress_at, frame_timeout_ms)
		 VALUES ($1, $2, 'running', now(), now(), now(), 600000)
		 RETURNING frame_id`,
		[]any{instID, msgID}, &frameID,
	)
	_ = nodeID
	var runID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`INSERT INTO rimsky_node_runs(id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, run_scope_id, sequence)
		 VALUES (gen_random_uuid(), $1, 'worker', ARRAY[]::text[], now(), 'stale', $2, $3, 0)
		 RETURNING id`,
		[]any{nodeID, frameID, mainScopeID}, &runID,
	)
	return runID
}

func seedScopeClaimHandle(ctx context.Context, t *testing.T, h *adminHarness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	producerName := "test-store"
	intent := "rw"
	id := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 id,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"r-1"`),
			Intent:             &intent,
			HolderSupervisorID: "scenario-supervisor",
			HolderNodeID:       nodeID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
		}, tx)
	}))
	return id
}
