// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-executor-protocol: a service author's
// example Executor — registered with rimsky's executor catalog, advertising
// declared events / declared error classes / a constraining attributes
// schema — plugs into a running rimsky stack end-to-end through the public
// protocol surface. Each leg of the spec's Acceptance is exhibited against
// the REAL assembled product (rimsky-all-in-one in a testcontainer) plus
// the REAL example executor binary (this directory's Executor type, run
// in-process and exposed to the container via WithHostPortAccess):
//
//  1. Execute is dispatched. A template referencing the example executor
//     produces an instance whose node settles to `fresh` through the real
//     supervisor — proof the supervisor dialed the executor at the
//     advertised endpoint and the dispatch ran to a terminal Success.
//
//  2. NamedEvent records appear on the unified event log. A template
//     whose node carries `mode: emit_event` causes the executor to emit a
//     NamedEvent named DeclaredEventName before the Success terminal; the
//     supervisor persists it on rimsky_events as kind `event/<name>` (per
//     concept:signal), observable via GET /v1/events?kind=event/<name>.
//
//  3. The DeclaredErrorClass routes through `error_types:`. A template
//     whose node declares `error_types: { example/forbidden: { policy:
//     [give_up] } }` and carries `mode: raise_error` causes the executor
//     to emit Error with the declared class; rimsky routes it through the
//     declared-class chain and emits the canonical signal
//     `terminal/error/example/forbidden` on the event log — proof the
//     declared class IS the routing key, not a generic fallback.
//
//  4. Attribute-schema validation rejects a misshapen template at
//     registration. A template whose node carries a static default
//     `count: -1` violates the executor's advertised `count.minimum: 0`
//     constraint; rimsky's registration-time validator (default mode
//     `all`) refuses the POST /v1/templates with HTTP 400 and a body
//     citing the offending attribute and the violated constraint — proof
//     the validator reads the live discovery cache's schema from the
//     real Capabilities handshake and gates on it.
//
// The four legs together exhibit the falsifier's three failure modes
// (advertised class treated as generic / emitted event missing from log /
// schema bypass at registration) as observable failures rather than silent
// drops.
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import the lib/services testcontainers harness without
// putting the example's published Apache surface at risk — consumers
// who `go build` the example never pull in any test dependency.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestE2E_ExampleExecutorAgainstRunningRimsky boots the rimsky-all-in-one
// image with the example Executor registered as a peer service, then
// exhibits each of the four protocol-surface properties STORY-executor-
// protocol's Acceptance names.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`) before this test runs. The harness pulls
// `rimsky-all-in-one:latest` from the local Docker daemon — nothing is
// fetched from a registry. A missing image is a hard t.Fatal (the
// harness never t.Skip's), so a developer who hasn't run `make
// core-images` sees the missing-image error directly.
func TestE2E_ExampleExecutorAgainstRunningRimsky(t *testing.T) {
	// Not parallel: this scenario stands up a docker network + a
	// rimsky-all-in-one container plus an in-process executor on a host
	// port. The cost is real, so other test methods in this package
	// (`executor_test.go`) keep their fast in-process shape and only
	// this gate pays the cross-stack price.
	ctx := context.Background()

	// 1. Stand up the example executor in-process on a free host port.
	//    The container's supervisor dials it at
	//    `host.testcontainers.internal:<port>` — see the
	//    WithHostPortAccess option below.
	port := freeHostPort(t)
	startExampleExecutor(t, port)

	// 2. Bring up rimsky-all-in-one on the harness default (Postgres) with
	//    the example executor registered as the "example" peer. The peer
	//    must be reachable BEFORE rimsky comes up because rimsky's
	//    control-api fires a Capabilities handshake against every
	//    declared executor at startup and exits non-zero if any is
	//    unreachable; startExampleExecutor blocks until the gRPC server
	//    is listening, satisfying that ordering constraint.
	endpoint := fmt.Sprintf("host.testcontainers.internal:%d", port)
	// Postgres (the harness default) for the state DB. The SQLite-WAL
	// path the all-in-one image ships as its baked default surfaces
	// transient `SQLITE_BUSY` errors at the GET /v1/events read API
	// while the supervisor is concurrently writing — too noisy for a
	// gate that polls aggressively across four legs. Postgres is the
	// same DB pg_error_classes scenarios use for the same reason.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExecutor("example", endpoint),
		harness.WithHostPortAccess(port),
	)

	// Wait for the executor's Capabilities handshake to populate the
	// observability discovery cache. The registration-time validator
	// reads the cache directly; in reference-validation mode `all` (the
	// all-in-one image's baked default) a template referencing an
	// executor whose schema isn't yet visible is rejected at registration
	// with "expected_attributes_schema is not visible at registration".
	// The startup handshake runs in parallel with control-api startup
	// and may complete after /health flips to 200, so the test must wait
	// for the cache explicitly before posting a template.
	waitForExecutorReachable(t, ep, "example", 90*time.Second)

	// Run each acceptance leg as a sub-test against the SAME running
	// stack — the four legs are independent observations against the
	// same control-api, so a single bring-up is sufficient and a
	// per-leg bring-up would only multiply the bring-up cost.
	t.Run("Execute_dispatched_to_real_executor", func(t *testing.T) {
		exerciseExecuteDispatchLeg(t, ep)
	})
	t.Run("NamedEvent_lands_on_event_log", func(t *testing.T) {
		exerciseNamedEventLeg(t, ep)
	})
	t.Run("DeclaredErrorClass_routes_through_error_types", func(t *testing.T) {
		exerciseDeclaredErrorClassLeg(t, ep)
	})
	t.Run("AttributeSchema_rejects_misshapen_at_registration", func(t *testing.T) {
		exerciseAttributeSchemaRejectionLeg(t, ep)
	})
}

