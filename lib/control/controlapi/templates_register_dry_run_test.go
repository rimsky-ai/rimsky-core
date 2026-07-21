// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: dry-run

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type dryRunAppHarness struct {
	srv    *httptest.Server
	db     persistence.Database
	bearer string
}

func newDryRunAppHarness(t *testing.T) *dryRunAppHarness {
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
			Name:        "admin",
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

	lcReg := lifecycle.NewRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		AdvisoryLocker: d.AdvisoryLocker(),
		Queue:          d.Queue(),
		Clock:          clock,
		Logger:         shared.SilentLogger{},
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
		AuthState: state,
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	return &dryRunAppHarness{srv: srv, db: d, bearer: plaintext}
}

func (h *dryRunAppHarness) do(t *testing.T, method, path string, body any) (int, map[string]any) {
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

func TestTemplateRegister_DryRun_InvalidSpecStillRejected(t *testing.T) {
	h := newDryRunAppHarness(t)

	body := map[string]any{
		"spec": map[string]any{
			"name":    "dry-run-invalid-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "not-a-real-executor"},
			},
		},
	}
	status, out := h.do(t, "POST", "/v1/templates?dry_run=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestTemplateRegister_DryRun_ValidSpecSkipsInsert(t *testing.T) {
	h := newDryRunAppHarness(t)

	name := "dry-run-valid-" + uuid.NewString()
	body := map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, out := h.do(t, "POST", "/v1/templates?dry_run=true", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["dry_run"])
	summary, ok := out["would_have_registered"].(map[string]any)
	require.True(t, ok, "response missing would_have_registered envelope: %v", out)
	hash, _ := summary["template_hash"].(string)
	require.NotEmpty(t, hash)

	status, _ = h.do(t, "GET", "/v1/templates/"+hash, nil)
	require.Equal(t, http.StatusNotFound, status,
		"dry-run register must not insert the template row")

	status, out = h.do(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, hash, out["template_id"],
		"a real register after the dry-run must produce the same hash the dry-run reported")

	status, _ = h.do(t, "GET", "/v1/templates/"+hash, nil)
	require.Equal(t, http.StatusOK, status,
		"after a real (non-dry-run) register, the template row must exist")
}
