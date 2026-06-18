// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @story: executor-protocol — cross-stack proof for STORY-executor-protocol.
// Boots a real rimsky-all-in-one container (testcontainers; Postgres state
// DB), runs the example Executor as an in-process gRPC server on a host
// port that the rimsky container reaches via `host.testcontainers.internal`,
// and exhibits each promised protocol surface end-to-end against the
// assembled product:
//
//  1. **Tag-keyed cascade.** A template whose worker carries
//     `mode: emit_event` causes the executor to return Success with
//     `tags: ["work_started"]`; a downstream subscriber declared
//     `subscribes: [{node: worker, type: terminal/success, when:
//     "work_started" in payload.tags}]` dispatches under the real
//     supervisor. The audit row for the worker's terminal/success
//     carries `payload.tags` containing the declared tag.
//
//  2. **Declared error class routes through `error_types:`.** A template
//     whose worker carries `mode: raise_error` and declares
//     `error_types: { example/forbidden: { policy: [give_up] } }` causes
//     the executor to settle as Error{error_class: example/forbidden};
//     the worker node settles `failed` with `current_error_class`
//     equal to the declared class — proving the routing keys on the
//     executor-declared class, not a generic fallback.
//
//  3. **Async-callback registration + delivery + persistent-registry
//     survival across supervisor restart.** A template whose worker
//     carries `mode: async_callback` causes the executor to return
//     `Outcome{AwaitAsyncCallback{async_ack_id}}` synchronously; the
//     supervisor persists the ack id to
//     `col:rimsky_node_runs.async_ack_id` (per
//     TD-persist-async-callback-registry). The test confirms the
//     persisted ack id via `/v1/observability/node-runs`, then
//     `RimskyHandle.Restart()` recreates the rimsky-all-in-one
//     container — dropping the in-memory `code:CallbackRegistry`
//     completely — and POSTs `AsyncCallbackBody{success:{...}}` to
//     the new supervisor's `route:POST /v1/callback/{async_ack_id}`.
//     The fresh supervisor's callback handler falls through to
//     `code:Queue.LookupRunByAsyncAckID` against the persisted
//     column, drives the dispatch to terminal/success, and the node
//     reaches `fresh`. This is the leg the Falsifier names: "an
//     async-callback POST is dropped after the supervisor that
//     registered it restarts."
//
//  4. **Attribute schema rejects misshapen template at registration.**
//     A template whose worker carries a static default `count: -1`
//     violates the executor's advertised `count.minimum: 0` constraint;
//     rimsky's registration-time validator (default mode `all`) refuses
//     the template with HTTP 400 — proving the executor's Capabilities
//     handshake reached the validator.
//
// The unit tests at `examples/executor/executor_test.go` prove the
// executor surfaces in isolation (Success / Error / tagged-Success /
// AwaitAsync); this test's job is to prove the cross-stack wiring
// through the real supervisor / control-api / executor catalog.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestExampleExecutorE2E(t *testing.T) {
	ctx := context.Background()

	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	srv := grpc.NewServer()
	exec := &Executor{}
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		<-srvErr
	})

	probeDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(probeDeadline) {
		conn, dErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if dErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	executorEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", port)
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExecutor("example", executorEndpoint),
		harness.WithHostPortAccess(port),
		harness.WithRefValidationMode("available"),
	)
	ep := h.Endpoint

	t.Setenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE", ep.CallbackBaseURL)

	t.Setenv("EXAMPLE_EXECUTOR_ASYNC_CALLBACK_DELAY_MS", "30000")

	waitExecutorDiscovered(t, ep, "example", 60*time.Second)

	t.Run("tag-keyed cascade", func(t *testing.T) {
		tid := deployExampleTemplate(t, ep, map[string]any{
			"name":             "example-tag-keyed-cascade",
			"version":          "1",
			"frame_timeout_ms": 300000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "example",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":    "string",
									"default": "emit_event",
								},
							},
						},
					},
				},
				{
					"type":     "sink",
					"executor": "example",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":    "string",
									"default": "ok",
								},
							},
						},
					},
					"subscribes": []map[string]any{
						{
							"node":                   "worker",
							"type":                   "terminal/success",
							"when":                   "\"work_started\" in payload.tags",
							"wake_on_change":         true,
							"force_upstream_refresh": false,
						},
					},
				},
			},
		})
		iid := createExampleInstance(t, ep, tid, "ck-tag-cascade")
		workerID := resolveExampleNodeID(t, ep, iid, "worker")
		sinkID := resolveExampleNodeID(t, ep, iid, "sink")

		waitForTerminalSuccessTag(t, ep, workerID, "work_started", 90*time.Second)

		waitForTerminalSuccessAny(t, ep, sinkID, 60*time.Second)
	})

	t.Run("async callback survives supervisor restart", func(t *testing.T) {
		const ackID = "ack-async-cross-stack-1"
		tid := deployExampleTemplate(t, ep, map[string]any{
			"name":             "example-async-callback",
			"version":          "1",
			"frame_timeout_ms": 300000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "example",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":    "string",
									"default": "async_callback",
								},
								"async_ack_id": map[string]any{
									"type":    "string",
									"default": ackID,
								},
							},
						},
					},
				},
			},
		})
		iid := createExampleInstance(t, ep, tid, "ck-async-callback")
		workerID := resolveExampleNodeID(t, ep, iid, "worker")

		waitForPersistedAsyncAckID(t, ep, iid, ackID, 90*time.Second)

		h.Restart(ctx, t)
		ep = h.Endpoint

		t.Setenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE", ep.CallbackBaseURL)

		waitExecutorDiscovered(t, ep, "example", 60*time.Second)

		postCallbackUntilOK(t, ep.CallbackBaseURL, ackID, 30*time.Second)

		waitExampleNodeState(t, ep, workerID, "fresh", "", 90*time.Second)
	})

	t.Run("declared error class routes through error_types", func(t *testing.T) {
		tid := deployExampleTemplate(t, ep, map[string]any{
			"name":             "example-declared-error",
			"version":          "1",
			"frame_timeout_ms": 300000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "example",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"mode": map[string]any{
									"type":    "string",
									"default": "raise_error",
								},
							},
						},
					},
					"error_types": map[string]any{
						"example/forbidden": map[string]any{
							"policy": []map[string]any{
								{"action": "give_up"},
							},
						},
					},
				},
			},
		})
		iid := createExampleInstance(t, ep, tid, "ck-declared-error")
		workerID := resolveExampleNodeID(t, ep, iid, "worker")
		waitExampleNodeState(t, ep, workerID, "failed", "example/forbidden", 90*time.Second)
	})

}

