// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func startFilesystemServerForLifecycleGateTest(t *testing.T, enableLifecycle bool) string {
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
		done <- Run(ctx, Config{Root: t.TempDir(), EnableLifecycle: enableLifecycle}, grpcLis, httpLis, nil)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return grpcLis.Addr().String()
}

func dialLifecycleSubscriberClient(t *testing.T, addr string) genv1.LifecycleSubscriberClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewLifecycleSubscriberClient(conn)
}

func dialClaimProducerClient(t *testing.T, addr string) genv1.ClaimProducerClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return genv1.NewClaimProducerClient(conn)
}

func TestFilesystemServer_LifecycleEnabled_CapabilitiesAdvertisesProtocol(t *testing.T) {
	addr := startFilesystemServerForLifecycleGateTest(t, true)
	client := dialClaimProducerClient(t, addr)

	ctx := context.Background()
	resp, err := client.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	found := false
	for _, p := range resp.GetProtocols() {
		if p == "lifecycle_subscriber" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Capabilities.protocols = %v, want it to include %q since Run was started with EnableLifecycle:true", resp.GetProtocols(), "lifecycle_subscriber")
	}
}

func TestFilesystemServer_LifecycleDisabled_CapabilitiesOmitsProtocol(t *testing.T) {
	addr := startFilesystemServerForLifecycleGateTest(t, false)
	client := dialClaimProducerClient(t, addr)

	ctx := context.Background()
	resp, err := client.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(resp.GetProtocols()) != 0 {
		t.Fatalf("Capabilities.protocols = %v, want empty since Run was started with EnableLifecycle:false", resp.GetProtocols())
	}
}

func TestFilesystemServer_LifecycleDisabled_RPCUnimplemented(t *testing.T) {
	addr := startFilesystemServerForLifecycleGateTest(t, false)
	client := dialLifecycleSubscriberClient(t, addr)

	ctx := context.Background()
	_, err := client.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{InstanceId: "inst-1"})

	if err == nil {
		t.Fatalf("OnInstanceCreated succeeded with EnableLifecycle=false; " +
			"the bundled filesystem store must not register the LifecycleSubscriber service when the " +
			"enable_lifecycle flag is off, so callers must observe Unimplemented, not a silent success")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=false: got err %v, want gRPC Unimplemented "+
			"(no LifecycleSubscriber service registered on this server)", err)
	}
}

func TestFilesystemServer_LifecycleEnabled_RPCSucceeds(t *testing.T) {
	addr := startFilesystemServerForLifecycleGateTest(t, true)
	client := dialLifecycleSubscriberClient(t, addr)

	ctx := context.Background()
	ack, err := client.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{InstanceId: "inst-1"})
	if err != nil {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=true: got err %v, want a successful ack from the "+
			"registered no-op LifecycleSubscriber", err)
	}
	if ack == nil {
		t.Fatalf("OnInstanceCreated with EnableLifecycle=true returned a nil ack")
	}
}