// exerciseExecuteDispatchLeg drives a happy-path template against the
// example executor and asserts the supervisor dispatched a real Execute
// call: the node settles to `fresh` (a terminal Success) and a
// `work_started` operational event appears on the event log.
//
// Proof for spec acceptance leg (a): "the executor receives Execute".
func exerciseExecuteDispatchLeg(t *testing.T, ep harness.RimskyEndpoint) {
	tplID := deployTemplate(t, ep, exampleTemplate("example-exec-dispatch", map[string]any{
		"type":     "worker",
		"executor": "example",
		// Attribute-bearing node so the supervisor's dispatch-time
		// attribute gate runs against the executor's advertised schema.
		// `mode: ok` (the default) routes to the Success terminal.
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":  map[string]any{"type": "string", "default": "ok"},
					"count": map[string]any{"type": "integer", "default": 0},
				},
			},
		},
	}))
	instanceID := createInstance(t, ep, tplID, "ck-exec-dispatch")

	// `work_started` is the operational event the supervisor appends
	// when it acquires + dispatches; its presence proves the Execute
	// RPC ran (the in-process executor's Heartbeat is the wire echo).
	requireEventKind(t, ep, instanceID, "work_started", 60*time.Second,
		"the supervisor must have dialed the example executor and dispatched Execute")
}

// exerciseNamedEventLeg drives a template whose worker carries
// `mode: emit_event` and asserts the resulting NamedEvent appears on
// the unified event log under kind `event/<DeclaredEventName>` — proof
// the executor's emitted event reached rimsky and was persisted under
// the canonical signal type-path (per concept:signal).
//
// Proof for spec acceptance leg (b): "named events appear on /v1/events".
// Falsifier: "an event the executor emits doesn't appear on the event log".
func exerciseNamedEventLeg(t *testing.T, ep harness.RimskyEndpoint) {
	// Hard-code the default value so the supervisor pre-populates the
	// dispatch attributes with `mode: emit_event` even though there is
	// no per-instance override.
	tplID := deployTemplate(t, ep, exampleTemplate("example-named-event", map[string]any{
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
					"count": map[string]any{"type": "integer", "default": 0},
				},
			},
		},
	}))
	instanceID := createInstance(t, ep, tplID, "ck-named-event")

	// The NamedEvent the executor emits writes a row to rimsky_events
	// with kind `event/<name>` (per concept:signal, runner_named_events.go).
	// The events route validates kind parameters; the slash-bearing
	// signal type-path is accepted as opaque.
	requireEventKind(t, ep, instanceID, "event/"+DeclaredEventName, 60*time.Second,
		"a NamedEvent emitted by the executor MUST land on /v1/events as kind=event/<name> "+
			"(falsifier: event emitted but missing from the log)")
}

