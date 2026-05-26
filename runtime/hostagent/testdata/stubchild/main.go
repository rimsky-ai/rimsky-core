// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func main() {
	port := os.Getenv("RIMSKY_AGENT_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "stubchild: RIMSKY_AGENT_PORT unset")
		os.Exit(1)
	}

	if os.Getenv("STUBCHILD_NO_BIND") != "" {
		// Never bind; sleep until killed so the agent's port-probe times out.
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

	go func() { _ = srv.Serve(lis) }()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	if path := os.Getenv("STUBCHILD_TERM_LOG"); path != "" {
		// Touch the marker so a reap-scenario test can assert the agent
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

// Execute emits one NamedEvent (when STUBCHILD_EXECUTE_ECHO is set) echoing
// the request's node_id, then a terminal StreamClose with a Success outcome.
func (s *stubExecutor) Execute(req *genv1.ExecuteRequest, stream grpc.ServerStreamingServer[genv1.ExecuteEvent]) error {
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
