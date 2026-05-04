package remote

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/fallguy/rimsky/foundation/locks"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
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

func (c *LifecycleClient) OnTemplateRegistered(ctx context.Context, templateID string) error {
	_, err := c.rpc.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{TemplateHash: templateID})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateRegistered: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateDeployed(ctx context.Context, templateID string) error {
	_, err := c.rpc.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{TemplateHash: templateID})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateDeployed: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateUndeployed(ctx context.Context, templateID string) error {
	_, err := c.rpc.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{TemplateHash: templateID})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateUndeployed: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnTemplateDeregistered(ctx context.Context, templateID string) error {
	_, err := c.rpc.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{TemplateHash: templateID})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnTemplateDeregistered: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnInstanceCreated(ctx context.Context, templateID, instanceID string) error {
	_, err := c.rpc.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{
		InstanceId:   instanceID,
		TemplateHash: templateID,
	})
	if err != nil {
		return fmt.Errorf("lifecycle subscriber %q: OnInstanceCreated: %w", c.name, err)
	}
	return nil
}

func (c *LifecycleClient) OnInstanceTerminated(ctx context.Context, templateID, instanceID string) error {
	_, err := c.rpc.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{
		InstanceId:   instanceID,
		TemplateHash: templateID,
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
