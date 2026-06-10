// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-lifecycle-subscriber-author: a service
// author's example LifecycleSubscriber — implementing all seven of the
// protocol's callbacks (OnTemplateRegistered / OnTemplateDeployed /
// OnTemplateUndeployed / OnTemplateDeregistered / OnInstanceCreated /
// OnInstanceTerminated / OnRunScopeTerminal) — plugs into a running
// rimsky stack end-to-end and receives each callback at the corresponding
// lifecycle transition with the documented context fields populated.
//
// The seven-callback walk is exhibited against the REAL assembled product
// (rimsky-all-in-one in a testcontainer, Postgres state DB) plus the REAL
// example Subscriber type (this directory's Subscriber, run in-process
// behind a thin recording wrapper that captures each call without
// modifying the example):
//
//  1. OnTemplateRegistered fires at POST /v1/templates with the
//     template_hash AND the canonical JCS-canonicalized spec bytes
//     populated.
//  2. OnTemplateDeployed fires at POST /v1/templates/{hash}/deploy with
//     the template_hash populated.
//  3. OnInstanceCreated fires at POST /v1/instances with instance_id,
//     template_hash, service_bindings, AND owner_api_key_id populated.
//     The test pre-mints an admin api-key and authenticates the create
//     request so owner_api_key_id is non-empty (the anonymous-mode path
//     would surface an empty string per concept:lifecycle-subscriber).
//  4. OnRunScopeTerminal fires at POST /v1/instances/{id}/terminate (the
//     main run-scope close) with run_scope_id, terminal_reason, AND
//     instance_id populated.
//  5. OnInstanceTerminated fires at DELETE /v1/instances/{id} with
//     instance_id, template_hash, AND terminated_at_unix_ms populated.
//  6. OnTemplateUndeployed fires at POST /v1/templates/{hash}/undeploy
//     with the template_hash populated.
//  7. OnTemplateDeregistered fires at DELETE /v1/templates/{hash} with
//     the template_hash populated.
//
// An eighth leg drives a callback whose subscriber returns a non-nil
// error and asserts rimsky honors the failure synchronously: the
// triggering HTTP request returns 5xx and the rimsky-side row mutation
// did NOT happen (fire-and-forget is the spec's named falsifier).
//
// The example's Subscriber type is the value-delivering component —
// the wrapper delegates each callback to it before recording the
// captured envelope, so a regression that turns the example into a
// returns-error stub is observable here. The wrapper also implements a
// minimal ClaimProducer surface (Capabilities → write_semantics: sync;
// Open → Unavailable; Commit/Abandon/Release → no-op) so rimsky's
// startup Capabilities handshake passes on the same gRPC server. The
// runtime claim verbs are never invoked because the template's node
// returns the executor's Success terminal before any claim acquisition
// reaches the producer.
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
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestE2E_ExampleLifecycleSubscriberAgainstRunningRimsky boots the
// rimsky-all-in-one image with the example Subscriber registered as a
// lifecycle-subscriber peer (mixed into a minimal claim_producer entry)
// plus a stub executor, then walks the seven-callback lifecycle and
// exhibits each context-field property STORY-lifecycle-subscriber-author's
// Acceptance names.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`) before this test runs. The harness pulls
// `rimsky-all-in-one:latest` from the local Docker daemon — nothing is
// fetched from a registry. A missing image is a hard t.Fatal (the
// harness never t.Skip's), so a developer who hasn't run `make
// core-images` sees the missing-image error directly.
func TestE2E_ExampleLifecycleSubscriberAgainstRunningRimsky(t *testing.T) {
	// Not parallel: this scenario stands up a docker network + a Postgres
	// testcontainer + a rimsky-all-in-one container plus an in-process
	// subscriber + executor on host ports. The cost is real, so other
	// test methods in this package (subscriber_test.go) keep their fast
	// in-process shape and only this gate pays the cross-stack price.
	ctx := context.Background()

	// 1. Stand up the example Subscriber, wrapped to record each call,
	//    as an in-process gRPC server on a free host port. The wrapper
	//    also serves the minimal ClaimProducer surface rimsky requires
	//    to pass its startup Capabilities handshake on a peer entry
	//    that advertises [claim_producer, lifecycle_subscriber].
	subPort := freeHostPort(t)
	rec := startRecordingLifecycleSubscriber(t, subPort)

	// 2. Stand up a Success-returning stub executor on a SEPARATE host
	//    port. The template's worker node uses this executor so the
	//    instance can reach terminal end-to-end through real dispatch —
	//    OnRunScopeTerminal and OnInstanceTerminated only fire on a
	//    real terminate path. A canned executor is enough: the
	//    load-bearing observable is the lifecycle callbacks, not what
	//    the executor returned.
	execPort := freeHostPort(t)
	startStubExecutor(t, execPort)

	// 3. Bring up rimsky-all-in-one on the harness default (Postgres)
	//    with the subscriber registered as an executor peer advertising
	//    the lifecycle_subscriber mix-in AND a separate stub executor.
	//    The subscriber rides on an executor entry (not a claim_producer)
	//    on purpose: the claim-producer path's StartScheduler dial runs
	//    a BLOCKING Capabilities handshake at rimsky startup and races
	//    the reverse-SSH host-port tunnel under load (per
	//    claimproducer_custom.go's preamble), which surfaces as
	//    "connection refused" against the in-process subscriber. The
	//    executor entry's observability handshake is best-effort and the
	//    LifecycleClient dial is non-blocking (DialLifecycle does
	//    grpc.NewClient without a Capabilities call), so the first
	//    actual lifecycle round-trip happens at template-register time
	//    — long after the SSH tunnel is up.
	subEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", subPort)
	execEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", execPort)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExecutor("example-subscriber", subEndpoint),
		harness.WithExecutorProtocols("example-subscriber", "lifecycle_subscriber"),
		harness.WithExecutor("stub", execEndpoint),
		harness.WithHostPortAccess(subPort, execPort),
		// The worker node references the `stub` executor by name. The
		// example-subscriber executor is referenced by no node directly
		// — it appears in peersReferencedBySpec via the worker node's
		// `stores: [{name: example-subscriber}]` ref, which is the
		// channel rimsky's lifecycle-fan-out walks. Strict "all" mode
		// requires every referenced peer's schema visible at
		// registration; the stub advertises an open schema and the
		// subscriber's Capabilities advertise an open schema too, but
		// the SSH-tunnel race may leave the discovery cache marking
		// either as Unreachable on a slow first probe; "none" sidesteps
		// the registration-time gate without affecting the dispatch
		// path (which uses the subsequent live-cache probes).
		harness.WithRefValidationMode("none"),
	)

	// Carries the IDs captured in leg 1 and reused by legs 2..7.
	state := &lifecycleState{}

	// Each leg runs against the SAME running stack — the lifecycle
	// callbacks fire in a strict natural order (register → deploy →
	// instance-create → terminate → instance-delete → undeploy →
	// deregister), so per-leg bring-up would only multiply the cost.
	t.Run("OnTemplateRegistered_fires_on_template_create", func(t *testing.T) {
		exerciseOnTemplateRegisteredLeg(t, ep, rec, state)
	})
	t.Run("OnTemplateDeployed_fires_on_deploy", func(t *testing.T) {
		exerciseOnTemplateDeployedLeg(t, ep, rec, state)
	})
	t.Run("OnInstanceCreated_carries_owner_and_bindings", func(t *testing.T) {
		exerciseOnInstanceCreatedLeg(t, ep, rec, state)
	})
	t.Run("OnRunScopeTerminal_fires_on_terminate_with_context", func(t *testing.T) {
		exerciseOnRunScopeTerminalLeg(t, ep, rec, state)
	})
	t.Run("OnInstanceTerminated_fires_on_delete", func(t *testing.T) {
		exerciseOnInstanceTerminatedLeg(t, ep, rec, state)
	})
	t.Run("OnTemplateUndeployed_fires_on_undeploy", func(t *testing.T) {
		exerciseOnTemplateUndeployedLeg(t, ep, rec, state)
	})
	t.Run("OnTemplateDeregistered_fires_on_delete", func(t *testing.T) {
		exerciseOnTemplateDeregisteredLeg(t, ep, rec, state)
	})
	t.Run("Subscriber_failure_is_honored_synchronously", func(t *testing.T) {
		exerciseFailureHonoredSynchronouslyLeg(t, ep, rec, state)
	})
}

