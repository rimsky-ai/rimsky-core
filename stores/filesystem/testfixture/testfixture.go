// Package testfixture starts the filesystem store-service on ephemeral
// listeners for in-process loopback tests. Per spec §9.2.
package testfixture

import (
	"context"
	"net"
	"testing"

	"github.com/fallguy/rimsky/stores/filesystem/server"
)

// Start spawns server.Run on a goroutine bound to ephemeral ports.
// Returns the gRPC endpoint (host:port) and a teardown closure that
// cancels the context. The HTTP listener is started but not exposed —
// scenario tests connect over gRPC.
func Start(t *testing.T, root string) (endpoint string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("filesystem testfixture: grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("filesystem testfixture: http listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = server.Run(ctx, server.Config{Root: root}, grpcLis, httpLis)
		close(done)
	}()
	return grpcLis.Addr().String(), func() {
		cancel()
		<-done
	}
}
