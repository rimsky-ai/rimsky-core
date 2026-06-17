// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-lifecycle-subscriber-author: a service
// author's example Subscriber — implementing the seven lifecycle
// callbacks (OnTemplateRegistered / OnTemplateDeployed /
// OnTemplateUndeployed / OnTemplateDeregistered / OnInstanceCreated /
// OnInstanceTerminated / OnRunScopeTerminal) — plugs into a running
// rimsky stack end-to-end and receives each callback at the
// corresponding lifecycle transition with documented context fields.
//
// The seven-callback walk is exhibited against the REAL assembled
// product (rimsky-all-in-one in a testcontainer, Postgres state DB)
// plus the REAL example Subscriber type (this directory's Subscriber,
// run in-process behind a thin recording wrapper that captures each
// call without modifying the example). An eighth leg drives a failing
// callback and asserts rimsky honors the failure synchronously (the
// triggering HTTP request returns 5xx and the row mutation does NOT
// happen).
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import the lib/services testcontainers harness without
// putting the example's published Apache surface at risk.
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
// lifecycle-subscriber peer (mixed into an executor entry) plus a stub
// executor, then walks the seven-callback lifecycle and exhibits each
// context-field property STORY-lifecycle-subscriber-author's Acceptance
// names.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`).
func TestE2E_ExampleLifecycleSubscriberAgainstRunningRimsky(t *testing.T) {
	ctx := context.Background()

	subPort := freeHostPort(t)
	rec := startRecordingLifecycleSubscriber(t, subPort)

	execPort := freeHostPort(t)
	startStubExecutor(t, execPort)

	subEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", subPort)
	execEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", execPort)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExecutor("example-subscriber", subEndpoint),
		harness.WithExecutorProtocols("example-subscriber", "lifecycle_subscriber"),
		harness.WithExecutor("stub", execEndpoint),
		harness.WithHostPortAccess(subPort, execPort),
		harness.WithRefValidationMode("none"),
	)

	state := &lifecycleState{}

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
// shared "before terminate" baseline for legs 4 and 5.
type lifecycleState struct {
	templateHash      string
	instanceID        string
	preTerminateIndex int
	// @deliberate: adminKey is the bootstrap admin plaintext minted in
	// leg 3. Anonymous mode is open before this is set; afterwards,
	// every request must carry the bearer.
	adminKey string
}

func exerciseOnTemplateRegisteredLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	state.templateHash = registerLifecycleTemplate(t, ep)
	call := waitForCall(t, rec, "OnTemplateRegistered", before, 30*time.Second,
		"OnTemplateRegistered must fire synchronously on POST /v1/templates")

	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateRegistered template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
	if len(call.Spec) == 0 {
		t.Fatalf("OnTemplateRegistered carried empty spec bytes — the JCS-canonicalized template spec must be populated")
	}
}

func exerciseOnTemplateDeployedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	before := rec.snapshot()
	deployLifecycleTemplate(t, ep, "", state.templateHash)
	call := waitForCall(t, rec, "OnTemplateDeployed", before, 30*time.Second,
		"OnTemplateDeployed must fire on POST /v1/templates/{hash}/deploy")
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnTemplateDeployed template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
}

func exerciseOnInstanceCreatedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	state.adminKey = bootstrapAdminKey(t, ep)
	adminKey := state.adminKey

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
		t.Fatalf("OnInstanceCreated owner_api_key_id was empty — an authenticated create MUST carry the api-key id")
	}
	if len(call.ServiceBindings) == 0 {
		t.Fatalf("OnInstanceCreated service_bindings was empty — the proxy consumes this to populate its per-instance binding cache")
	}
	var got map[string]any
	if err := json.Unmarshal(call.ServiceBindings, &got); err != nil {
		t.Fatalf("OnInstanceCreated service_bindings JSON decode failed: %v; raw=%q", err, string(call.ServiceBindings))
	}
	if _, ok := got["some-service"]; !ok {
		t.Fatalf("OnInstanceCreated service_bindings did not preserve 'some-service': %v", got)
	}
}

func exerciseOnRunScopeTerminalLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	state.preTerminateIndex = rec.snapshot()
	terminateInstance(t, ep, state.adminKey, state.instanceID, "test_termination_reason")
	call := waitForCall(t, rec, "OnRunScopeTerminal", state.preTerminateIndex, 60*time.Second,
		"OnRunScopeTerminal must fire on POST /v1/instances/{id}/terminate")

	if call.InstanceID != state.instanceID {
		t.Fatalf("OnRunScopeTerminal instance_id mismatch: got %q want %q",
			call.InstanceID, state.instanceID)
	}
	if call.RunScopeID == "" {
		t.Fatalf("OnRunScopeTerminal run_scope_id was empty")
	}
	if call.TerminalReason == "" {
		t.Fatalf("OnRunScopeTerminal terminal_reason was empty")
	}
}

func exerciseOnInstanceTerminatedLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	deleteInstance(t, ep, state.adminKey, state.instanceID)
	call := waitForCall(t, rec, "OnInstanceTerminated", state.preTerminateIndex, 60*time.Second,
		"OnInstanceTerminated must fire on DELETE /v1/instances/{id} (or earlier from the InstanceTerminator worker)")
	if call.InstanceID != state.instanceID {
		t.Fatalf("OnInstanceTerminated instance_id mismatch: got %q want %q",
			call.InstanceID, state.instanceID)
	}
	if call.TemplateHash != state.templateHash {
		t.Fatalf("OnInstanceTerminated template_hash mismatch: got %q want %q",
			call.TemplateHash, state.templateHash)
	}
	if call.TerminatedAtUnixMs == 0 {
		t.Fatalf("OnInstanceTerminated terminated_at_unix_ms was zero")
	}
}

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

