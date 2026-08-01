// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type blockingObsServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	startedCh chan struct{}
}

func (s *blockingObsServer) StreamClaim(_ *genv1.StreamClaimRequest, stream grpc.ServerStreamingServer[genv1.ClaimEvent]) error {
	close(s.startedCh)
	<-stream.Context().Done()
	return stream.Context().Err()
}

func dialLifecycleTestServer(t *testing.T, srv *grpc.Server) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go Serve(srv, lis, "lifecycle-test")

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGracefulStop_ForcesStopAfterBudgetWithInFlightStream(t *testing.T) {
	srv := grpc.NewServer()
	obsSrv := &blockingObsServer{startedCh: make(chan struct{})}
	genv1.RegisterClaimProducerObservabilityServer(srv, obsSrv)
	conn := dialLifecycleTestServer(t, srv)

	client := genv1.NewClaimProducerObservabilityClient(conn)
	stream, err := client.StreamClaim(t.Context(), &genv1.StreamClaimRequest{ClaimId: "c1"})
	if err != nil {
		t.Fatalf("StreamClaim: %v", err)
	}
	t.Cleanup(func() { _, _ = stream.Recv() })

	<-obsSrv.startedCh

	done := make(chan struct{})
	go func() {
		GracefulStop(srv, 20*time.Millisecond)
		close(done)
	}()
	<-done
}

func TestGracefulStop_ReturnsImmediatelyWhenIdle(t *testing.T) {
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerObservabilityServer(srv, &blockingObsServer{startedCh: make(chan struct{}, 1)})
	dialLifecycleTestServer(t, srv)

	done := make(chan struct{})
	go func() {
		GracefulStop(srv, time.Hour)
		close(done)
	}()
	<-done
}
