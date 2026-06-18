// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const validationRejectRole = "stubchild-reject"

const candidateHandlePrefix = "stub-candidate:"

const committedMetadataPrefix = "stub-committed:"

func main() {
	port := os.Getenv("RIMSKY_AGENT_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "stubchild: RIMSKY_AGENT_PORT unset")
		os.Exit(1)
	}

	if os.Getenv("STUBCHILD_NO_BIND") != "" {
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

var pidLogMu sync.Mutex

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

var execLogMu sync.Mutex

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

func (s *stubExecutor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	recordPID(req.GetRunScopeId())
	recordExec()
	success := &genv1.Success{Changed: true}
	if os.Getenv("STUBCHILD_EXECUTE_ECHO") != "" {
		delta, _ := structpb.NewStruct(map[string]any{"echoed_node_id": req.GetNodeId()})
		success.AttributesDelta = delta
		success.Tags = []string{"stubchild.output"}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: success}}, nil
}

type stubExecutorObs struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (s *stubExecutorObs) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{DeclaredTags: []string{"stubchild.output"}}, nil
}

type stubClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
}

var verbLogMu sync.Mutex

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

type stubPublisher struct {
	genv1.UnimplementedPublisherServer
}

func (s *stubPublisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{Protocols: []string{"publisher"}}, nil
}

var publishLogMu sync.Mutex

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
