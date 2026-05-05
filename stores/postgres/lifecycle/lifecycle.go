// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package lifecycle implements the LifecycleSubscriber gRPC service for
// the postgres store-service. The standard postgres store maintains no
// template or instance metadata of its own; every method returns
// success without side effects. Operators that need postgres-side
// reactions (e.g. bootstrapping per-template items tables on
// OnTemplateDeployed) can fork this package.
package lifecycle

import (
	"context"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Server is the LifecycleSubscriber implementation.
type Server struct {
	genv1.UnimplementedLifecycleSubscriberServer
}

// NewServer returns a fresh Server.
func NewServer() *Server { return &Server{} }

func (*Server) OnTemplateRegistered(_ context.Context, _ *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
func (*Server) OnTemplateDeployed(_ context.Context, _ *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
func (*Server) OnTemplateUndeployed(_ context.Context, _ *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
func (*Server) OnTemplateDeregistered(_ context.Context, _ *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
func (*Server) OnInstanceCreated(_ context.Context, _ *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
func (*Server) OnInstanceTerminated(_ context.Context, _ *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
