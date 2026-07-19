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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type mcpParityHarness struct {
	srv *httptest.Server
	db  persistence.Database
}

func newMCPParityHarness(t *testing.T) *mcpParityHarness {
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

	return &mcpParityHarness{srv: srv, db: d}
}

func (h *mcpParityHarness) mintKey(t *testing.T, name, permissionsJSON string) string {
	t.Helper()
	plaintext, hash, err := auth.Mint()
	require.NoError(t, err)
	require.NoError(t, h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.db.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID(uuid.New()),
			Name:        name,
			KeyHash:     hash[:],
			Permissions: []byte(permissionsJSON),
			CreatedAt:   time.Now(),
		}, tx)
	}))
	return plaintext
}

func (h *mcpParityHarness) http(t *testing.T, method, path, bearer string, body any) (int, map[string]any) {
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
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
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

func (h *mcpParityHarness) attemptedRowForAction(t *testing.T, action string) map[string]any {
	t.Helper()
	var res persistence.EventListResult
	err := h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = h.db.Tables().Events().List(ctx, persistence.EventListFilter{
			Kind: auth.EventAccessAttempted,
		}, persistence.ListPagination{Limit: 10}, tx)
		return lerr
	})
	require.NoError(t, err)
	for _, ev := range res.Events {
		if ev.Payload["action"] == action {
			return ev.Payload
		}
	}
	t.Fatalf("no auth.access_attempted row found for action %q among %d recent rows", action, len(res.Events))
	return nil
}

func TestMCPWrite_UnderDryRunGrant_NoMutationAndProtocolSkinMCP(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, out := h.http(t, "POST", "/v1/templates", admin, validTemplateBody("mcp-dry-run-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	dryRunner := h.mintKey(t, "dry-run-tagger",
		`[{"action":"mcp:read"},{"action":"tag:create","mode":"dry_run"}]`)

	tagName := "mcp-dry-run-tag-" + uuid.NewString()
	rpcBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "tag_create",
			"arguments": map[string]any{
				"tag":      tagName,
				"template": tplID,
			},
		},
	}
	status, out = h.http(t, "POST", "/v1/mcp", dryRunner, rpcBody)
	require.Equal(t, http.StatusOK, status, out)

	result, ok := out["result"].(map[string]any)
	require.True(t, ok, "expected JSON-RPC result envelope: %v", out)
	content, ok := result["content"].([]any)
	require.True(t, ok, "expected result.content array: %v", result)
	require.NotEmpty(t, content)
	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	text, _ := first["text"].(string)
	require.NotEmpty(t, text)

	var toolResult map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &toolResult))
	require.Equal(t, true, toolResult["dry_run"],
		"MCP write under a dry_run grant must return the same dry-run envelope as HTTP: %v", toolResult)
	require.Contains(t, toolResult, "would_have_created_tag")

	payload := h.attemptedRowForAction(t, "tag:create")
	require.Equal(t, "mcp", payload["protocol_skin"])
	require.Equal(t, string(auth.ModeDryRun), payload["mode"])
	require.Equal(t, false, payload["executed"])

	status, out = h.http(t, "GET", "/v1/tags", admin, nil)
	require.Equal(t, http.StatusOK, status, out)
	tags, _ := out["tags"].([]any)
	for _, tg := range tags {
		m, _ := tg.(map[string]any)
		require.NotEqual(t, tagName, m["tag"],
			"a dry-run MCP write must not mutate state: tag %q was created", tagName)
	}
}