// lifecycleState carries the per-test-run IDs the legs share, plus a
// shared "before terminate" baseline so the OnInstanceTerminated leg
// can match a callback the InstanceTerminator worker may fire ahead of
// the explicit DELETE — the worker polls every terminated instance and
// fans out OnInstanceTerminated on its own tick. Snapshotting once
// pre-terminate lets either leg observe the same call.
type lifecycleState struct {
	templateHash      string
	instanceID        string
	preTerminateIndex int
	// adminKey is the bootstrap admin plaintext minted in leg 3.
	// Anonymous mode is open before this is set; afterwards, every
	// request must carry the bearer so the auth gate accepts it.
	adminKey string
}

// ---------------------------------------------------------------------------
// Leg 1: OnTemplateRegistered fires on POST /v1/templates with the
// template_hash AND the canonical spec bytes populated.
// ---------------------------------------------------------------------------

func exerciseOnTemplateRegisteredLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	state.templateHash = registerLifecycleTemplate(t, ep)

	// Wait for the callback to arrive — synchronous from
	// FanOutTemplateEvent, but the test runs in the same goroutine as
	// the HTTP response, so the captured row is available by the time
	// the response returns. A short poll absorbs any concurrent
	// scheduling lag without false-failing.
	call := waitForCall(t, rec, "OnTemplateRegistered", before, 30*time.Second,
		"OnTemplateRegistered must fire synchronously on POST /v1/templates")

	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateRegistered template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
	// The canonical JCS-canonicalized spec bytes must be carried on the
	// callback — the lifecycle.proto comment says "may be empty" but
	// rimsky populates it from canonical.CanonicalSpecBytes(spec) on
	// the register fan-out, so non-empty is the actual contract.
	if len(call.Spec) == 0 {
		t.Fatalf("OnTemplateRegistered carried empty spec bytes — the JCS-canonicalized template spec must be populated (lifecycle.proto OnTemplateRegisteredRequest.spec); falsifier: documented context field is missing from the callback payload")
	}
}

