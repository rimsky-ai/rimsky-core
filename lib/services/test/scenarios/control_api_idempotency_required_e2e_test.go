// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestControlAPIIdempotencyRequired_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "idempotency-required-e2e",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": "idem/probe",
					"body_schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"reason": map[string]any{"type": "string"},
						},
					},
				},
			},
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-idempotency-required-e2e")

	messagesPath := "/v1/instances/" + instanceID + "/messages"

	// @story: instance-create-is-idle
	baseline := countInstanceMessages(t, ep, messagesPath)
	if baseline != 0 {
		t.Fatalf("newly-created instance %s already has %d persisted messages, want 0 — instance creation must be idle", instanceID, baseline)
	}

	invalidateBody := map[string]any{
		"type": "idem/probe",
		"payload": map[string]any{
			"reason": "idempotency-required-e2e",
		},
	}

	status, raw, _ := postMessage(t, ep, messagesPath, invalidateBody, "")
	if status != http.StatusBadRequest {
		t.Fatalf("keyless POST %s returned %d, want 400 — a missing Idempotency-Key must be refused, not silently accepted\nbody: %s",
			messagesPath, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "idempotency-key") {
		t.Fatalf("keyless POST 400 body did not name the required Idempotency-Key header; got: %s", string(raw))
	}

	if n := countInstanceMessages(t, ep, messagesPath); n != baseline {
		t.Fatalf("after a rejected keyless POST, GET %s shows %d messages, want baseline %d — the rejected emit must leave no envelope (and thus no idempotency row)",
			messagesPath, n, baseline)
	}

	const idemKey = "idem-key-e2e-0001"
	status, raw, firstMsgID := postMessage(t, ep, messagesPath, invalidateBody, idemKey)
	if status != http.StatusCreated {
		t.Fatalf("first keyed POST %s returned %d, want 201 Created\nbody: %s", messagesPath, status, string(raw))
	}
	if firstMsgID == "" {
		t.Fatalf("first keyed POST returned no message_id; body: %s", string(raw))
	}

	if n := countInstanceMessages(t, ep, messagesPath); n != baseline+1 {
		t.Fatalf("after the first keyed POST, GET %s shows %d messages, want exactly baseline+1=%d", messagesPath, n, baseline+1)
	}

	status, raw, replayMsgID := postMessage(t, ep, messagesPath, invalidateBody, idemKey)
	if status != http.StatusOK {
		t.Fatalf("replay keyed POST %s returned %d, want 200 OK (idempotent dedup)\nbody: %s", messagesPath, status, string(raw))
	}
	if replayMsgID != firstMsgID {
		t.Fatalf("replay returned message_id %q, want the original %q — a replayed key must dedup to the original message", replayMsgID, firstMsgID)
	}
	if n := countInstanceMessages(t, ep, messagesPath); n != baseline+1 {
		t.Fatalf("after replaying the same key, GET %s shows %d messages, want still exactly baseline+1=%d — the replay must not insert a second envelope",
			messagesPath, n, baseline+1)
	}
}

func postMessage(t *testing.T, ep harness.RimskyEndpoint, path string, body map[string]any, idempotencyKey string) (int, []byte, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal message body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ep.BaseURL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var decoded struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(out, &decoded)
	return resp.StatusCode, out, decoded.MessageID
}

func countInstanceMessages(t *testing.T, ep harness.RimskyEndpoint, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last int
	for {
		status, raw := ep.GetJSON(t, path, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s returned %d, want 200\nbody: %s", path, status, string(raw))
		}
		var resp struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode GET %s response: %v\nbody: %s", path, err, string(raw))
		}
		last = len(resp.Messages)
		if last > 0 || !time.Now().Before(deadline) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
}
