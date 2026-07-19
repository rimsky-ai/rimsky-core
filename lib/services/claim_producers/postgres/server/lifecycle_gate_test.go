// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func startPostgresServerForLifecycleGateTest(t *testing.T, dsn string, enableLifecycle bool) string {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Connection: dsn, EnableLifecycle: enableLifecycle}, grpcLis, httpLis, nil)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return grpcLis.Addr().String()
}

func dialPostgresLifecycleSubscriberClient(t *testing.T, addr string) genv1.LifecycleSubscriberClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewLifecycleSubscriberClient(conn)
}

func TestPostgresServer_LifecycleDisabled_RPCUnimplemented(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)
	addr := startPostgresServerForLifecycleGateTest(t, dsn, false)
	client := dialPostgresLifecycleSubscriberClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{InstanceId: "inst-1"})

	if err == nil {
		t.Fatalf("OnInstanceCreated succeeded with EnableLifecycle=false; " +
			"the bundled postgres store must not register the LifecycleSubscriber service when the " +
			"enable_lifecycle flag is off, so callers must observe Unimplemented, not a silent success")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=false: got err %v, want gRPC Unimplemented "+
			"(no LifecycleSubscriber service registered on this server)", err)
	}
}

func TestPostgresServer_LifecycleEnabled_RPCSucceeds(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)
	addr := startPostgresServerForLifecycleGateTest(t, dsn, true)
	client := dialPostgresLifecycleSubscriberClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ack, err := client.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{InstanceId: "inst-1"})
	if err != nil {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=true: got err %v, want a successful ack from the "+
			"registered no-op LifecycleSubscriber", err)
	}
	if ack == nil {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=true returned a nil ack")
	}
}
