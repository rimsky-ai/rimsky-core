// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// stubchild is a test fixture binary that honors the host-agent's
// RIMSKY_AGENT_PORT contract: it binds a gRPC server on 127.0.0.1:$PORT
// implementing the Executor, ExecutorObservability, and ClaimProducer
// protocols with deterministic responses, then serves until SIGTERM. The
// host-agent spawn/dispatch/reap tests build and exec this binary to exercise
// the real exec → port-probe → Capabilities → dispatch → reap path.
//
// Behavior knobs via env:
//
//	RIMSKY_AGENT_PORT       required; the port to bind (set by the agent).
//	STUBCHILD_NO_BIND       when set, the process sleeps without binding (to
//	                        exercise the agent's ready-timeout path).
//	STUBCHILD_EXECUTE_ECHO  when set, Execute emits one NamedEvent echoing the
//	                        request's node_id before StreamClose.
//	STUBCHILD_VERB_LOG      when set, each ClaimProducer RPC appends its verb
//	                        name (one per line) to this file path, so a test
//	                        can assert which verb the agent actually invoked.
//	STUBCHILD_TERM_LOG      when set, the process touches this file path on
//	                        SIGTERM/SIGINT before exiting, so a test can assert
//	                        the child was actually reaped (signalled) rather
//	                        than left running.
//	STUBCHILD_PID_LOG       when set, each Execute appends one line
//	                        "<run_scope_id> <pid>" to this file path, so a
//	                        per-run-scope-isolation test can assert that two
//	                        concurrent run-scopes were served by two DISTINCT
//	                        child processes (distinct pids) rather than one
//	                        shared child. The run_scope_id is read from
//	                        ExecuteRequest.run_scope_id (the dispatch wire
//	                        field the host-agent-proxy keys spawn isolation on).
//	STUBCHILD_EXEC_LOG      when set, each Execute appends one JSON line
//	                        {"args":[...],"env":"<value-of-STUBCHILD_EXEC_ENV_KEY>",
//	                        "cwd":"<os.Getwd()>"} to this file path, so a
//	                        per-binding-exec-overrides test can assert the
//	                        spawned child actually ran with the per-binding
//	                        argv (os.Args[1:]), env var, and working directory
//	                        the binding declared. The env value echoed is the
//	                        process env var named by STUBCHILD_EXEC_ENV_KEY (so
//	                        the test can pick a binding-specific key without
//	                        recompiling the stub).
//	STUBCHILD_EXEC_ENV_KEY  names which process env var STUBCHILD_EXEC_LOG
//	                        echoes back; absent → the "env" field is empty.
//	STUBCHILD_PUBLISH_LOG   when set, each Publisher.Subscribe appends one
//	                        line "<publisher_subscription_id> <instance_id>
//	                        <target_node>" to this file path, so a late-bind
//	                        publisher test can assert the stub recorded a real
//	                        publish (i.e. the Subscribe dispatch reached the
//	                        spawned binary, not a stub returning Unimplemented).
//
// The stub additionally implements the Validation and DataProcessing
// mix-in protocols so the late-bind-ALL-protocols scenario can drive a
// validation, publisher, and data-processing dispatch through the real
// proxy + agent to a real spawned binary:
//   - Validate REJECTS (valid=false with one ValidationFinding) when the
//     request carries the sentinel role "stubchild-reject"; otherwise it
//     accepts (valid=true). A deliberately-rejecting validator is thus
//     observable end to end.
//   - BeginCandidate/CommitCandidate perform a deterministic typed-data
//     op: BeginCandidate echoes the idempotency_key into the candidate
//     handle (prefixed); CommitCandidate returns candidate_metadata whose
//     bytes are derived deterministically from the handle, so a test can
//     assert the committed candidate came from the real binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// validationRejectRole is the sentinel role that makes the stub's
// Validate REJECT (valid=false). The late-bind-all-protocols scenario
// sends a ValidateRequest carrying this role so a deliberately-rejecting
// validator is observable end to end through the proxy + agent tunnel.
const validationRejectRole = "stubchild-reject"

// candidateHandlePrefix is prepended to the BeginCandidate idempotency_key
// to form the candidate handle, so a test can assert the handle came from
// the real spawned binary (a deterministic typed-data op) rather than from
// an Unimplemented stub.
const candidateHandlePrefix = "stub-candidate:"

