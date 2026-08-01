// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle

import (
	"context"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Server struct {
	genv1.UnimplementedLifecycleSubscriberServer
}

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
func (*Server) OnRunScopeTerminal(_ context.Context, _ *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