// ---------------------------------------------------------------------------
// Leg 2: OnTemplateDeployed fires on POST /v1/templates/{hash}/deploy.
// ---------------------------------------------------------------------------

func exerciseOnTemplateDeployedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	// Anonymous mode is still open at this point (admin key is minted
	// in leg 3); pass empty bearer.
	deployLifecycleTemplate(t, ep, "", state.templateHash)
	call := waitForCall(t, rec, "OnTemplateDeployed", before, 30*time.Second,
		"OnTemplateDeployed must fire on POST /v1/templates/{hash}/deploy")
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateDeployed template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
}

// ---------------------------------------------------------------------------
// Leg 3: OnInstanceCreated fires with instance_id, template_hash,
// service_bindings, AND owner_api_key_id populated.
// ---------------------------------------------------------------------------

func exerciseOnInstanceCreatedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	// Bootstrap the auth perimeter so owner_api_key_id can be observed
	// non-empty on the callback. `rimsky auth init` on a fresh
	// deployment is the supported bootstrap path; this test calls the
	// HTTP equivalent directly so the test process holds the plaintext
	// for the authenticated POST /v1/instances. From here on every
	// subsequent request must carry the bearer — anonymous mode closes
	// the moment the first key is minted.
	state.adminKey = bootstrapAdminKey(t, ep)
	adminKey := state.adminKey

	// Per-instance late-bound service catalog: a non-empty bag the
	// callback should mirror verbatim. The actual key name doesn't
	// matter to rimsky for fan-out (the proxy reads it); what matters
	// is that the callback's service_bindings bytes round-trip.
	bindings := map[string]any{
		"some-service": map[string]any{"endpoint": "grpc://example:9999"},
	}

	before := rec.snapshot()
	state.instanceID = createLifecycleInstance(t, ep, adminKey, state.templateHash, bindings)
	call := waitForCall(t, rec, "OnInstanceCreated", before, 30*time.Second,
		"OnInstanceCreated must fire synchronously on POST /v1/instances")

	if call.InstanceID != state.instanceID {
		t.Fatalf("OnInstanceCreated instance_id mismatch: got %q want %q",
			call.InstanceID, state.instanceID)
	}
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnInstanceCreated template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
	if call.OwnerAPIKeyID == "" {
		t.Fatalf("OnInstanceCreated owner_api_key_id was empty — an authenticated create MUST carry the api-key id so the host-agent-proxy can route dispatches (concept:lifecycle-subscriber); the bootstrap admin key was used to authenticate the create; falsifier: documented context field is missing from the callback payload")
	}
	if len(call.ServiceBindings) == 0 {
		t.Fatalf("OnInstanceCreated service_bindings was empty — the proxy consumes this to populate its per-instance binding cache; the request carried %v; falsifier: documented context field is missing from the callback payload",
			bindings)
	}
	// Round-trip the bag to assert it's the same shape the request sent.
	var got map[string]any
	if err := json.Unmarshal(call.ServiceBindings, &got); err != nil {
		t.Fatalf("OnInstanceCreated service_bindings JSON decode failed: %v; raw=%q", err, string(call.ServiceBindings))
	}
	if _, ok := got["some-service"]; !ok {
		t.Fatalf("OnInstanceCreated service_bindings did not preserve the request's binding key 'some-service': %v", got)
	}
}

// ---------------------------------------------------------------------------
// Leg 4: OnRunScopeTerminal fires on POST /v1/instances/{id}/terminate
// with run_scope_id, terminal_reason, AND instance_id populated.
// ---------------------------------------------------------------------------

func exerciseOnRunScopeTerminalLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	// Capture the pre-terminate baseline once for both the OnRunScopeTerminal
	// and OnInstanceTerminated legs. The InstanceTerminator worker may fire
	// OnInstanceTerminated before the test's explicit DELETE runs; if leg 5
	// re-snapshots after terminate, it could miss the worker's fan-out.
	// Sharing the baseline keeps the assertion timing-independent.
	state.preTerminateIndex = rec.snapshot()
	terminateInstance(t, ep, state.adminKey, state.instanceID, "test_termination_reason")
	call := waitForCall(t, rec, "OnRunScopeTerminal", state.preTerminateIndex, 60*time.Second,
		"OnRunScopeTerminal must fire on POST /v1/instances/{id}/terminate (main run-scope close)")

	if call.InstanceID != state.instanceID {
		t.Fatalf("OnRunScopeTerminal instance_id mismatch: got %q want %q (the host-agent-proxy reaps lazy-spawned children by instance_id, so an empty field breaks reap)",
			call.InstanceID, state.instanceID)
	}
	if call.RunScopeID == "" {
		t.Fatalf("OnRunScopeTerminal run_scope_id was empty — falsifier: documented context field is missing from the callback payload")
	}
	if call.TerminalReason == "" {
		t.Fatalf("OnRunScopeTerminal terminal_reason was empty — the supervisor MUST carry a non-empty reason naming why the scope closed (instance_deleted / instance_killed / etc.); falsifier: documented context field is missing from the callback payload")
	}
}

// ---------------------------------------------------------------------------
// Leg 5: OnInstanceTerminated fires on DELETE /v1/instances/{id}.
// ---------------------------------------------------------------------------

func exerciseOnInstanceTerminatedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	// Use the pre-terminate baseline so a call fired by the
	// InstanceTerminator worker between leg 4's terminate and this leg's
	// DELETE is still observable to the assertion. DELETE is still
	// called: it is the canonical surface for OnInstanceTerminated and
	// the row-delete is required so the template-delete leg can succeed.
	deleteInstance(t, ep, state.adminKey, state.instanceID)
	call := waitForCall(t, rec, "OnInstanceTerminated", state.preTerminateIndex, 60*time.Second,
		"OnInstanceTerminated must fire on DELETE /v1/instances/{id} (or earlier from the InstanceTerminator worker; either site honors the contract)")
	if call.InstanceID != state.instanceID {
		t.Fatalf("OnInstanceTerminated instance_id mismatch: got %q want %q",
			call.InstanceID, state.instanceID)
	}
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnInstanceTerminated template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
	if call.TerminatedAtUnixMs == 0 {
		t.Fatalf("OnInstanceTerminated terminated_at_unix_ms was zero — rimsky must carry the row's terminated_at (lifecycle.proto OnInstanceTerminatedRequest.terminated_at_unix_ms); falsifier: documented context field is missing from the callback payload")
	}
}

// ---------------------------------------------------------------------------
// Leg 6: OnTemplateUndeployed fires on POST /v1/templates/{hash}/undeploy.
// ---------------------------------------------------------------------------

func exerciseOnTemplateUndeployedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	undeployTemplate(t, ep, state.adminKey, state.templateHash)
	call := waitForCall(t, rec, "OnTemplateUndeployed", before, 30*time.Second,
		"OnTemplateUndeployed must fire on POST /v1/templates/{hash}/undeploy")
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateUndeployed template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
}

// ---------------------------------------------------------------------------
// Leg 7: OnTemplateDeregistered fires on DELETE /v1/templates/{hash}.
// ---------------------------------------------------------------------------

func exerciseOnTemplateDeregisteredLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	deregisterTemplate(t, ep, state.adminKey, state.templateHash)
	call := waitForCall(t, rec, "OnTemplateDeregistered", before, 30*time.Second,
		"OnTemplateDeregistered must fire on DELETE /v1/templates/{hash}")
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateDeregistered template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
}

// ---------------------------------------------------------------------------
// Leg 8: rimsky honors the subscriber's failure response synchronously.
// ---------------------------------------------------------------------------

