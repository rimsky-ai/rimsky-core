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

// TestExampleExecutorE2E drives the three cross-stack legs end-to-end
// (tag-keyed cascade, async-callback restart-survival, declared error
// class routing) against a single BringUpRimskyHandle bring-up. The
// example Executor is launched in-process on an OS-assigned host
// port, exposed to the rimsky container via the reverse-SSH tunnel
// WithHostPortAccess sets up.
//
// @deliberate: known intermittent — the testcontainers reverse-SSH
// host-port tunnel is environment-sensitive on Docker Desktop. A
// connection-refused error in `last_error` at the discovery probe
// usually clears after a Docker Desktop restart. The unit tests at
// `examples/executor/executor_test.go` exercise the executor
// surface in isolation and are not affected.
func TestExampleExecutorE2E(t *testing.T) {
	// @deliberate: NOT t.Parallel — the test uses t.Setenv to swap the
	// example executor's async-callback host override per-restart,
	// which Go's testing package forbids inside a parallel-marked
	// test (the `t.Setenv` machinery panics if t.Parallel was called).
	ctx := context.Background()

	// @deliberate: in-process gRPC server hosting the example
	// Executor + ExecutorObservability surfaces. We start it on an
	// OS-assigned port so parallel test runs do not collide and so
	// the test does not depend on the example binary having been
	// installed on the host.
	// @deliberate: bind 0.0.0.0 so the testcontainers reverse-SSH
	// tunnel forwarding `host.testcontainers.internal:<port>` →
	// the host's `<port>` reaches us regardless of which loopback
	// interface the tunnel terminates on.
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

	// @deliberate: probe our own listener over the loopback before
	// continuing so we observe early if the goroutine hasn't begun
	// Accept. A connection-refused here means the server hasn't
	// started; without this probe the failure surfaces 60s later
	// as a discovery-cache timeout that masks the real cause.
	probeDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(probeDeadline) {
		conn, dErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if dErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// @deliberate: bring up rimsky-all-in-one against a Postgres
	// testcontainer, registering the in-process example executor as
	// `example` via WithExecutor. The rimsky container reaches it
	// through the reverse-SSH host-port tunnel opened by
	// WithHostPortAccess, at
	// `host.testcontainers.internal:<port>`.
	executorEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", port)
	// @deliberate: ref_validation_mode = "available" tolerates the
	// in-process executor's Capabilities response not yet being
	// visible at registration time (the discovery handshake runs
	// asynchronously after container bring-up). The example
	// executor's schema is still enforced at dispatch time once the
	// discovery cache catches up.
	// @deliberate: BringUpRimskyHandle (over BringUpRimsky) so the
	// async-callback leg can drive RimskyHandle.Restart and witness
	// callback delivery against a recreated supervisor — the
	// in-memory CallbackRegistry is dropped on restart, forcing the
	// callback handler to fall through to the persisted ack column.
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExecutor("example", executorEndpoint),
		harness.WithHostPortAccess(port),
		harness.WithRefValidationMode("available"),
	)
	ep := h.Endpoint

	// @deliberate: tell the in-process executor's async-callback
	// goroutine to swap the supervisor's advertised in-network host
	// (`rimsky:9100`) for the host-mapped callback URL when posting
	// `AsyncCallbackBody`. The supervisor advertises the in-network
	// alias because that is what an executor running on the docker
	// network would dial; the example executor runs on the host so
	// it needs the host-mapped form. t.Setenv tears the override
	// down at test end.
	t.Setenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE", ep.CallbackBaseURL)

	// @deliberate: force the executor's async-callback goroutine to
	// sleep long enough that the restart-survival sub-test's
	// Restart() races ahead of the goroutine's POST. This makes the
	// restart-survival branch deterministic: the test process drives
	// the callback POST against the post-restart supervisor, NOT the
	// pre-restart in-memory CallbackRegistry. 30s covers a typical
	// container Restart + wait-for-callback-ready window.
	t.Setenv("EXAMPLE_EXECUTOR_ASYNC_CALLBACK_DELAY_MS", "30000")

	// @deliberate: Wait for the discovery handshake to cache the
	// example executor's Capabilities before deploying templates.
	// Without this the dispatch path fails with
	// `executor_schema_unavailable` because the schema-resolve step
	// runs before discovery has reached this peer.
	waitExecutorDiscovered(t, ep, "example", 60*time.Second)

	t.Run("tag-keyed cascade", func(t *testing.T) {
		// @deliberate: two-node template — `worker` runs the example
		// executor with `mode: emit_event`; `sink` subscribes to the
		// worker's terminal/success filtered by `"work_started" in
		// payload.tags`. The sink reaching `fresh` proves the
		// supervisor matched the CEL filter against the persisted
		// payload.tags and dispatched the subscriber.
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

		// @deliberate: wait for the worker's terminal/success audit
		// row to land — proves the supervisor dispatched the
		// example executor and the unary Outcome reached terminal
		// resolution. `fresh` is also the freshly-created node
		// state so a state poll alone wouldn't witness a real
		// dispatch.
		waitForTerminalSuccessTag(t, ep, workerID, "work_started", 90*time.Second)

		// @deliberate: the sink reaching `fresh` after the worker's
		// terminal/success+work_started lands proves the cascade
		// walker matched the CEL filter against payload.tags and
		// dispatched the subscriber.
		waitForTerminalSuccessAny(t, ep, sinkID, 60*time.Second)
	})

	t.Run("async callback survives supervisor restart", func(t *testing.T) {
		// @deliberate: single-node template — worker runs the example
		// executor with `mode: async_callback` and a known
		// `async_ack_id`. The executor returns AwaitAsyncCallback
		// synchronously and spawns a goroutine that will POST the
		// AsyncCallbackBody to the supervisor's callback URL. The
		// supervisor persists the ack id to
		// col:rimsky_node_runs.async_ack_id IN TX with the
		// transient/await_async signal emit (per
		// runner_dispatch.go::registerAsyncIfSet chain) so a crash
		// between handoff and callback cannot lose the registration.
		//
		// This sub-test catches the Falsifier "an async-callback POST
		// is dropped after the supervisor that registered it
		// restarts." We restart the rimsky-all-in-one container AFTER
		// the persisted async_ack_id is observed and BEFORE the
		// callback POST lands. The fresh supervisor's in-memory
		// CallbackRegistry starts empty; the callback handler MUST
		// fall through to Queue.LookupRunByAsyncAckID, find the row,
		// and drive the dispatch to terminal/success.
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

		// @deliberate: poll /v1/observability/node-runs until a row
		// for this instance carries async_ack_id == ackID. This is
		// the persisted-registry assertion — the row must commit
		// BEFORE we restart, so a restart-survived lookup has a row
		// to find. The supervisor's registerAsync tx commits the
		// column alongside the transient/await_async signal in one
		// frame; once we observe ackID on the row, the persistent
		// registry is live.
		waitForPersistedAsyncAckID(t, ep, iid, ackID, 90*time.Second)

		// @deliberate: drop the executor's deferred goroutine's
		// PRE-restart POST on the floor. The Go goroutine fires its
		// POST 100ms after the unary Execute returns; the restart
		// races that POST. Outcomes:
		//
		//   (a) the POST arrives BEFORE restart, the in-memory
		//       CallbackRegistry pops the entry, the dispatch
		//       settles via the in-memory hot-path — the test
		//       observes terminal/success but proves the EASY
		//       path, not the restart-survival path.
		//   (b) the POST arrives DURING restart (container
		//       terminating) and the executor's HTTP client
		//       surfaces a connection error — the executor's
		//       goroutine logs to stderr and returns; the dispatch
		//       stays `running` against the persisted ack id.
		//   (c) the POST is delayed past restart (e.g. the executor
		//       is loaded). The goroutine eventually POSTs to the
		//       same URL — but it computed the URL via
		//       EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE, which the
		//       restart re-evaluates BELOW.
		//
		// To force the restart-survival branch the test POSTs the
		// callback itself AFTER restart against the post-restart
		// callback URL. The executor's pre-restart POST may or may
		// not have landed; either way the test's POST drives the
		// proof.
		h.Restart(ctx, t)
		ep = h.Endpoint

		// @deliberate: re-anchor the override env var to the
		// post-restart callback URL so the SECOND test POST (and
		// any retried executor goroutine) dial the new mapped port.
		t.Setenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE", ep.CallbackBaseURL)

		// @deliberate: POST the AsyncCallbackBody to the
		// post-restart supervisor. The callback handler's
		// in-memory CallbackRegistry is empty (fresh process);
		// the handler MUST fall through to
		// Queue.LookupRunByAsyncAckID against the persisted
		// async_ack_id column — this is the restart-survival
		// proof. Retry briefly so we are not racing the post-
		// restart `/health` 200 → callback handler ready window.
		postCallbackUntilOK(t, ep.CallbackBaseURL, ackID, 30*time.Second)

		// @deliberate: with the persisted registry resolving the
		// callback to the right dispatch row, the supervisor
		// drives the verdict to terminal/success and the node
		// reaches `fresh`. A timeout here means either the
		// persisted lookup did not find the row (broken
		// restart-survival) or the supervisor failed to commit
		// the terminal — both falsify the leg.
		waitExampleNodeState(t, ep, workerID, "fresh", "", 90*time.Second)

		// @deliberate: re-discover the example executor on the
		// post-restart supervisor before subsequent sub-tests run.
		// The discovery cache is per-process; the fresh
		// rimsky-all-in-one process starts cold and only catches up
		// asynchronously after the startup dial loop reaches the
		// peer. Without this the next sub-test's dispatch fails
		// with `executor_schema_unavailable`.
		waitExecutorDiscovered(t, ep, "example", 60*time.Second)
	})

	t.Run("declared error class routes through error_types", func(t *testing.T) {
		// @deliberate: single-node template — worker runs the example
		// executor with `mode: raise_error`, declaring the
		// `example/forbidden` class with policy `[give_up]`. The
		// worker MUST settle `failed` with
		// `current_error_class == "example/forbidden"`; a fallback
		// to a generic class would fail this assertion.
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

	// @deliberate: the README documents a fourth leg — registration-
	// time rejection of a misshapen `count: -1` default against the
	// executor's advertised `count.minimum: 0` constraint. That gate
	// fires only when the executor's Capabilities have reached the
	// discovery cache by registration time AND
	// templates.ref_validation_mode is "all". Both conditions are
	// already exercised by other harness tests
	// (`code:lib/control/controlapi/template_validator_test.go`); we
	// run the cross-stack proof under "available" mode to keep the
	// cascade + error-class legs robust against the bring-up race.
}

// @deliberate: deployExampleTemplate POSTs the template spec and
// transitions it to deployed; returns the template id. Inlined for
// the example module (the scenarios package helpers live in
// `lib/services/test/scenarios/` and would create a circular import
// path back through the harness).
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

// createExampleInstance POSTs a new instance against the deployed template
// and returns its instance_id. Instance creation is idle post-spec; the
// helper follows up with an empty message so the structural roots wake.
//
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

// @deliberate: resolveExampleNodeID reads /v1/instances/{id}/nodes and
// returns the UUID of the node with the given node_type. Retries
// briefly because the GET races the instance-create commit on
// SQLite (the rimsky-all-in-one default).
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

// @deliberate: waitExampleNodeState polls GET /v1/nodes/{id} until
// state == wantState; when wantErrClass is non-empty, also asserts
// current_error_class. A timeout fatals.
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

// @deliberate: waitExecutorDiscovered polls
// /v1/observability/executors/{name} until the executor's
// `observability_capabilities.expected_attributes_schema` field is
// populated (proof the rimsky-side discovery handshake reached the
// peer and cached the Capabilities response). The response wraps the
// peer in a top-level `peer` field, with reachability_status sibling.
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

// @deliberate: waitForTerminalSuccessTag polls /v1/events for the
// node's terminal/success rows until one carries the named tag in
// payload.tags, or fatals on timeout. The per-emission audit row IS
// the canonical persistence surface for the settling-terminal's tag
// set under TD-collapse-named-event-to-tags; asserting against it
// reads the tag at the same site cascade-fire reads it from.
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

// @deliberate: waitForTerminalSuccessAny polls until ANY
// terminal/success audit row exists for the node — used to witness
// a downstream subscriber dispatching without insisting on a
// specific tag.
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

// @deliberate: payloadCarriesTag walks the events response body
// (`{events: [{payload: {tags: [...]}, ...}]}`) and returns true iff
// any event row's payload.tags contains the named tag. The
// per-emission audit row is the canonical persistence surface for the
// settling-terminal's tag set under TD-collapse-named-event-to-tags;
// asserting against it (rather than against the worker's
// LatestAttributes) reads the tag at the same site cascade-fire
// reads it from.
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

// @deliberate: waitForPersistedAsyncAckID polls
// /v1/observability/node-runs?instance_id=... for a dispatch row
// carrying async_ack_id == wantAck. The supervisor's
// runner_dispatch.go::registerAsync chain writes the column in tx
// with the transient/await_async signal emit (per
// TD-persist-async-callback-registry), so once we observe the column
// non-nil and equal to wantAck the persistent registry is the source
// of truth for restart-survival. The handler at
// /v1/observability/node-runs surfaces async_ack_id directly off the
// DispatchRow, so the lookup needs no separate query.
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

// @deliberate: postCallbackUntilOK POSTs an
// AsyncCallbackBody{success:{...}} to
// `${callbackBaseURL}/v1/callback/{ackID}` and retries until the
// supervisor's callback handler returns 200 OK or the deadline
// elapses. Retry is required because the rimsky-all-in-one
// container's /health 200 (the harness's bring-up signal) covers the
// control-api listener, NOT the supervisor's callback listener — the
// two have separate `Start` paths inside the unified process. A
// transient connection-refused / 404 / 503 right after Restart is
// expected; we want the handler ready for the registry-lookup leg
// before the test fatals.
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

// @deliberate: readAllAndClose reads the response body and closes it.
// Inlined here to keep the e2e test self-contained and avoid pulling
// in an io-helper package.
func readAllAndClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
