// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

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

	if os.Getenv("STUBCHILD_EXIT_IMMEDIATELY") != "" {
		os.Exit(0)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stubchild: listen %s: %v\n", port, err)
		os.Exit(1)
	}

	srvOpts, err := mtlsServerOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stubchild: enroll: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer(srvOpts...)
	genv1.RegisterExecutorServer(srv, &stubExecutor{})
	genv1.RegisterExecutorObservabilityServer(srv, &stubExecutorObs{})
	genv1.RegisterClaimProducerServer(srv, &stubClaimProducer{})

	go func() { _ = srv.Serve(lis) }()

	if os.Getenv("STUBCHILD_IGNORE_SIGTERM") != "" {
		signal.Ignore(syscall.SIGTERM)
		select {}
	}

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

func mtlsServerOptions() ([]grpc.ServerOption, error) {
	if os.Getenv(enroll.EnvPeerAuth) != enroll.PeerAuthMTLS {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := enroll.Enroll(ctx, &http.Client{Timeout: 30 * time.Second},
		os.Getenv(enroll.EnvControlAPIURL), os.Getenv(enroll.EnvAPIKey), "stubchild")
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair([]byte(resp.CertPEM), []byte(resp.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("load leaf keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(resp.CARootPEM)) {
		return nil, fmt.Errorf("ca_root_pem is not a valid certificate")
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(cfg))}, nil
}

func sleepUntilSignal() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
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

func (s *stubExecutor) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	recordPID(req.GetRunScopeId())
	recordExec()
	if os.Getenv("STUBCHILD_EXECUTE_BLOCK_UNTIL_CANCEL") != "" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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

func (s *stubClaimProducer) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	recordVerb("open")
	if os.Getenv("STUBCHILD_OPEN_BLOCK_UNTIL_CANCEL") != "" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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
