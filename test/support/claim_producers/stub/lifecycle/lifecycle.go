// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle

import (
	"context"
	"sync"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Call struct {
	Method string
	Body   any
}

type Server struct {
	genv1.UnimplementedLifecycleSubscriberServer

	mu    sync.Mutex
	calls []Call
}

func NewServer() *Server { return &Server{} }

func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Server) record(method string, body any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Method: method, Body: body})
}

func (s *Server) OnTemplateRegistered(_ context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	s.record("OnTemplateRegistered", req)
	return &genv1.LifecycleAck{}, nil
}
func (s *Server) OnTemplateDeployed(_ context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	s.record("OnTemplateDeployed", req)
	return &genv1.LifecycleAck{}, nil
}
func (s *Server) OnTemplateUndeployed(_ context.Context, req *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	s.record("OnTemplateUndeployed", req)
	return &genv1.LifecycleAck{}, nil
}
func (s *Server) OnTemplateDeregistered(_ context.Context, req *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	s.record("OnTemplateDeregistered", req)
	return &genv1.LifecycleAck{}, nil
}
func (s *Server) OnInstanceCreated(_ context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	s.record("OnInstanceCreated", req)
	return &genv1.LifecycleAck{}, nil
}
func (s *Server) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	s.record("OnInstanceTerminated", req)
	return &genv1.LifecycleAck{}, nil
}
