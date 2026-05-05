// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package lifecycle

import "context"

// LifecycleSubscriber is the Go interface for the LifecycleSubscriber
// service protocol. Implementations return nil from methods they
// don't react to.
type LifecycleSubscriber interface {
	// Name returns the operator-configured peer name (matches the
	// peer's name in rimsky.yml under claim_producers: or executors:).
	// Rimsky-side identifier; not transported over the wire.
	Name() string

	OnTemplateRegistered(ctx context.Context, req OnTemplateRegisteredRequest) error
	OnTemplateDeployed(ctx context.Context, req OnTemplateDeployedRequest) error
	OnTemplateUndeployed(ctx context.Context, req OnTemplateUndeployedRequest) error
	OnTemplateDeregistered(ctx context.Context, req OnTemplateDeregisteredRequest) error
	OnInstanceCreated(ctx context.Context, req OnInstanceCreatedRequest) error
	OnInstanceTerminated(ctx context.Context, req OnInstanceTerminatedRequest) error
}
