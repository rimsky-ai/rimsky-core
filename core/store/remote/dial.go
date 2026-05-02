package remote

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/fallguy/rimsky/core/store"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// Dial connects to a remote store-service over gRPC, performs the
// startup Capabilities() handshake, and returns a Client that
// satisfies the rimsky-side store.Store interface.
//
// Endpoint may carry a "grpc://" prefix (the convention used in
// rimsky.yml); the prefix is stripped before passing to grpc.NewClient.
//
// Insecure credentials are used by default. Per spec §15 (out of
// scope) auth is deployment-layer concern (mTLS, service mesh, IAM)
// and is auth-blind in v3 — see the spec's deferral. mTLS support is
// a follow-up cycle; do not add transport credentials here without a
// concurrent spec change.
//
// On any failure (unreachable, capability RPC error, timeout), Dial
// returns the error without leaking a partial Client. Callers should
// pass a context with a deadline (config.dialRemoteStores wraps each
// dial in context.WithTimeout) so a non-responsive store-service
// cannot block startup forever.
func Dial(ctx context.Context, name, endpoint string) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("remote store %q: endpoint is required", name)
	}
	for _, badScheme := range []string{"http://", "https://", "tcp://", "unix://"} {
		if strings.HasPrefix(endpoint, badScheme) {
			return nil, fmt.Errorf("remote store %q: endpoint scheme must be grpc:// (got %s)", name, badScheme)
		}
	}
	target := strings.TrimPrefix(endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("remote store %q: dial %q: %w", name, endpoint, err)
	}
	rpc := genv1.NewStoreServiceClient(conn)
	resp, err := rpc.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("remote store %q: Capabilities handshake: %w", name, err)
	}
	caps := store.Capabilities{
		WriteSemantics: store.WriteSemantics(resp.GetCapabilities().GetWriteSemantics()),
	}
	return &Client{
		name: name,
		conn: conn,
		rpc:  rpc,
		caps: caps,
	}, nil
}
