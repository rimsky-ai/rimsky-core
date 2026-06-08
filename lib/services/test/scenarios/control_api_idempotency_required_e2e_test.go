// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that the platform refuses any message-emit that omits the
// mandatory `Idempotency-Key` header, against the REAL assembled product.
//
// S-control-api-mcp-idempotency-key-required: as a publisher, when I emit a
// message via `POST /instances/{id}/messages`, the platform refuses any
// request that omits the `Idempotency-Key` header, so replay-dedup is
// mandatory and a missing key can never silently bypass it.
//
// Unlike a handler-altitude unit test, this drives the REAL control-api
// handler inside the running rimsky-all-in-one image over real HTTP (not an
// httptest recorder, not an in-process call). The value path is the live
// `handleCreateMessage` reached through the chi router, the auth middleware
// chain, and the real persistence layer (`rimsky_message_idempotencies` +
// the message envelope insert) on the baked SQLite backend. The control-api,
// scheduler, and supervisor are the real value-delivering components; the
// in-tree stub executor stands in for "whatever executor your deployment
// provides" so the template's worker node can be claimed/dispatched, but the
// thing under test — the idempotency-key guard and the dedup INSERT — is the
// real, shipped control-api code path.
//
// The three observable outcomes the story names are each asserted at the wire:
//
//	(1) A POST carrying a valid invalidate body but NO Idempotency-Key returns
//	    400 with a header-required diagnostic, AND leaves no trace: a
//	    subsequent GET /instances/{id}/messages shows zero messages (no
//	    envelope, and — since the dedup INSERT is gated on the same tx as the
//	    envelope insert — no idempotency row either).
//	(2) A POST that DOES carry the header returns 201 Created with a message_id.
//	(3) A third POST replaying that same header returns 200 OK with the
//	    IDENTICAL message_id and inserts no second envelope (GET still shows
//	    exactly one message).
//
// If the guard ever regresses (an empty/absent key silently accepted, or the
// replay producing a fresh envelope), this test fails on the observable HTTP
// status / message-count, not a Docker error.
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

// TestControlAPIIdempotencyRequired_E2E proves the mandatory-Idempotency-Key
// contract end to end against the live control-api: a keyless emit is rejected
// 400 and leaves no message, a keyed emit is accepted 201, and a replay of the
// same key is deduped 200 to the original message_id with no second envelope.
func TestControlAPIIdempotencyRequired_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable on the shared network before
	// rimsky/all starts — the control-api fires a Capabilities handshake
	// against declared executors at startup. Network first, then the
	// executor peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// A single worker node gives the message-emit path a real node to source
	// a delivery frame on (resolveMessageFrameSource needs at least one node)
	// and a real target for the invalidate envelope.
	templateID := deploySQLiteTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "idempotency-required-e2e",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})
	instanceID := createSQLiteInstance(t, ep, templateID, "ck-idempotency-required-e2e")

	messagesPath := "/instances/" + instanceID + "/messages"

	// A valid invalidate body — targets the worker node by type. The ONLY
	// difference between the rejected and accepted POSTs below is the presence
	// of the Idempotency-Key header, so the body is held constant.
	invalidateBody := map[string]any{
		"kind":   "invalidate",
		"target": "worker",
	}

	// (1) Keyless emit: valid body, NO Idempotency-Key header → 400 with a
	// header-required diagnostic. This is the heart of the story: a missing key
	// can never silently bypass dedup.
	status, raw, _ := postMessage(t, ep, messagesPath, invalidateBody, "")
	if status != http.StatusBadRequest {
		t.Fatalf("keyless POST %s returned %d, want 400 — a missing Idempotency-Key must be refused, not silently accepted\nbody: %s",
			messagesPath, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "idempotency-key") {
		t.Fatalf("keyless POST 400 body did not name the required Idempotency-Key header; got: %s", string(raw))
	}

	// The rejected emit must have left NO trace. The dedup INSERT and the
	// envelope insert share one tx and both run only AFTER the key guard, so a
	// rejected keyless POST inserts neither: GET shows zero messages. (No
	// envelope ⇒ no idempotency row, since the row is written in the same tx as
	// the envelope it points at.)
	if n := countInstanceMessages(t, ep, messagesPath); n != 0 {
		t.Fatalf("after a rejected keyless POST, GET %s shows %d messages, want 0 — the rejected emit must leave no envelope (and thus no idempotency row)",
			messagesPath, n)
	}

	// (2) Keyed emit: same valid body, WITH an Idempotency-Key → 201 Created
	// with a message_id. The status-code distinction (201 first insert vs 200
	// replay) is operator-visible, so it is asserted exactly.
	const idemKey = "idem-key-e2e-0001"
	status, raw, firstMsgID := postMessage(t, ep, messagesPath, invalidateBody, idemKey)
	if status != http.StatusCreated {
		t.Fatalf("first keyed POST %s returned %d, want 201 Created\nbody: %s", messagesPath, status, string(raw))
	}
	if firstMsgID == "" {
		t.Fatalf("first keyed POST returned no message_id; body: %s", string(raw))
	}

	// Exactly one envelope now exists.
	if n := countInstanceMessages(t, ep, messagesPath); n != 1 {
		t.Fatalf("after the first keyed POST, GET %s shows %d messages, want exactly 1", messagesPath, n)
	}

	// (3) Replay: a third POST with the SAME key → 200 OK with the IDENTICAL
	// message_id, and NO second envelope. This is the dedup guarantee the
	// mandatory key exists to deliver.
	status, raw, replayMsgID := postMessage(t, ep, messagesPath, invalidateBody, idemKey)
	if status != http.StatusOK {
		t.Fatalf("replay keyed POST %s returned %d, want 200 OK (idempotent dedup)\nbody: %s", messagesPath, status, string(raw))
	}
	if replayMsgID != firstMsgID {
		t.Fatalf("replay returned message_id %q, want the original %q — a replayed key must dedup to the original message", replayMsgID, firstMsgID)
	}
	if n := countInstanceMessages(t, ep, messagesPath); n != 1 {
		t.Fatalf("after replaying the same key, GET %s shows %d messages, want still exactly 1 — the replay must not insert a second envelope",
			messagesPath, n)
	}
}

// postMessage POSTs body as JSON to ep.BaseURL+path, optionally setting the
// Idempotency-Key header (empty key ⇒ header omitted entirely, which is the
// precise condition the guard must reject). Returns the HTTP status, the raw
// response body, and the decoded message_id (empty when the body has none).
//
// The harness PostJSON helper sets no custom headers, so this scenario uses a
// local raw-HTTP poster: the header's presence/absence is the variable under
// test and must be controllable per request. Content-Type application/json is
// always set because the control-api write surface gates on it
// (AllowContentType middleware) — a missing Content-Type would be rejected
// before the handler runs, masking the idempotency-key behavior under test.
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

// countInstanceMessages reads GET /instances/{id}/messages and returns the
// number of envelopes recorded for the instance. Pages are not walked because
// the test inserts at most one message; a non-empty next_cursor under that
// invariant would itself be a defect, so a single page is the right read.
func countInstanceMessages(t *testing.T, ep harness.RimskyEndpoint, path string) int {
	t.Helper()
	// A short settle window: the GET is against the same control-api process
	// that just handled the POST, so the envelope is durably committed before
	// the POST returns — but a tiny retry guards against any read-after-write
	// projection lag on SQLite rather than racing on the first GET.
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