// exerciseFailureHonoredSynchronouslyLeg flips the recording wrapper to
// return a non-nil error on the next OnTemplateRegistered, then POSTs a
// fresh template. The HTTP response MUST be 5xx — rimsky must NOT
// swallow the error and proceed with the row insert; the falsifier
// names "subscriber's failure response on a callback is ignored by
// rimsky (fire-and-forget)". After resetting the failure mode, a
// follow-up POST succeeds so the test's after-state is clean.
func exerciseFailureHonoredSynchronouslyLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	rec.failNextOnTemplateRegistered(status.Error(codes.Internal, "subscriber rejected the callback"))
	defer rec.clearFailures()

	// POST a fresh template (different name → different hash → the
	// idempotency table does NOT short-circuit the fan-out).
	spec := map[string]any{
		"spec": map[string]any{
			"name":                  "lifecycle-subscriber-failure-probe",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"stores": []map[string]any{
						{
							"name":     "example-subscriber",
							"intent":   "r",
							"selector": "probe",
						},
					},
				},
			},
		},
	}
	statusCode, body := ep.PostJSONWithHeaders(t, "/v1/templates", spec, map[string]string{
		"Authorization": "Bearer " + state.adminKey,
	})
	if statusCode >= 200 && statusCode < 300 {
		t.Fatalf("POST /v1/templates with a failing subscriber returned %d — rimsky must surface a 5xx synchronously (the falsifier names fire-and-forget as the failure mode); body: %s",
			statusCode, string(body))
	}
	if statusCode < 500 {
		t.Fatalf("POST /v1/templates with a failing subscriber returned %d — the failure must surface as 5xx (rimsky's FanOutTemplateEvent returns the error and the handler writes 500 with the per-store details); body: %s",
			statusCode, string(body))
	}
	bodyLower := strings.ToLower(string(body))
	if !strings.Contains(bodyLower, "fan-out") && !strings.Contains(bodyLower, "subscriber") {
		t.Fatalf("5xx response body should name the lifecycle fan-out or the subscriber as the cause: %s", string(body))
	}
}

// ---------------------------------------------------------------------------
// Recording lifecycle subscriber + minimal ClaimProducer Capabilities.
// ---------------------------------------------------------------------------

// capturedCall is one observed lifecycle callback. Fields are populated
// per the callback's request shape; unused fields stay at zero-value.
type capturedCall struct {
	Verb               string
	TemplateHash       string
	Spec               []byte
	Tags               []string
	InstanceID         string
	InstanceKey        string
	Params             []byte
	ServiceBindings    []byte
	OwnerAPIKeyID      string
	TerminatedAtUnixMs int64
	RunScopeID         string
	TerminalReason     string
}

// recordingLifecycleSubscriber wraps the example Subscriber, recording
// every callback before delegating to it. The example Subscriber's
// pass-through ack bodies stay the value-delivering component — a
// regression that turns the example into a returning-error stub is
// observable here (legs 1..7 would all fail).
//
// The wrapper also implements the minimal Executor / ExecutorObservability
// surface rimsky probes at startup on a peer entry that advertises
// [executor, lifecycle_subscriber] — Capabilities returns an open
// expected-attributes schema so the discovery probe succeeds, and
// Execute returns Unimplemented (no template references the subscriber
// by name as an executor; the worker node settles via the separate
// `stub` peer). Riding the lifecycle mix-in on an executor entry
// avoids the eager-blocking Capabilities handshake the claim-producer
// path runs at StartScheduler (which races the reverse-SSH host-port
// tunnel under load).
type recordingLifecycleSubscriber struct {
	genv1.UnimplementedLifecycleSubscriberServer
	genv1.UnimplementedExecutorServer
	genv1.UnimplementedExecutorObservabilityServer

	delegate *Subscriber

	mu       sync.Mutex
	calls    []capturedCall
	failNext map[string]error // per-verb sticky failure injection
}

func newRecordingLifecycleSubscriber() *recordingLifecycleSubscriber {
	return &recordingLifecycleSubscriber{
		delegate: &Subscriber{},
		failNext: map[string]error{},
	}
}

func (r *recordingLifecycleSubscriber) record(c capturedCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

// snapshot returns the current call-list length. Callers use it as a
// "before" baseline for waitForCall so a leg only matches callbacks
// fired after the leg's trigger.
func (r *recordingLifecycleSubscriber) snapshot() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// callsSince returns a copy of every call captured at index >= base.
// Stable snapshot under the lock — callers iterate the returned slice
// without re-locking.
func (r *recordingLifecycleSubscriber) callsSince(base int) []capturedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	if base > len(r.calls) {
		return nil
	}
	out := make([]capturedCall, len(r.calls)-base)
	copy(out, r.calls[base:])
	return out
}

// failNextOnTemplateRegistered makes the NEXT OnTemplateRegistered call
// return the given error before recording. Used by the failure-honored
// leg to inject a deterministic subscriber failure.
func (r *recordingLifecycleSubscriber) failNextOnTemplateRegistered(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNext["OnTemplateRegistered"] = err
}