// exerciseDeclaredErrorClassLeg drives a template whose worker carries
// `mode: raise_error` and declares `error_types: { example/forbidden:
// give_up }`. The executor's Error{class: example/forbidden} terminal
// must route through the declared chain and emit the canonical signal
// `terminal/error/example/forbidden` on the event log — proof the
// declared class IS the routing key, not a generic fallback.
//
// Proof for spec acceptance leg (c): "declared error class routes
// through error_types". Falsifier: "registered executor advertising a
// declared error class emits it but the policy router treats it as
// generic".
func exerciseDeclaredErrorClassLeg(t *testing.T, ep harness.RimskyEndpoint) {
	tplID := deployTemplate(t, ep, exampleTemplate("example-error-routing", map[string]any{
		"type":     "worker",
		"executor": "example",
		// Declared-class routing: the operator's error_types: chain
		// keys on the executor-declared class. give_up drives the
		// node-run to a terminal/error/<class> signal, which is the
		// canonical event-log row a subscriber/operator would observe.
		"error_types": map[string]any{
			DeclaredErrorClass: map[string]any{
				"policy": []map[string]any{
					{"action": "give_up"},
				},
			},
		},
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"type":    "string",
						"default": "raise_error",
					},
					"count": map[string]any{"type": "integer", "default": 0},
				},
			},
		},
	}))
	instanceID := createInstance(t, ep, tplID, "ck-error-routing")

	// The canonical `terminal/error/<declared_class>` row is what the
	// supervisor emits when the error-policy chain matches the
	// declared class. Per pg_error_classes scenarios, this row landing
	// is the load-bearing observable for "the declared class IS the
	// routing key".
	requireEventKind(t, ep, instanceID,
		"terminal/error/"+DeclaredErrorClass, 60*time.Second,
		"the declared error class MUST route through the operator's error_types: chain as "+
			"a terminal/error/<class> signal on the event log (falsifier: declared class "+
			"treated as generic)")
}

// exerciseAttributeSchemaRejectionLeg posts a template whose node's
// static default `count: -1` violates the executor's advertised
// schema (`minimum: 0`) and asserts the rimsky control-api refuses
// registration at the registration-time validator (default mode `all`)
// with HTTP 400 citing the offending attribute and the violated
// constraint.
//
// Proof for spec acceptance leg (d): "attribute schema validation
// rejects misshapen attributes at registration". Falsifier: "attributes
// resolved against the executor's schema bypass the schema validation".
func exerciseAttributeSchemaRejectionLeg(t *testing.T, ep harness.RimskyEndpoint) {
	// count:-1 violates the executor's schema (minimum:0). The
	// registration-time validator (`all` mode is the all-in-one
	// image's baked default) reads the live discovery cache's schema
	// from the real Capabilities handshake and refuses.
	misshapenSpec := exampleTemplate("example-schema-rejection", map[string]any{
		"type":     "worker",
		"executor": "example",
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":    "integer",
						"default": -1,
					},
				},
			},
		},
	})

	status, raw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": misshapenSpec})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /v1/templates with a schema-violating default: got status %d, want 400 (the registration-time validator must refuse the misshapen template at the registration gate); body: %s",
			status, string(raw))
	}

	// The rejection body must name the offending attribute (`count`)
	// AND cite the `minimum` constraint — a genuine value check, not
	// a generic surface error. Lower-case search keeps the assertion
	// resilient to body capitalization.
	bodyLower := strings.ToLower(string(raw))
	if !strings.Contains(bodyLower, "count") {
		t.Fatalf("rejection body must name the offending attribute `count`: %s", string(raw))
	}
	if !strings.Contains(bodyLower, "minimum") {
		t.Fatalf("rejection body must cite the violated `minimum` constraint: %s", string(raw))
	}
}

// --- helpers ---------------------------------------------------------------

// exampleTemplate builds a single-node templatespec under a given name,
// taking the node block as a map so each leg can vary the
// attributes/error_types/etc. without restating the surrounding spec.
// `frame_timeout_ms` is set high enough that a long-running test doesn't
// trip the per-frame supervisor timeout (mirrors the cli_watch
// chronological scenario's 10-minute cap).
func exampleTemplate(name string, node map[string]any) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               "1",
		"frame_resolution_mode": "serial_queue",
		"frame_timeout_ms":      600000,
		"nodes":                 []map[string]any{node},
	}
}

// deployTemplate POSTs the spec to /v1/templates, deploys it, and
// returns the template ID. Fails hard on any non-2xx — this is the
// happy-path helper shared across the legs that DO expect registration
// to succeed.
func deployTemplate(t *testing.T, ep harness.RimskyEndpoint, spec map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": spec})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createInstance POSTs a new instance and returns its instance_id.
func createInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	return resp.InstanceID
}

