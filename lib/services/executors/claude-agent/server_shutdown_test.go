// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestRunningGrpcServerShutdownForceStopsPastDeadline(t *testing.T) {
	obs := NewObservabilityServer("http://bridge.invalid")
	obs.RegisterDispatch("held-open")
	obs.AppendEvent("held-open", makeTraceEvent("log", genv1.Severity_INFO, "started", nil))

	executor, _, _ := startTestExecutor(t)
	running, err := StartGrpcServer("127.0.0.1", 0, executor, obs, nil)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := grpc.NewClient(running.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	stream, err := genv1.NewExecutorObservabilityClient(conn).StreamTrace(streamCtx, &genv1.StreamTraceRequest{DispatchId: "held-open"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("expected the stream to stay open with no terminal event yet, got %v", err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShutdown()
	done := make(chan struct{})
	go func() {
		running.Shutdown(shutdownCtx)
		close(done)
	}()
	<-done
}