// clearFailures resets the per-verb failure switches so subsequent
// callbacks proceed normally.
func (r *recordingLifecycleSubscriber) clearFailures() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNext = map[string]error{}
}

// popFailure returns and clears any sticky failure for verb. Called by
// each callback handler before recording so the recorded call reflects
// what would have been ack'd had the failure not been injected.
func (r *recordingLifecycleSubscriber) popFailure(verb string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failNext[verb]; ok {
		delete(r.failNext, verb)
		return err
	}
	return nil
}

// LifecycleSubscriber methods — delegate to the example then record.

func (r *recordingLifecycleSubscriber) OnTemplateRegistered(ctx context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:         "OnTemplateRegistered",
		TemplateHash: req.GetTemplateHash(),
		Spec:         append([]byte(nil), req.GetSpec()...),
	})
	if err := r.popFailure("OnTemplateRegistered"); err != nil {
		return nil, err
	}
	return r.delegate.OnTemplateRegistered(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnTemplateDeployed(ctx context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:         "OnTemplateDeployed",
		TemplateHash: req.GetTemplateHash(),
		Tags:         append([]string(nil), req.GetTags()...),
	})
	if err := r.popFailure("OnTemplateDeployed"); err != nil {
		return nil, err
	}
	return r.delegate.OnTemplateDeployed(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnTemplateUndeployed(ctx context.Context, req *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:         "OnTemplateUndeployed",
		TemplateHash: req.GetTemplateHash(),
	})
	if err := r.popFailure("OnTemplateUndeployed"); err != nil {
		return nil, err
	}
	return r.delegate.OnTemplateUndeployed(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnTemplateDeregistered(ctx context.Context, req *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:         "OnTemplateDeregistered",
		TemplateHash: req.GetTemplateHash(),
	})
	if err := r.popFailure("OnTemplateDeregistered"); err != nil {
		return nil, err
	}
	return r.delegate.OnTemplateDeregistered(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnInstanceCreated(ctx context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:            "OnInstanceCreated",
		InstanceID:      req.GetInstanceId(),
		TemplateHash:    req.GetTemplateHash(),
		InstanceKey:     req.GetInstanceKey(),
		Params:          append([]byte(nil), req.GetParams()...),
		ServiceBindings: append([]byte(nil), req.GetServiceBindings()...),
		OwnerAPIKeyID:   req.GetOwnerApiKeyId(),
	})
	if err := r.popFailure("OnInstanceCreated"); err != nil {
		return nil, err
	}
	return r.delegate.OnInstanceCreated(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnInstanceTerminated(ctx context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:               "OnInstanceTerminated",
		InstanceID:         req.GetInstanceId(),
		TemplateHash:       req.GetTemplateHash(),
		TerminatedAtUnixMs: req.GetTerminatedAtUnixMs(),
	})
	if err := r.popFailure("OnInstanceTerminated"); err != nil {
		return nil, err
	}
	return r.delegate.OnInstanceTerminated(ctx, req)
}

func (r *recordingLifecycleSubscriber) OnRunScopeTerminal(ctx context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	r.record(capturedCall{
		Verb:           "OnRunScopeTerminal",
		RunScopeID:     req.GetRunScopeId(),
		TerminalReason: req.GetTerminalReason(),
		InstanceID:     req.GetInstanceId(),
	})
	if err := r.popFailure("OnRunScopeTerminal"); err != nil {
		return nil, err
	}
	return r.delegate.OnRunScopeTerminal(ctx, req)
}

// Capabilities answers the rimsky startup ExecutorObservability probe.
// The peer entry advertises [executor, lifecycle_subscriber], so the
// best-effort observability handshake calls
// ExecutorObservability.Capabilities; the LifecycleSubscriber path has
// no Capabilities verb. Advertising an open schema lets the discovery
// cache record the peer as Reachable when the probe lands. The runtime
// Execute verb is declared Unimplemented (inherited from the embedded
// UnimplementedExecutorServer); no template references this peer as an
// executor, so Execute is never invoked.
func (r *recordingLifecycleSubscriber) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// startRecordingLifecycleSubscriber stands up the wrapper as an
// in-process gRPC server on `port`, registers BOTH the lifecycle-
// subscriber server and the (minimal) claim-producer server on the
// same listener, and blocks until the listener is accepting
// connections — the harness's eager Capabilities handshake must
// succeed at rimsky startup. Cleanup (graceful Stop) is registered via
// t.Cleanup.
func startRecordingLifecycleSubscriber(t *testing.T, port int) *recordingLifecycleSubscriber {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	rec := newRecordingLifecycleSubscriber()
	genv1.RegisterLifecycleSubscriberServer(srv, rec)
	genv1.RegisterExecutorServer(srv, rec)
	genv1.RegisterExecutorObservabilityServer(srv, rec)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return rec
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("recording lifecycle subscriber did not become dialable at %s within 10s", addr)
	return nil
}