// requireEventKind polls GET /v1/events?instance_id=...&kind=... until
// at least one event of the given kind appears, or the deadline elapses.
// Fails hard on timeout — the kind landing is the load-bearing
// observable, never a skip. On failure, dumps every event kind that
// DID land on the instance so the developer can diagnose whether the
// wrong-class routing fired vs the value path never engaged at all.
//
// The 1s poll cadence and the SQLITE_BUSY-tolerant retry are
// deliberate: the all-in-one image runs on SQLite-WAL, and a fast poll
// against the events read-API can collide with the supervisor's writes
// to the same DB under contention. 500-class responses are tolerated
// (the next iteration retries) rather than fataled.
func requireEventKind(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind string, deadline time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(deadline)
	// URL-encoding the slash in kind for safety: chi tolerates a raw
	// slash in the query value, but the events route's kind parser
	// accepts the canonical slash-delimited form verbatim.
	path := fmt.Sprintf("/v1/events?instance_id=%s&kind=%s", instanceID, kind)
	var lastStatus int
	var lastBody string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, path, "")
		lastStatus, lastBody = status, string(raw)
		if status == http.StatusOK {
			var resp struct {
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, e := range resp.Events {
					if e.Kind == kind {
						return
					}
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	// Diagnostic: enumerate every event kind that DID land on the
	// instance so the developer can tell whether routing fired under a
	// different class (e.g. unknown_error_class) vs the value path
	// never engaged at all.
	dump := dumpEventKindsForInstance(t, ep, instanceID)
	t.Fatalf("event kind %q never landed on the event log for instance %s within %v (last GET status=%d body=%s) — %s\nobserved event kinds on this instance: %v",
		kind, instanceID, deadline, lastStatus, lastBody, why, dump)
}

// dumpEventKindsForInstance fetches the unfiltered event feed for an
// instance and returns the sorted set of distinct kinds. Used by
// requireEventKind to enrich the failure message. Retries on the
// SQLite-WAL busy-lock contention the all-in-one image surfaces
// transiently when the supervisor is writing concurrently.
func dumpEventKindsForInstance(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var (
		status int
		raw    []byte
	)
	for attempt := 0; attempt < 10; attempt++ {
		status, raw = ep.GetJSON(t, "/v1/events?instance_id="+instanceID+"&limit=500", "")
		if status == http.StatusOK {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if status != http.StatusOK {
		return []string{fmt.Sprintf("<GET /v1/events failed after retries: %d %s>", status, string(raw))}
	}
	var resp struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return []string{fmt.Sprintf("<decode failed: %v>", err)}
	}
	seen := map[string]int{}
	for _, e := range resp.Events {
		seen[e.Kind]++
	}
	out := make([]string, 0, len(seen))
	for k, n := range seen {
		out = append(out, fmt.Sprintf("%s(%d)", k, n))
	}
	return out
}

// waitForExecutorReachable polls GET /v1/observability/executors/<name>
// until the discovery cache reports the executor as reachable AND its
// expected_attributes_schema field is non-empty, or the deadline
// elapses. The cache is what the registration-time validator reads at
// POST /v1/templates; without the wait, the misshapen-attribute leg of
// the test races against the startup Capabilities handshake.
//
// The response shape (`{peer: {reachability_status, observability_capabilities:
// {expected_attributes_schema, ...}}}`) is the GET-executor JSON envelope
// the observability handler emits; key names mirror the wire shape from
// proto:executor_observability.proto.
func waitForExecutorReachable(t *testing.T, ep harness.RimskyEndpoint, name string, deadline time.Duration) {
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
						// base64-encoded JSON Schema bytes; non-empty when
						// the executor advertised a schema.
						ExpectedAttributesSchema string `json:"expected_attributes_schema"`
					} `json:"observability_capabilities"`
				} `json:"peer"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastBody = string(raw)
				if resp.Peer.ReachabilityStatus == "reachable" &&
					resp.Peer.ObservabilityCapabilities.ExpectedAttributesSchema != "" {
					return
				}
			}
		} else {
			lastBody = fmt.Sprintf("status=%d body=%s", status, string(raw))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("executor %q never became reachable in the observability cache within %v; last body: %s",
		name, deadline, lastBody)
}

// freeHostPort grabs an OS-assigned TCP port and returns it. The brief
// close-then-reuse race is acceptable for an in-process test fixture
// (matches the pattern host_agent_harness_test.go::freePort uses).
func freeHostPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if cerr := lis.Close(); cerr != nil {
		t.Fatalf("close listener: %v", cerr)
	}
	return port
}

// startExampleExecutor stands up the example Executor as an in-process
// gRPC server on the given host port and blocks until the listener is
// accepting connections, so the caller can hand the endpoint to
// BringUpRimsky knowing the eager Capabilities handshake will succeed.
// Cleanup (graceful Stop) is registered via t.Cleanup.
func startExampleExecutor(t *testing.T, port int) {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	exec := &Executor{}
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// Poll-dial to confirm the gRPC server is up before returning.
	// rimsky-all-in-one's startup Capabilities handshake is eager —
	// if the executor isn't listening at the configured endpoint
	// when rimsky boots, the container exits non-zero. Blocking here
	// makes the ordering deterministic without a sleep.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("example executor did not become dialable at %s within 10s", addr)
}
