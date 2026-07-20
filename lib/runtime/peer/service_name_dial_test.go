// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type capturedServiceNames struct {
	mu    sync.Mutex
	byRPC map[string]string
}

func (c *capturedServiceNames) record(method string, ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	vals := md.Get("x-rimsky-service-name")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byRPC == nil {
		c.byRPC = map[string]string{}
	}
	if len(vals) > 0 {
		c.byRPC[method] = vals[0]
	}
}

func (c *capturedServiceNames) get(method string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byRPC[method]
}

type peerPublisherServer struct {
	genv1.UnimplementedPublisherServer
}

type peerLifecycleServer struct {
	genv1.UnimplementedLifecycleSubscriberServer
}

type peerDataProcessingServer struct {
	genv1.UnimplementedDataProcessingServer
}

type peerValidationServer struct {
	genv1.UnimplementedValidationServer
}

func startAllPeerServicesServer(t *testing.T) (addr string, captured *capturedServiceNames) {
	t.Helper()
	captured = &capturedServiceNames{}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		captured.record(info.FullMethod, ctx)
		return handler(ctx, req)
	}))
	genv1.RegisterPublisherServer(srv, peerPublisherServer{})
	genv1.RegisterLifecycleSubscriberServer(srv, peerLifecycleServer{})
	genv1.RegisterDataProcessingServer(srv, peerDataProcessingServer{})
	genv1.RegisterValidationServer(srv, peerValidationServer{})

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), captured
}

func TestDialPublisher_InstallsServiceNameInterceptor(t *testing.T) {
	addr, captured := startAllPeerServicesServer(t)
	client, err := DialPublisher(context.Background(), "pub", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("DialPublisher: %v", err)
	}
	t.Cleanup(client.Close)

	ctx := WithServiceName(context.Background(), "core-service")
	_ = client.Subscribe(ctx, sampleSubscribeRequest())

	if got := captured.get("/rimsky.v1.Publisher/Subscribe"); got != "core-service" {
		t.Fatalf("x-rimsky-service-name header on DialPublisher's client = %q, want %q; "+
			"DialPublisher must install ServiceNameUnaryInterceptor like the claim-producer Dial does", got, "core-service")
	}
}

func TestDialLifecycle_InstallsServiceNameInterceptor(t *testing.T) {
	addr, captured := startAllPeerServicesServer(t)
	client, err := DialLifecycle(context.Background(), "lc", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("DialLifecycle: %v", err)
	}
	t.Cleanup(client.Close)

	ctx := WithServiceName(context.Background(), "core-service")
	_ = client.OnRunScopeTerminal(ctx, locks.OnRunScopeTerminalRequest{})

	if got := captured.get("/rimsky.v1.LifecycleSubscriber/OnRunScopeTerminal"); got != "core-service" {
		t.Fatalf("x-rimsky-service-name header on DialLifecycle's client = %q, want %q", got, "core-service")
	}
}

func TestDialDataProcessing_InstallsServiceNameInterceptor(t *testing.T) {
	addr, captured := startAllPeerServicesServer(t)
	client, err := DialDataProcessing(context.Background(), "dp", addr, TLSModeOff)
	if err != nil {
		t.Fatalf("DialDataProcessing: %v", err)
	}
	t.Cleanup(client.Close)

	ctx := WithServiceName(context.Background(), "core-service")
	_, _ = client.ListVersions(ctx, clientiface.ListVersionsInput{})

	if got := captured.get("/rimsky.v1.DataProcessing/ListVersions"); got != "core-service" {
		t.Fatalf("x-rimsky-service-name header on DialDataProcessing's client = %q, want %q", got, "core-service")
	}
}

func TestDialValidation_InstallsServiceNameInterceptor(t *testing.T) {
	addr, captured := startAllPeerServicesServer(t)
	client, err := DialValidation(context.Background(), "val", addr, TLSModeOff, nil)
	if err != nil {
		t.Fatalf("DialValidation: %v", err)
	}
	t.Cleanup(client.Close)

	ctx := WithServiceName(context.Background(), "core-service")
	_, _, _ = client.ValidateExecutor(ctx, clientiface.ValidateExecutorInput{})

	if got := captured.get("/rimsky.v1.Validation/Validate"); got != "core-service" {
		t.Fatalf("x-rimsky-service-name header on DialValidation's client = %q, want %q", got, "core-service")
	}
}