// ---------------------------------------------------------------------------
// Stub executor — minimal Success-returning Executor + open schema.
// ---------------------------------------------------------------------------

// stubExecutorServer is a minimal Executor that returns a single
// terminal Success for every dispatch. Mirrors the pattern in
// examples/publisher/main_e2e_test.go::stubExecutorServer — kept inline
// so the example's cross-stack proof has no extra docker-build dep.
type stubExecutorServer struct {
	genv1.UnimplementedExecutorServer
}

// Execute sends exactly one terminal StreamClose{Success} and closes
// the stream — the minimal honest Executor contract per
// concept:executor.
func (stubExecutorServer) Execute(_ *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "stub executor: success",
		}}},
	}})
}

// stubObservabilityServer answers Capabilities with an open expected-
// attributes schema so the registration-time and dispatch-time gates
// accept the worker node's attributes unconditionally.
type stubObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities returns an open schema, no-trace observability contract.
func (stubObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// GetTrace / StreamTrace return Unimplemented (the stub retains no
// traces).
func (stubObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

func (stubObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

// startStubExecutor brings up the stub Executor on `port` and blocks
// until the listener accepts. Mirrors startRecordingLifecycleSubscriber's
// ordering discipline so rimsky's eager Capabilities handshake succeeds
// at startup.
func startStubExecutor(t *testing.T, port int) {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("stub executor listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, stubExecutorServer{})
	genv1.RegisterExecutorObservabilityServer(srv, stubObservabilityServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

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
	t.Fatalf("stub executor did not become dialable at %s within 10s", addr)
}

// ---------------------------------------------------------------------------
// Test helpers — template + instance lifecycle drivers + auth bootstrap.
// ---------------------------------------------------------------------------

// freeHostPort grabs an OS-assigned TCP port and returns it. The brief
// close-then-reuse race is acceptable for an in-process test fixture
// (matches examples/publisher/main_e2e_test.go::freeHostPort).
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

// waitForCall polls the recorder's per-verb call set until at least one
// call captured at index >= base matches `verb`, or the deadline
// elapses. Returns the FIRST matching capturedCall. Fails hard on
// timeout — the callback landing is the load-bearing observable, never
// a skip.
func waitForCall(t *testing.T, rec *recordingLifecycleSubscriber, verb string, base int, deadline time.Duration, why string) capturedCall {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		for _, c := range rec.callsSince(base) {
			if c.Verb == verb {
				return c
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Diagnostic: enumerate every callback verb captured since `base`
	// so the developer can tell whether a different lifecycle event
	// fired (e.g. ordering swap) vs no events fired at all.
	tail := rec.callsSince(base)
	verbs := make([]string, 0, len(tail))
	for _, c := range tail {
		verbs = append(verbs, c.Verb)
	}
	t.Fatalf("lifecycle callback %q never landed at the example subscriber within %v — %s\nverbs captured since baseline=%d: %v",
		verb, deadline, why, base, verbs)
	return capturedCall{}
}

// registerLifecycleTemplate POSTs a template that references the
// example subscriber as a store (so it appears in peersReferencedBySpec
// and receives lifecycle fan-out) and the stub executor as the worker
// node's executor. Returns the resulting template_hash. The frame
// timeout is set high so a long-running test doesn't trip the
// supervisor's per-frame timeout.
func registerLifecycleTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":                  "lifecycle-subscriber-walkthrough",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					// Reference the subscriber peer as a store; the
					// alias is what's used by the supervisor, but the
					// `name` is what makes it appear in
					// peersReferencedBySpec for lifecycle fan-out.
					"stores": []map[string]any{
						{
							"name":     "example-subscriber",
							"intent":   "r",
							"selector": "lifecycle-walk",
						},
					},
				},
			},
		},
	}
	statusCode, raw := ep.PostJSON(t, "/v1/templates", body)
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", statusCode, string(raw))
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
	return resp.TemplateID
}

// deployLifecycleTemplate POSTs the deploy verb on `hash`. Leg 1's
// template-register POST happens before bootstrap, so it's
// anonymous-mode (no bearer); from leg 2 onward `bearer` must be
// non-empty.
func deployLifecycleTemplate(t *testing.T, ep harness.RimskyEndpoint, bearer, hash string) {
	t.Helper()
	statusCode, raw := ep.PostJSONWithHeaders(t, "/v1/templates/"+hash+"/deploy", map[string]any{},
		authHeader(bearer))
	if statusCode != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", hash, statusCode, string(raw))
	}
}

// undeployTemplate POSTs the undeploy verb on `hash`.
func undeployTemplate(t *testing.T, ep harness.RimskyEndpoint, bearer, hash string) {
	t.Helper()
	statusCode, raw := ep.PostJSONWithHeaders(t, "/v1/templates/"+hash+"/undeploy", map[string]any{},
		authHeader(bearer))
	if statusCode != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/undeploy: %d %s", hash, statusCode, string(raw))
	}
}

