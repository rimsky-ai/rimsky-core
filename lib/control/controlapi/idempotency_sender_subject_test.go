// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: api-key
// @concept: message

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

type senderSubjectHarness struct {
	srv   *httptest.Server
	db    persistence.Database
	auth  *AuthState
	clock *shared.ControllableClock
	close func()
}

func newSenderSubjectHarness(t *testing.T) *senderSubjectHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	require.NoError(t, err, "persistence.Open")
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))

	clock := shared.NewControllableClock(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
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
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Clock:         clock,
		Logger:        shared.SilentLogger{},
		Stores:        reg,
		LifecycleSubs: lcReg,
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
	return &senderSubjectHarness{
		srv:   srv,
		db:    d,
		auth:  state,
		clock: clock,
		close: func() {
			srv.Close()
			_ = d.Close()
		},
	}
}

func (h *senderSubjectHarness) mintActiveAPIKey(t *testing.T, name string, perms []map[string]any) (string, shared.UUID) {
	t.Helper()
	plaintext, hash, err := auth.Mint()
	require.NoError(t, err)
	id := shared.UUID(uuid.New())
	permsJSON, err := json.Marshal(perms)
	require.NoError(t, err)
	require.NoError(t, h.db.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.db.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          id,
			Name:        name,
			KeyHash:     hash[:],
			Permissions: permsJSON,
			CreatedAt:   h.clock.Now(),
		}, tx)
	}))
	h.auth.InvalidateAnonCache()
	return plaintext, id
}

func (h *senderSubjectHarness) httpPostAs(t *testing.T, path string, body any, bearer, idemKey string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", h.srv.URL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(rawResp) > 0 {
		_ = json.Unmarshal(rawResp, &out)
	}
	return resp.StatusCode, out
}

func (h *senderSubjectHarness) newInstance(t *testing.T, adminKey, tag string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "msg-pub-" + tag + "-" + uuid.NewString(),
			"version": "v1",
			"messages": []map[string]any{
				{"type": "system/invalidate"},
			},
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, body := h.httpPostAs(t, "/v1/templates", tplBody, adminKey, "")
	require.Equal(t, http.StatusCreated, status, body)
	tplID, _ := body["template_id"].(string)
	require.NotEmpty(t, tplID)
	status, body = h.httpPostAs(t, "/v1/templates/"+tplID+"/deploy", map[string]any{}, adminKey, "")
	require.Equal(t, http.StatusOK, status, body)

	ck := "ck-" + uuid.NewString()
	status, body = h.httpPostAs(t, "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	}, adminKey, "")
	require.Equal(t, http.StatusCreated, status, body)
	instID, _ := body["instance_id"].(string)
	require.NotEmpty(t, instID)
	return instID
}

func messagePayload(label string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"label": label})
	return raw
}

func (h *senderSubjectHarness) getMessage(t *testing.T, msgID, bearer string) map[string]any {
	t.Helper()
	req, err := http.NewRequest("GET", h.srv.URL+"/v1/messages/"+msgID, nil)
	require.NoError(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(rawResp))
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rawResp, &out))
	return out
}

func TestIdempotency_SenderSubject_DistinctAPIKeys_NoCollision(t *testing.T) {
	t.Parallel()
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})

	keyAPlain, keyAID := h.mintActiveAPIKey(t, "tenant-a", []map[string]any{
		{"action": "message:send"},
		{"action": "message:read"},
	})
	keyBPlain, keyBID := h.mintActiveAPIKey(t, "tenant-b", []map[string]any{
		{"action": "message:send"},
		{"action": "message:read"},
	})
	require.NotEqual(t, keyAID, keyBID, "distinct api-key UUIDs are the sender_subject discriminator")

	instID := h.newInstance(t, adminKey, "ss-distinct")

	const sharedKey = "shared-idem-key"
	path := fmt.Sprintf("/v1/instances/%s/messages", instID)

	statusA1, bodyA1 := h.httpPostAs(t, path, map[string]any{
		"type":    "system/invalidate",
		"payload": messagePayload("A-first"),
	}, keyAPlain, sharedKey)
	require.Equal(t, http.StatusCreated, statusA1, "key A first insert must succeed: %+v", bodyA1)
	msgAID, _ := bodyA1["message_id"].(string)
	require.NotEmpty(t, msgAID)

	statusB, bodyB := h.httpPostAs(t, path, map[string]any{
		"type":    "system/invalidate",
		"payload": messagePayload("B-first"),
	}, keyBPlain, sharedKey)
	require.Equal(t, http.StatusCreated, statusB,
		"distinct sender_subject (api-key B vs A) MUST NOT replay A's row\nbody: %+v", bodyB)
	msgBID, _ := bodyB["message_id"].(string)
	require.NotEmpty(t, msgBID)
	require.NotEqual(t, msgAID, msgBID,
		"distinct sender_subject MUST return a distinct message_id; got both = %s", msgAID)

	statusA2, bodyA2 := h.httpPostAs(t, path, map[string]any{
		"type":    "system/invalidate",
		"payload": messagePayload("A-replay-P3"),
	}, keyAPlain, sharedKey)
	require.Equal(t, http.StatusOK, statusA2,
		"key A's second request with the same key must be a replay (200 OK), not a fresh insert\nbody: %+v", bodyA2)
	msgAReplayID, _ := bodyA2["message_id"].(string)
	require.Equal(t, msgAID, msgAReplayID,
		"key A's replay must return the ORIGINAL message_id; got %s, want %s", msgAReplayID, msgAID)
	require.NotEqual(t, msgBID, msgAReplayID,
		"key A's replay must NOT inherit key B's message_id (would prove sender_subject did not isolate the tuple)")

	envA := h.getMessage(t, msgAID, adminKey)
	payloadA, _ := envA["payload"].(map[string]any)
	require.Equal(t, "A-first", payloadA["label"],
		"key A's envelope must carry the FIRST request's payload; replay payload must not overwrite it")

	envB := h.getMessage(t, msgBID, adminKey)
	payloadB, _ := envB["payload"].(map[string]any)
	require.Equal(t, "B-first", payloadB["label"],
		"key B's envelope must carry B's first-request payload, distinct from key A's")
}