// committedMetadataPrefix is prepended to the candidate handle bytes to
// form CommitCandidate's candidate_metadata, so the committed candidate is
// deterministically derived from (and provably tied to) the begun one.
const committedMetadataPrefix = "stub-committed:"

func main() {
	port := os.Getenv("RIMSKY_AGENT_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "stubchild: RIMSKY_AGENT_PORT unset")
		os.Exit(1)
	}

	if os.Getenv("STUBCHILD_NO_BIND") != "" {
		// @deliberate: never bind; sleep until killed so the agent's port-probe times out.
		sleepUntilSignal()
		return
	}

	lis, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stubchild: listen %s: %v\n", port, err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, &stubExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, &stubExecutorObs{})
	genv1.RegisterClaimProducerServer(srv, &stubClaimProducer{})
	genv1.RegisterPublisherServer(srv, &stubPublisher{})
	genv1.RegisterValidationServer(srv, &stubValidation{})
	genv1.RegisterDataProcessingServer(srv, &stubDataProcessing{})

	go func() { _ = srv.Serve(lis) }()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	if path := os.Getenv("STUBCHILD_TERM_LOG"); path != "" {
		// @deliberate: touch the marker so a reap-scenario test can assert the agent
		// actually signalled this child (i.e. the reap reached the agent).
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString("term\n")
			_ = f.Close()
		}
	}
	srv.GracefulStop()
}

func sleepUntilSignal() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigs:
	case <-time.After(60 * time.Second):
	}
}

type stubExecutor struct {
	genv1.UnimplementedExecutorServer
}

// pidLogMu serializes appends to STUBCHILD_PID_LOG across concurrent
// Execute RPCs within one stub process.
var pidLogMu sync.Mutex

// recordPID appends "<run_scope_id> <pid>" to STUBCHILD_PID_LOG (if set)
// so a per-run-scope-isolation test can assert two concurrent run-scopes
// were served by two DISTINCT child processes. The run_scope_id rides
// ExecuteRequest.run_scope_id — the dispatch wire field the
// host-agent-proxy keys per-run-scope spawn isolation on. The load-bearing
// observation is the pid: when the proxy collapses all run-scopes of an
// instance onto one shared child, every line carries the same pid.
func recordPID(runScopeID string) {
	path := os.Getenv("STUBCHILD_PID_LOG")
	if path == "" {
		return
	}
	pidLogMu.Lock()
	defer pidLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %d\n", runScopeID, os.Getpid())
}

// execLogMu serializes appends to STUBCHILD_EXEC_LOG across concurrent
// Execute RPCs within one stub process.
var execLogMu sync.Mutex

// recordExec appends one JSON line {"args":[...],"env":"...","cwd":"..."} to
// STUBCHILD_EXEC_LOG (if set). This is the load-bearing observation for the
// per-binding-exec-overrides test: the only way to prove the agent exec()'d
// the child with the binding's declared argv, env, and working directory is
// to have the child echo back its OWN os.Args[1:], a chosen env var, and
// os.Getwd() — captured from inside the spawned process, not inferred from
// the agent. The echoed env var is named by STUBCHILD_EXEC_ENV_KEY so a test
// can pick a binding-specific key without recompiling the stub.
func recordExec() {
	path := os.Getenv("STUBCHILD_EXEC_LOG")
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	envVal := ""
	if key := os.Getenv("STUBCHILD_EXEC_ENV_KEY"); key != "" {
		envVal = os.Getenv(key)
	}
	line, err := json.Marshal(struct {
		Args []string `json:"args"`
		Env  string   `json:"env"`
		Cwd  string   `json:"cwd"`
	}{
		Args: os.Args[1:],
		Env:  envVal,
		Cwd:  cwd,
	})
	if err != nil {
		return
	}
	execLogMu.Lock()
	defer execLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Execute emits one NamedEvent (when STUBCHILD_EXECUTE_ECHO is set) echoing
// the request's node_id, then a terminal StreamClose with a Success outcome.
func (s *stubExecutor) Execute(req *genv1.ExecuteRequest, stream grpc.ServerStreamingServer[genv1.ExecuteEvent]) error {
	recordPID(req.GetRunScopeId())
	recordExec()
	if os.Getenv("STUBCHILD_EXECUTE_ECHO") != "" {
		if err := stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_NamedEvent{NamedEvent: &genv1.NamedEvent{
			Name:    "stubchild.output",
			Payload: []byte(req.GetNodeId()),
		}}}); err != nil {
			return err
		}
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{Changed: true}}},
	}})
}

