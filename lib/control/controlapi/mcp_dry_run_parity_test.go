// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

type mcpParityHarness struct {
	srv          *httptest.Server
	db           persistence.Database
	mcpSessionID string
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

func (h *mcpParityHarness) ensureMCPSession(t *testing.T, bearer string) string {
	t.Helper()
	if h.mcpSessionID != "" {
		return h.mcpSessionID
	}
	_, hdr, out := h.httpWithHeaders(t, "POST", "/v1/mcp", bearer, "", map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize"})
	sid := hdr.Get("Mcp-Session-Id")
	require.NotEmpty(t, sid, "mcp initialize must issue a session id: %v", out)
	h.mcpSessionID = sid
	return sid
}

func (h *mcpParityHarness) http(t *testing.T, method, path, bearer string, body any) (int, map[string]any) {
	t.Helper()
	sessionID := ""
	if method == http.MethodPost && path == "/v1/mcp" {
		sessionID = h.ensureMCPSession(t, bearer)
	}
	status, _, out := h.httpWithHeaders(t, method, path, bearer, sessionID, body)
	return status, out
}

func (h *mcpParityHarness) httpWithHeaders(t *testing.T, method, path, bearer, mcpSessionID string, body any) (int, http.Header, map[string]any) {
	t.Helper()
	body = injectDefaultTargetDaemonIfInstanceCreate(method, path, body)
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
	if mcpSessionID != "" {
		req.Header.Set("Mcp-Session-Id", mcpSessionID)
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
	return resp.StatusCode, resp.Header, out
}

func (h *mcpParityHarness) attemptedRowForAction(t *testing.T, action string) map[string]any {
	t.Helper()
	var res persistence.EventListResult
	err := h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = h.db.Tables().Events().List(ctx, persistence.EventListFilter{
			KindIn: []string{auth.EventAccessAttempted.String()},
		}, persistence.ListPagination{Limit: 10}, tx)
		return lerr
	})
	require.NoError(t, err)
	for _, ev := range res.Events {
		if ev.Payload.Map()["action"] == action {
			return ev.Payload.Map()
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

// @concept: dry-run
func TestDryRunTagCreateOnAnExistingTagStillSaysItIsARehearsal(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, out := h.http(t, "POST", "/v1/templates", admin, validTemplateBody("dry-run-existing-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	tagName := "dry-run-existing-tag-" + uuid.NewString()
	status, out = h.http(t, "POST", "/v1/tags", admin, map[string]any{"tag": tagName, "template": tplID})
	require.Equal(t, http.StatusCreated, status, out)

	dryRunner := h.mintKey(t, "dry-run-existing-tagger",
		`[{"action":"mcp:read"},{"action":"tag:create","mode":"dry_run"}]`)

	status, out = h.http(t, "POST", "/v1/tags", dryRunner, map[string]any{"tag": tagName, "template": tplID})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["dry_run"],
		"a dry-run create on a tag that already names the same template must still say it was a rehearsal: %v", out)
	require.Contains(t, out, "would_have_left_tag_unchanged")

	status, out = h.http(t, "POST", "/v1/templates", admin, validTemplateBody("dry-run-other-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	otherID, _ := out["template_id"].(string)
	require.NotEmpty(t, otherID)

	status, out = h.http(t, "POST", "/v1/tags", dryRunner, map[string]any{"tag": tagName, "template": otherID})
	require.Equal(t, http.StatusConflict, status,
		"a dry run previews what the write would produce, and this write would be refused: %v", out)

	status, out = h.http(t, "GET", "/v1/tags", admin, nil)
	require.Equal(t, http.StatusOK, status, out)
	for _, tg := range out["tags"].([]any) {
		m, _ := tg.(map[string]any)
		if m["tag"] == tagName {
			require.Equal(t, tplID, m["template_id"], "the dry runs moved the tag")
		}
	}
}

func TestMCPLineageAncestorsDescendants_ReachDedicatedRoutesNotTheRunItem(t *testing.T) {
	h := newMCPParityHarness(t)
	admin := h.mintKey(t, "admin", `[{"action":"*"}]`)

	status, out := h.http(t, "POST", "/v1/templates", admin, validTemplateBody("mcp-lineage-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.http(t, "POST", "/v1/templates/"+tplID+"/deploy", admin, map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out = h.http(t, "POST", "/v1/instances", admin, map[string]any{
		"template":     tplID,
		"instance_key": "mcp-lineage-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	rootScope := shared.UUID(uuid.New())
	var frameID shared.UUID
	require.NoError(t, h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		if err := h.db.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: rootScope, GraphName: "main", InstanceID: shared.UUID(instUUID),
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := h.db.Tables().Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: shared.UUID(instUUID), Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := h.db.Tables().Frames().InsertRunningFrame(ctx, shared.UUID(instUUID), msgID, rootScope, tx)
		frameID = fid
		return err
	}))
	require.NotEqual(t, shared.UUID{}, frameID)

	parentRunID := uuid.New()
	childRunID := uuid.New()
	base := time.Now().UTC()
	insertLeaf := func(runID uuid.UUID, substitutedFrom uuid.UUID, observedAt time.Time) {
		rec := map[string]any{"run_id": runID.String(), "frame_id": frameID.String(), "state": "fresh"}
		if substitutedFrom != uuid.Nil {
			rec["substitution_refs"] = []map[string]any{{"source_kind": "run", "source_version_or_id": substitutedFrom.String()}}
		}
		recBytes, merr := json.Marshal(rec)
		require.NoError(t, merr)
		require.NoError(t, h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			return h.db.Tables().Lineage().Insert(ctx, persistence.LineageRow{
				ID: shared.UUID(uuid.New()), RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: shared.UUID(instUUID), FrameID: frameID, ObservedAt: observedAt, Record: recBytes,
			}, tx)
		}))
	}
	insertLeaf(parentRunID, uuid.Nil, base)
	insertLeaf(childRunID, parentRunID, base.Add(time.Second))

	callTool := func(name string, args map[string]any) map[string]any {
		rpcBody := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		}
		status, out := h.http(t, "POST", "/v1/mcp", admin, rpcBody)
		require.Equal(t, http.StatusOK, status, out)
		result, ok := out["result"].(map[string]any)
		require.True(t, ok, "expected JSON-RPC result envelope for %s: %v", name, out)
		content, ok := result["content"].([]any)
		require.True(t, ok && len(content) > 0, "expected result.content for %s: %v", name, result)
		first, _ := content[0].(map[string]any)
		text, _ := first["text"].(string)
		var toolResult map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &toolResult))
		return toolResult
	}

	ancestorsResult := callTool("lineage_run_ancestors", map[string]any{"run_id": childRunID.String()})
	ancestors, _ := ancestorsResult["ancestors"].([]any)
	require.Len(t, ancestors, 1,
		"lineage_run_ancestors must reach the ancestors sub-route, not the plain run item")
	ancItem, _ := ancestors[0].(map[string]any)
	ancRec, _ := ancItem["record"].(map[string]any)
	require.Equal(t, parentRunID.String(), ancRec["run_id"])

	descendantsResult := callTool("lineage_run_descendants", map[string]any{"run_id": parentRunID.String()})
	descendants, _ := descendantsResult["descendants"].([]any)
	require.Len(t, descendants, 1,
		"lineage_run_descendants must reach the descendants sub-route, not the plain run item")
	descItem, _ := descendants[0].(map[string]any)
	descRec, _ := descItem["record"].(map[string]any)
	require.Equal(t, childRunID.String(), descRec["run_id"])
}