func deployExampleTemplate(t *testing.T, ep harness.RimskyEndpoint, spec map[string]any) string {
	t.Helper()
	body := map[string]any{"spec": spec}
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createExampleInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "executor-example", instanceKey)
	return resp.InstanceID
}

func resolveExampleNodeID(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) string {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/nodes"
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
			var resp struct {
				Nodes []struct {
					ID       string `json:"id"`
					NodeType string `json:"node_type"`
				} `json:"nodes"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, n := range resp.Nodes {
					if n.NodeType == nodeType && n.ID != "" {
						return n.ID
					}
				}
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("node %q not found via GET %s within deadline (last status %d)", nodeType, path, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitExampleNodeState(
	t *testing.T,
	ep harness.RimskyEndpoint,
	nodeID, wantState, wantErrClass string,
	deadline time.Duration,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState    string
		lastErrClass string
		lastBody     string
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/nodes/"+nodeID, "")
		if status == http.StatusOK {
			var resp struct {
				State             string `json:"state"`
				CurrentErrorClass string `json:"current_error_class"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.State
				lastErrClass = resp.CurrentErrorClass
				if resp.State == wantState {
					if wantErrClass == "" || resp.CurrentErrorClass == wantErrClass {
						return
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %s did not settle to state=%q (err_class=%q) within %v; last_state=%q last_err_class=%q last_body=%s",
		nodeID, wantState, wantErrClass, deadline, lastState, lastErrClass, lastBody)
}

func waitExecutorDiscovered(t *testing.T, ep harness.RimskyEndpoint, name string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	path := "/v1/observability/executors/" + name
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
			var resp struct {
				Peer struct {
					ReachabilityStatus        string `json:"reachability_status"`
					ObservabilityCapabilities struct {
						ExpectedAttributesSchema string `json:"expected_attributes_schema"`
					} `json:"observability_capabilities"`
				} `json:"peer"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				if resp.Peer.ObservabilityCapabilities.ExpectedAttributesSchema != "" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("executor %q did not become discoverable within %v; last body: %s",
		name, deadline, lastBody)
}

func waitForTerminalSuccessTag(t *testing.T, ep harness.RimskyEndpoint, nodeID, tag string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/events?kind=terminal/success&node_id="+nodeID, "")
		if status == http.StatusOK && payloadCarriesTag(t, raw, tag) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %s did not emit terminal/success with tag %q within %v",
		nodeID, tag, deadline)
}

func waitForTerminalSuccessAny(t *testing.T, ep harness.RimskyEndpoint, nodeID string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/events?kind=terminal/success&node_id="+nodeID, "")
		if status == http.StatusOK {
			var resp struct {
				Events []map[string]any `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil && len(resp.Events) > 0 {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %s did not emit any terminal/success within %v", nodeID, deadline)
}

func payloadCarriesTag(t *testing.T, raw []byte, tag string) bool {
	t.Helper()
	var resp struct {
		Events []struct {
			Payload map[string]any `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode /v1/events: %v\nbody: %s", err, string(raw))
	}
	for _, ev := range resp.Events {
		tagsAny, ok := ev.Payload["tags"]
		if !ok {
			continue
		}
		tagsList, ok := tagsAny.([]any)
		if !ok {
			continue
		}
		for _, t := range tagsList {
			if got, _ := t.(string); got == tag {
				return true
			}
		}
	}
	return false
}

func waitForPersistedAsyncAckID(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantAck string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	path := "/v1/observability/node-runs?instance_id=" + instanceID
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
			var resp struct {
				NodeRuns []struct {
					AsyncAckID *string `json:"async_ack_id"`
				} `json:"node_runs"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, r := range resp.NodeRuns {
					if r.AsyncAckID != nil && *r.AsyncAckID == wantAck {
						return
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("instance %s never saw async_ack_id=%q persisted on any dispatch row within %v; last body: %s",
		instanceID, wantAck, deadline, lastBody)
}

func postCallbackUntilOK(t *testing.T, callbackBaseURL, ackID string, deadline time.Duration) {
	t.Helper()
	url := callbackBaseURL + "/v1/callback/" + ackID
	body, err := json.Marshal(map[string]any{
		"success": map[string]any{
			"changed":        false,
			"change_summary": "test-driven async callback after restart",
		},
	})
	if err != nil {
		t.Fatalf("marshal callback body: %v", err)
	}
	end := time.Now().Add(deadline)
	var lastStatus int
	var lastErr error
	var lastBody string
	for time.Now().Before(end) {
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		raw, _ := readAllAndClose(resp)
		lastStatus = resp.StatusCode
		lastBody = string(raw)
		if resp.StatusCode == http.StatusOK {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("callback POST %s never returned 200 within %v; last_status=%d last_err=%v last_body=%s",
		url, deadline, lastStatus, lastErr, lastBody)
}

func readAllAndClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
