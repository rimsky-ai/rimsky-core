package lifecycle

import "context"

// LifecycleSubscriber is the Go interface for the LifecycleSubscriber
// service protocol. Implementations return nil from methods they
// don't react to.
type LifecycleSubscriber interface {
	OnTemplateRegistered(ctx context.Context, req OnTemplateRegisteredRequest) error
	OnTemplateDeployed(ctx context.Context, req OnTemplateDeployedRequest) error
	OnTemplateUndeployed(ctx context.Context, req OnTemplateUndeployedRequest) error
	OnTemplateDeregistered(ctx context.Context, req OnTemplateDeregisteredRequest) error
	OnInstanceCreated(ctx context.Context, req OnInstanceCreatedRequest) error
	OnInstanceTerminated(ctx context.Context, req OnInstanceTerminatedRequest) error
}
