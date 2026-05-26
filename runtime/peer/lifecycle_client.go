// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package peer

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// LifecycleClient is a remote-gRPC implementation of the rimsky-side
// LifecycleSubscriber interface. One LifecycleClient per peer that
// declares the lifecycle_subscriber protocol in rimsky.yml.
type LifecycleClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.LifecycleSubscriberClient
}

// Compile-time interface check.
var _ locks.LifecycleSubscriber = (*LifecycleClient)(nil)

// Name returns the operator-configured peer name.
func (c *LifecycleClient) Name() string { return c.name }

func (c *LifecycleClient) OnTemplateRegistered(ctx context.Context, req locks.OnTemplateRegisteredRequest) error {
	_, err := c.rpc.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{
		TemplateHash: req.TemplateHash,
		Spec:         req.Spec,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateRegistered: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateDeployed(ctx context.Context, req locks.OnTemplateDeployedRequest) error {
	_, err := c.rpc.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{
		TemplateHash: req.TemplateHash,
		Tags:         req.Tags,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateDeployed: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateUndeployed(ctx context.Context, req locks.OnTemplateUndeployedRequest) error {
	_, err := c.rpc.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{
		TemplateHash: req.TemplateHash,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateUndeployed: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateDeregistered(ctx context.Context, req locks.OnTemplateDeregisteredRequest) error {
	_, err := c.rpc.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{
		TemplateHash: req.TemplateHash,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateDeregistered: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnInstanceCreated(ctx context.Context, req locks.OnInstanceCreatedRequest) error {
	_, err := c.rpc.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{
		InstanceId:   req.InstanceID,
		TemplateHash: req.TemplateHash,
		InstanceKey:  req.InstanceKey,
		Params:       req.Params,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnInstanceCreated: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnInstanceTerminated(ctx context.Context, req locks.OnInstanceTerminatedRequest) error {
	_, err := c.rpc.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{
		InstanceId:         req.InstanceID,
		TemplateHash:       req.TemplateHash,
		TerminatedAtUnixMs: req.TerminatedAtUnixMs,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnInstanceTerminated: %w", c.name, err)
	}
	return nil
}

// Close releases the gRPC connection.
func (c *LifecycleClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}
