// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/awaited"
)

func startTestBridge(t *testing.T) (string, *callbackRecorder) {
	t.Helper()
	executor, _, recorder := startTestExecutor(t)
	bridge, err := StartHTTPBridge("127.0.0.1", 0, executor, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bridge.Shutdown(ctx)
	})
	return "http://" + bridge.Address, recorder
}

func TestHTTPBridgeHealthz(t *testing.T) {
	base, _ := startTestBridge(t)
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("healthz = %d %s", resp.StatusCode, raw)
	}
}

func TestHTTPBridgeExecuteAcksAndPostsCallback(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	base, recorder := startTestBridge(t)

	body := `{
		"node_id": "node-a",
		"node_type": "claude-agent",
		"dispatch_id": "disp-77",
		"attributes": {"user_prompt": "go"},
		"callback_url": "http://caller.invalid/cb",
		"cancel_token": "ct-1"
	}`
	resp, err := http.Post(base+"/v1/Execute", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("execute status = %d", resp.StatusCode)
	}
	ackID := decodeAwaitAsyncAckID(t, resp.Body)

	call := recorder.waitForCall(t)
	if call.url != "http://caller.invalid/cb" {
		t.Fatalf("callback url = %q", call.url)
	}
	if call.body["async_ack_id"] != ackID {
		t.Fatalf("bridge callback body must carry async_ack_id: %v", call.body)
	}
	success, ok := call.body["success"].(map[string]any)
	if !ok {
		t.Fatalf("expected success body, got %v", call.body)
	}
	delta, _ := success["attributes_delta"].(map[string]any)
	if delta["session_token"] != "disp-77" {
		t.Fatalf("delta = %v", delta)
	}
}

// @decision: protojson-gateway
func decodeAwaitAsyncAckID(t *testing.T, body io.Reader) string {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read execute response: %v", err)
	}
	outcome := &genv1.Outcome{}
	if err := protojson.Unmarshal(raw, outcome); err != nil {
		t.Fatalf("the bridge's execute response must be a protojson Outcome, got %s: %v", raw, err)
	}
	ackID := outcome.GetAwaitAsync().GetAsyncAckId()
	if ackID == "" {
		t.Fatalf("expected an await_async outcome carrying an ack id, got %s", raw)
	}
	return ackID
}

// @decision: protojson-gateway
func TestHTTPBridgeExecuteResponseMatchesTheGrpcOutcomeShape(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	base, recorder := startTestBridge(t)

	body := `{"node_id":"n","node_type":"claude-agent","dispatch_id":"disp-shape","attributes":{"user_prompt":"go"},"callback_url":"http://caller.invalid/cb"}`
	resp, err := http.Post(base+"/v1/Execute", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("execute status = %d", resp.StatusCode)
	}
	decodeAwaitAsyncAckID(t, resp.Body)
	recorder.waitForCall(t)
}

// @decision: protojson-gateway
func TestHTTPBridgeExecuteIgnoresUnknownFieldsLikeTheGrpcTransport(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	base, recorder := startTestBridge(t)

	body := `{"node_id":"n","node_type":"claude-agent","dispatch_id":"disp-unknown","attributes":{"user_prompt":"go"},"callback_url":"http://caller.invalid/cb","not_a_proto_field":"ignored"}`
	resp, err := http.Post(base+"/v1/Execute", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("execute status = %d; a field the proto does not declare is ignored on the wire, as it is over gRPC", resp.StatusCode)
	}
	decodeAwaitAsyncAckID(t, resp.Body)
	recorder.waitForCall(t)
}

func TestHTTPBridgeExecuteRejectsOversizedBody(t *testing.T) {
	base, _ := startTestBridge(t)
	oversized := make([]byte, maxExecuteBodyBytes+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	resp, err := http.Post(base+"/v1/Execute", "application/json", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("execute status = %d, want %d for a body over the size cap", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHTTPBridgeObservabilityCapabilities(t *testing.T) {
	base, _ := startTestBridge(t)
	resp, err := http.Get(base + "/observability/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var caps map[string]any
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatal(err)
	}
	if caps["supportsTraceGet"] != true && caps["supports_trace_get"] != true {
		t.Fatalf("capabilities = %s", raw)
	}
}

func TestHTTPBridgeTraceGet(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	base, recorder := startTestBridge(t)

	body := `{"node_id":"n","node_type":"claude-agent","dispatch_id":"disp-trace-http","attributes":{"user_prompt":"go"},"callback_url":"http://caller.invalid/cb"}`
	resp, err := http.Post(base+"/v1/Execute", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	recorder.waitForCall(t)

	awaited.Until(t, "the HTTP bridge's trace for disp-trace-http to report itself complete", func() bool {
		traceResp, err := http.Get(fmt.Sprintf("%s/observability/v1/trace/%s", base, "disp-trace-http"))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(traceResp.Body)
		traceResp.Body.Close()
		var trace map[string]any
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatal(err)
		}
		return trace["complete"] == true
	})
}
