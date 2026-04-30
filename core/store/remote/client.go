package remote

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/fallguy/rimsky/core/store"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// Client is a remote-gRPC implementation of the rimsky-side Store
// interface. One Client per registered store (operator-chosen name in
// stores.yml). Per spec §5.1.
type Client struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.StoreServiceClient
	caps store.Capabilities
}

// Compile-time interface check.
var _ store.Store = (*Client)(nil)

// Name returns the operator-configured store name supplied at Dial.
func (c *Client) Name() string { return c.name }

// Capabilities returns the cached capability struct populated by Dial's
// startup handshake. Returns the cached value without making another
// RPC; rimsky calls Capabilities exactly once per store-service per
// process at startup.
func (c *Client) Capabilities(_ context.Context) (store.Capabilities, error) {
	return c.caps, nil
}

// Open RPCs to the remote store. Maps the OpenResponse oneof to
// OpenOutcome: Acquired → {Available: true, Result: ...};
// Unavailable → {Available: false}. Substrate-side faults flow as
// gRPC errors and are surfaced to the caller.
func (c *Client) Open(ctx context.Context, claimID store.ClaimID, spec store.ClaimSpec) (store.OpenOutcome, error) {
	resp, err := c.rpc.Open(ctx, &genv1.OpenRequest{
		ClaimId:   string(claimID),
		StoreName: spec.StoreName,
		Selector:  spec.Selector,
		Intent:    string(spec.Intent),
		Alias:     spec.Alias,
	})
	if err != nil {
		return store.OpenOutcome{}, fmt.Errorf("remote store %q: Open: %w", c.name, err)
	}
	if u := resp.GetUnavailable(); u != nil {
		return store.OpenOutcome{Available: false}, nil
	}
	acq := resp.GetAcquired()
	if acq == nil {
		return store.OpenOutcome{}, fmt.Errorf("remote store %q: Open: response carries neither Acquired nor Unavailable", c.name)
	}
	return store.OpenOutcome{
		Available: true,
		Result: store.ClaimResult{
			Address: acq.GetAddress(),
			Payload: acq.GetPayload(),
			Region:  acq.GetRegion(),
		},
	}, nil
}

// Commit RPCs to the remote store.
func (c *Client) Commit(ctx context.Context, claimID store.ClaimID, region, address []byte) error {
	_, err := c.rpc.Commit(ctx, &genv1.CommitRequest{
		ClaimId: string(claimID),
		Region:  region,
		Address: address,
	})
	if err != nil {
		return fmt.Errorf("remote store %q: Commit: %w", c.name, err)
	}
	return nil
}

// Abandon RPCs to the remote store. address may be nil when Open's
// response was lost — the store identifies state by claim_id.
func (c *Client) Abandon(ctx context.Context, claimID store.ClaimID, region, address []byte) error {
	_, err := c.rpc.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId: string(claimID),
		Region:  region,
		Address: address,
	})
	if err != nil {
		return fmt.Errorf("remote store %q: Abandon: %w", c.name, err)
	}
	return nil
}

// Release RPCs to the remote store.
func (c *Client) Release(ctx context.Context, claimID store.ClaimID, region, address []byte) error {
	_, err := c.rpc.Release(ctx, &genv1.ReleaseRequest{
		ClaimId: string(claimID),
		Region:  region,
		Address: address,
	})
	if err != nil {
		return fmt.Errorf("remote store %q: Release: %w", c.name, err)
	}
	return nil
}

// Close releases the gRPC connection. Called by Registry.Close on
// shutdown.
func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// ValidateCapabilities compares the cached capability struct against
// the operator-declared block. Strict equality per spec §6.2 — any
// mismatch fails the rimsky process at startup.
func (c *Client) ValidateCapabilities(declared store.Capabilities) error {
	if c.caps != declared {
		return fmt.Errorf("remote store %q: capabilities mismatch — store advertises %+v, operator declared %+v",
			c.name, c.caps, declared)
	}
	return nil
}
