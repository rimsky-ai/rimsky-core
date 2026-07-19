// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type breakpointAuditHarness struct {
	srv    *httptest.Server
	db     persistence.Database
	auth   *AuthState
	clock  *shared.ControllableClock
	bearer string
}

func newBreakpointAuditHarness(t *testing.T) *breakpointAuditHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })

	clock := shared.NewControllableClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	state := &AuthState{
		Tables:   d.Tables(),
		Registry: BuildV1Registry(),
		Clock:    clock,
		Logger:   shared.SilentLogger{},
	}

	plaintext, hash, err := auth.Mint()
	require.NoError(t, err)
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID(uuid.New()),
			Name:        "seed",
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now(),
		}, tx)
	}))

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	topicsFake := storetest.NewFake("topics-ring", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	reg.Add("topics-ring", topicsFake)

	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Clock:          clock,
		Logger:         shared.SilentLogger{},
		AuthState:      state,
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
		NamedLocks: locks.NamedLocksConfig{
			Locks: map[string]locks.NamedLockConfig{
				"topics-ring:concurrent": {Limit: 5},
			},
		},
		Executors: map[string]ExecutorEntry{
			"worker": {Transport: "grpc", Endpoint: "localhost:0"},
		},
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	h := &breakpointAuditHarness{srv: srv, db: d, auth: state, clock: clock}
	h.bearer = plaintext
	return h
}

func (h *breakpointAuditHarness) request(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func (h *breakpointAuditHarness) seedInstance(t *testing.T, suffix string) string {
	t.Helper()
	status, out := h.request(t, "POST", "/v1/templates", validTemplateBody("bp-audit-"+suffix))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	status, out = h.request(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	status, out = h.request(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "bp-audit-ck-" + suffix,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	return instID
}

func (h *breakpointAuditHarness) lastAttemptedRow(t *testing.T) map[string]any {
	t.Helper()
	var res persistence.EventListResult
	err := h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = h.db.Tables().Events().List(ctx, persistence.EventListFilter{
			Kind: auth.EventAccessAttempted,
		}, persistence.ListPagination{Limit: 1}, tx)
		return lerr
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Events, "no auth.access_attempted row present after request")
	return res.Events[0].Payload
}

func TestBreakpointRoute_AccessAttemptedCarriesBreakpointAndHitID(t *testing.T) {
	h := newBreakpointAuditHarness(t)
	instID := h.seedInstance(t, uuid.NewString())

	status, out := h.request(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpIDStr, _ := out["breakpoint_id"].(string)
	require.NotEmpty(t, bpIDStr)
	bpID, err := uuid.Parse(bpIDStr)
	require.NoError(t, err)

	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	var hitID shared.UUID
	require.NoError(t, h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := h.db.Tables().BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: shared.UUID(bpID),
			InstanceID:   shared.UUID(instUUID),
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			Snapshot: map[string]any{
				"dispatch_context": map[string]any{
					"merged_attributes": map[string]any{},
				},
			},
		}, tx)
		if err != nil {
			return err
		}
		hitID = id
		return nil
	}))

	status, out = h.request(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpIDStr),
		map[string]any{"hit_id": hitID.String()})
	require.Equal(t, http.StatusOK, status, out)

	payload := h.lastAttemptedRow(t)
	require.Equal(t, "breakpoint:resume", payload["action"])

	requestPath, _ := payload["request_path"].(string)
	require.Contains(t, requestPath, bpIDStr,
		"auth.access_attempted for a breakpoint route must carry the breakpoint_id (via request_path)")

	rawParams, ok := payload["request_params"]
	require.True(t, ok, "auth.access_attempted must carry request_params for the resume call")
	paramsBytes, err := json.Marshal(rawParams)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(paramsBytes), hitID.String()),
		"auth.access_attempted request_params must carry hit_id: got %s", string(paramsBytes))
}
