// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

// PublisherClient is a remote-gRPC implementation of the rimsky-side
// clientiface.PublisherClient interface. One client per publisher
// service that advertises the `publisher` protocol in rimsky.yml.
type PublisherClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.PublisherClient
}

// @deliberate: compile-time interface check — fails compilation when
// *PublisherClient no longer satisfies clientiface.PublisherClient.
var _ clientiface.PublisherClient = (*PublisherClient)(nil)

// Name returns the operator-configured publisher service name.
func (c *PublisherClient) Name() string { return c.name }

// Subscribe RPCs to the remote publisher.
func (c *PublisherClient) Subscribe(ctx context.Context, req clientiface.SubscribeRequest) error {
	_, err := c.rpc.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: req.PublisherSubscriptionID.String(),
		InstanceId:              req.InstanceID.String(),
		Kind:                    req.Kind,
		ResolvedConfig:          req.ResolvedConfig,
		TargetNode:              req.TargetNode,
		MessageKind:             req.MessageKind,
	})
	if err != nil {
		return fmt.Errorf("remote publisher %q: Subscribe: %w", c.name, err)
	}
	return nil
}

// Unsubscribe RPCs to the remote publisher.
func (c *PublisherClient) Unsubscribe(ctx context.Context, subscriptionID shared.UUID) error {
	_, err := c.rpc.Unsubscribe(ctx, &genv1.UnsubscribeRequest{
		PublisherSubscriptionId: subscriptionID.String(),
	})
	if err != nil {
		return fmt.Errorf("remote publisher %q: Unsubscribe: %w", c.name, err)
	}
	return nil
}

// ListSubscriptions RPCs to the remote publisher.
func (c *PublisherClient) ListSubscriptions(ctx context.Context) ([]clientiface.ListedPublisherSubscription, error) {
	resp, err := c.rpc.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: ListSubscriptions: %w", c.name, err)
	}
	out := make([]clientiface.ListedPublisherSubscription, 0, len(resp.GetSubscriptions()))
	for _, w := range resp.GetSubscriptions() {
		wid, err := uuid.Parse(w.GetPublisherSubscriptionId())
		if err != nil {
			return nil, fmt.Errorf("remote publisher %q: ListSubscriptions: bad publisher_subscription_id %q: %w", c.name, w.GetPublisherSubscriptionId(), err)
		}
		iid, err := uuid.Parse(w.GetInstanceId())
		if err != nil {
			return nil, fmt.Errorf("remote publisher %q: ListSubscriptions: bad instance_id %q: %w", c.name, w.GetInstanceId(), err)
		}
		out = append(out, clientiface.ListedPublisherSubscription{
			PublisherSubscriptionID: shared.UUID(wid),
			InstanceID:              shared.UUID(iid),
			Kind:                    w.GetKind(),
			TargetNode:              w.GetTargetNode(),
			MessageKind:             w.GetMessageKind(),
		})
	}
	return out, nil
}

// Close releases the gRPC connection.
func (c *PublisherClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// DialPublisher connects to a peer that implements the Publisher
// service. tlsMode is the peer entry's validated `tls:` mode
// (TLSModeOff / TLSModeRequired; empty → off).
func DialPublisher(_ context.Context, name, endpoint, tlsMode string) (*PublisherClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
		grpc.WithStreamInterceptor(TLSModeStreamInterceptor(name, tlsMode)),
	)
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: dial %q: %w", name, endpoint, err)
	}
	return &PublisherClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewPublisherClient(conn),
	}, nil
}
