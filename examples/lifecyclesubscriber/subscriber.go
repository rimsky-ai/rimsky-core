// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify LifecycleSubscriber: it receives
// template / instance / run-scope lifecycle notifications from rimsky and
// acknowledges each. Unlike the executor, this protocol is plain unary RPCs and
// serverkit ships an HTTP+JSON bridge for it (see main.go).
//
// Copy this directory, rename the module in go.mod, and replace the bodies with
// your side effects (cache invalidation, provisioning, audit, etc.). Every RPC
// must return a LifecycleAck; rimsky tracks delivery idempotently by scope.
package main

import (
	"context"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Subscriber implements genv1.LifecycleSubscriberServer. Embedding the
// generated Unimplemented server keeps the type forward-compatible if the
// protocol gains RPCs.
type Subscriber struct {
	genv1.UnimplementedLifecycleSubscriberServer
}

var ack = &genv1.LifecycleAck{}

func (s *Subscriber) OnTemplateRegistered(_ context.Context, _ *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnTemplateDeployed(_ context.Context, _ *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnTemplateUndeployed(_ context.Context, _ *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnTemplateDeregistered(_ context.Context, _ *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnInstanceCreated(_ context.Context, _ *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnInstanceTerminated(_ context.Context, _ *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}

func (s *Subscriber) OnRunScopeTerminal(_ context.Context, _ *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	return ack, nil
}