// deregisterTemplate DELETEs `hash`. The harness only exposes a POST
// helper; the DELETE request is constructed manually so the verb is
// correct AND the bearer is carried.
func deregisterTemplate(t *testing.T, ep harness.RimskyEndpoint, bearer, hash string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, ep.BaseURL+"/v1/templates/"+hash, nil)
	if err != nil {
		t.Fatalf("build DELETE /v1/templates/%s: %v", hash, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/templates/%s: %v", hash, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/templates/%s: status=%d", hash, resp.StatusCode)
	}
}

// authHeader returns the Authorization header map for the bearer, or
// an empty map for an empty bearer (so anonymous-mode requests don't
// stamp an `Authorization: Bearer ` no-op header).
func authHeader(bearer string) map[string]string {
	if bearer == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + bearer}
}

// bootstrapAdminKey POSTs an admin key on the anonymous-mode
// deployment (the same request `rimsky auth init` issues against
// /v1/auth/keys). The fresh deployment starts in anonymous mode;
// bootstrapping the admin key closes anonymous mode and surfaces an
// api-key id on authenticated requests' OwnerAPIKeyID. The bundled
// admin role's permission set is the literal `*` (cmd/rimsky/cli/roles/
// admin.json), which the server projects to the full action surface.
func bootstrapAdminKey(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	statusCode, raw := ep.PostJSON(t, "/v1/auth/keys", map[string]any{
		"name": "admin",
		"permissions": []map[string]any{
			{"action": "*"},
		},
	})
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/auth/keys: status=%d body=%s", statusCode, string(raw))
	}
	var resp struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode auth keys create response: %v: %s", err, string(raw))
	}
	if resp.Plaintext == "" {
		t.Fatalf("auth keys create response did not carry plaintext: %s", string(raw))
	}
	return resp.Plaintext
}

// createLifecycleInstance POSTs an authenticated instance create
// carrying a non-empty service_bindings bag and returns the resulting
// instance_id. The api-key id derived from `bearer` is what surfaces as
// owner_api_key_id on the OnInstanceCreated callback.
func createLifecycleInstance(t *testing.T, ep harness.RimskyEndpoint, bearer, templateHash string, serviceBindings map[string]any) string {
	t.Helper()
	body := map[string]any{
		"template":         templateHash,
		"instance_key":     "ck-lifecycle-walk",
		"params":           map[string]any{},
		"service_bindings": serviceBindings,
	}
	statusCode, raw := ep.PostJSONWithHeaders(t, "/v1/instances", body, map[string]string{
		"Authorization": "Bearer " + bearer,
	})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", statusCode, string(raw))
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

// terminateInstance force-terminates an instance via
// POST /v1/instances/{id}/terminate. The handler closes the main
// run-scope (firing OnRunScopeTerminal) before marking the instance
// terminal. The `reason` field flows into the run-scope close's
// terminal_reason — but rimsky overrides that to "instance_killed" /
// "instance_deleted" / etc. on the actual fan-out; the
// caller-supplied reason is recorded on the audit event rather than
// the lifecycle envelope. The test only asserts non-empty.
func terminateInstance(t *testing.T, ep harness.RimskyEndpoint, bearer, instanceID, reason string) {
	t.Helper()
	statusCode, raw := ep.PostJSONWithHeaders(t, "/v1/instances/"+instanceID+"/terminate", map[string]any{
		"reason": reason,
	}, authHeader(bearer))
	if statusCode != http.StatusOK {
		t.Fatalf("POST /v1/instances/%s/terminate: %d %s", instanceID, statusCode, string(raw))
	}
}

// deleteInstance DELETEs the (already-terminated) instance row.
// Constructs the request manually because PostJSONWithHeaders only
// supports POST. Carries the bearer so the request passes the auth
// gate once anonymous mode has closed.
func deleteInstance(t *testing.T, ep harness.RimskyEndpoint, bearer, instanceID string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, ep.BaseURL+"/v1/instances/"+instanceID, nil)
	if err != nil {
		t.Fatalf("build DELETE /v1/instances/%s: %v", instanceID, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/instances/%s: %v", instanceID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/instances/%s: status=%d", instanceID, resp.StatusCode)
	}
}