// exerciseFailureHonoredSynchronouslyLeg flips the recording wrapper
// to return a non-nil error on the next OnTemplateRegistered, then
// POSTs a fresh template. The HTTP response MUST be 5xx — rimsky must
// NOT swallow the error and proceed with the row insert.
func exerciseFailureHonoredSynchronouslyLeg(t *testing.T, ep harness.RimskyEndpoint, rec *recordingLifecycleSubscriber, state *lifecycleState) {
	rec.failNextOnTemplateRegistered(status.Error(codes.Internal, "subscriber rejected the callback"))
	defer rec.clearFailures()

	spec := map[string]any{
		"spec": map[string]any{
			"name":             "lifecycle-subscriber-failure-probe",
			"version":          "1",
			"frame_timeout_ms": 600000,
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
		t.Fatalf("POST /v1/templates with a failing subscriber returned %d — rimsky must surface a 5xx; body: %s",
			statusCode, string(body))
	}
	if statusCode < 500 {
		t.Fatalf("POST /v1/templates with a failing subscriber returned %d — want 5xx; body: %s",
			statusCode, string(body))
	}
	bodyLower := strings.ToLower(string(body))
	if !strings.Contains(bodyLower, "fan-out") && !strings.Contains(bodyLower, "subscriber") {
		t.Fatalf("5xx response body should name the lifecycle fan-out or the subscriber: %s", string(body))
	}
}

// capturedCall is one observed lifecycle callback. Fields are
// populated per the callback's request shape; unused fields stay at
// zero-value.
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
// every callback before delegating to it. Also implements the minimal
// Executor / ExecutorObservability surface rimsky probes at startup.
type recordingLifecycleSubscriber struct {
	genv1.UnimplementedLifecycleSubscriberServer
	genv1.UnimplementedExecutorServer
	genv1.UnimplementedExecutorObservabilityServer

	delegate *Subscriber

	mu       sync.Mutex
	calls    []capturedCall
	failNext map[string]error
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
// baseline for waitForCall so a leg only matches callbacks fired after
// the leg's trigger.
func (r *recordingLifecycleSubscriber) snapshot() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// callsSince returns a copy of every call captured at index >= base.
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

// failNextOnTemplateRegistered arms the NEXT OnTemplateRegistered call
// to return the given error before recording.
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

// popFailure returns and clears any sticky failure for verb.
func (r *recordingLifecycleSubscriber) popFailure(verb string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failNext[verb]; ok {
		delete(r.failNext, verb)
		return err
	}
	return nil
}

// OnTemplateRegistered records the call then delegates.
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

// OnTemplateDeployed records the call then delegates.
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

// OnTemplateUndeployed records the call then delegates.
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

// OnTemplateDeregistered records the call then delegates.
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

// OnInstanceCreated records the call then delegates.
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

// OnInstanceTerminated records the call then delegates.
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

// OnRunScopeTerminal records the call then delegates.
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

// Capabilities answers the rimsky startup ExecutorObservability probe
// so the discovery cache records the peer as Reachable.
func (r *recordingLifecycleSubscriber) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// startRecordingLifecycleSubscriber stands up the wrapper as an
// in-process gRPC server on `port` and blocks until the listener is
// accepting connections.
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

// stubExecutorServer implements the post-coherence unary Executor
// returning a settling Success Outcome (per TD-execute-rpc-unary).
type stubExecutorServer struct {
	genv1.UnimplementedExecutorServer
}

// Execute returns a single settling Success Outcome (no stream).
func (stubExecutorServer) Execute(_ context.Context, _ *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "stub executor: success",
	}}}, nil
}

// stubObservabilityServer answers Capabilities with an open expected-
// attributes schema so the registration-time and dispatch-time gates
// accept the worker node unconditionally.
type stubObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities returns an open schema, no-trace observability
// contract.
func (stubObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// GetTrace returns Unimplemented (the stub retains no traces).
func (stubObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

// StreamTrace returns Unimplemented (the stub retains no traces).
func (stubObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

// startStubExecutor brings up the stub Executor on `port` and blocks
// until the listener accepts.
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

// freeHostPort grabs an OS-assigned TCP port and returns it.
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

// waitForCall polls the recorder's per-verb call set until at least
// one call captured at index >= base matches `verb`, or the deadline
// elapses.
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
	tail := rec.callsSince(base)
	verbs := make([]string, 0, len(tail))
	for _, c := range tail {
		verbs = append(verbs, c.Verb)
	}
	t.Fatalf("lifecycle callback %q never landed within %v — %s\nverbs captured since baseline=%d: %v",
		verb, deadline, why, base, verbs)
	return capturedCall{}
}

// registerLifecycleTemplate POSTs a template that references the
// example subscriber as a store and the stub executor as the worker
// node's executor. Returns the resulting template_hash.
func registerLifecycleTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":             "lifecycle-subscriber-walkthrough",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
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

// deployLifecycleTemplate POSTs the deploy verb on `hash`. Leg 1
// happens before bootstrap; from leg 2 onward `bearer` must be
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

// deregisterTemplate DELETEs `hash`. Manual request construction
// (PostJSONWithHeaders only supports POST).
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/templates/%s: status=%d", hash, resp.StatusCode)
	}
}

// authHeader returns the Authorization header map for the bearer, or
// nil for an empty bearer.
func authHeader(bearer string) map[string]string {
	if bearer == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + bearer}
}

// bootstrapAdminKey POSTs an admin key on the anonymous-mode
// deployment (the same request `rimsky auth init` issues).
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
// instance_id.
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
// POST /v1/instances/{id}/terminate.
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/instances/%s: status=%d", instanceID, resp.StatusCode)
	}
}