type stubExecutorObs struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (s *stubExecutorObs) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{DeclaredEvents: []string{"stubchild.output"}}, nil
}

type stubClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
}

// verbLogMu serializes appends to STUBCHILD_VERB_LOG across concurrent RPCs.
var verbLogMu sync.Mutex

// recordVerb appends the invoked ClaimProducer verb to STUBCHILD_VERB_LOG (if
// set) so a test can assert the agent dispatched the correct RPC. This is the
// load-bearing observation for the verb-fidelity test: Commit/Abandon/Release
// requests are byte-identical at claim_id, so the only way to prove the agent
// invoked the right RPC is to record which method handler actually ran.
func recordVerb(verb string) {
	path := os.Getenv("STUBCHILD_VERB_LOG")
	if path == "" {
		return
	}
	verbLogMu.Lock()
	defer verbLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(verb + "\n")
}

func (s *stubClaimProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		SupportsSplitScope:    false,
	}, nil
}

func (s *stubClaimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	recordVerb("open")
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
	}}}, nil
}

func (s *stubClaimProducer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	recordVerb("commit")
	return &genv1.CommitResponse{}, nil
}

func (s *stubClaimProducer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	recordVerb("abandon")
	return &genv1.AbandonResponse{}, nil
}

func (s *stubClaimProducer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	recordVerb("release")
	return &genv1.ReleaseResponse{}, nil
}

// stubPublisher implements the Publisher protocol. Subscribe records a
// real publish to STUBCHILD_PUBLISH_LOG so the late-bind publisher
// dispatch is observably served by this spawned binary.
type stubPublisher struct {
	genv1.UnimplementedPublisherServer
}

func (s *stubPublisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{Protocols: []string{"publisher"}}, nil
}

// publishLogMu serializes appends to STUBCHILD_PUBLISH_LOG.
var publishLogMu sync.Mutex

// recordPublish appends "<publisher_subscription_id> <instance_id>
// <target_node>" to STUBCHILD_PUBLISH_LOG (if set). This is the
// load-bearing observation for the late-bind publisher test: a recorded
// line proves the Subscribe dispatch reached the real spawned binary
// through the proxy + agent tunnel rather than a stub returning
// Unimplemented.
func recordPublish(subID, instanceID, targetNode string) {
	path := os.Getenv("STUBCHILD_PUBLISH_LOG")
	if path == "" {
		return
	}
	publishLogMu.Lock()
	defer publishLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s %s\n", subID, instanceID, targetNode)
}

func (s *stubPublisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	recordPublish(req.GetPublisherSubscriptionId(), req.GetInstanceId(), req.GetTargetNode())
	return &genv1.SubscribeResponse{}, nil
}

func (s *stubPublisher) Unsubscribe(_ context.Context, _ *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	return &genv1.UnsubscribeResponse{}, nil
}

func (s *stubPublisher) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	return &genv1.ListSubscriptionsResponse{}, nil
}

// stubValidation implements the Validation mix-in protocol. Validate
// REJECTS (valid=false with one ValidationFinding) when the request
// carries the sentinel role validationRejectRole, otherwise accepts. A
// deliberately-rejecting validator is thus observable end to end.
type stubValidation struct {
	genv1.UnimplementedValidationServer
}

func (s *stubValidation) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if req.GetRole() == validationRejectRole {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "stubchild_rejected",
				Message: "stubchild validator deliberately rejected role " + req.GetRole(),
				Path:    "/role",
			}},
		}, nil
	}
	return &genv1.ValidateResponse{Valid: true}, nil
}

// stubDataProcessing implements the DataProcessing mix-in protocol.
// BeginCandidate/CommitCandidate perform a deterministic typed-data op so
// a late-bind data-processing dispatch returns a real, provably-derived
// committed candidate.
type stubDataProcessing struct {
	genv1.UnimplementedDataProcessingServer
}

func (s *stubDataProcessing) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{Materializations: []string{"full"}}, nil
}

func (s *stubDataProcessing) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	return &genv1.BeginCandidateResponse{
		CandidateHandle: []byte(candidateHandlePrefix + req.GetIdempotencyKey()),
	}, nil
}

func (s *stubDataProcessing) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	return &genv1.CommitCandidateResponse{
		CandidateMetadata: append([]byte(committedMetadataPrefix), req.GetCandidateHandle()...),
	}, nil
}

func (s *stubDataProcessing) AbandonCandidate(_ context.Context, _ *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
