// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSubscriberAcksLifecycle(t *testing.T) {
	s := &Subscriber{}
	ctx := context.Background()

	if a, err := s.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{InstanceId: "i1"}); err != nil || a == nil {
		t.Fatalf("OnInstanceCreated: ack=%v err=%v", a, err)
	}
	if a, err := s.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{InstanceId: "i1"}); err != nil || a == nil {
		t.Fatalf("OnInstanceTerminated: ack=%v err=%v", a, err)
	}
	if a, err := s.OnRunScopeTerminal(ctx, &genv1.OnRunScopeTerminalRequest{RunScopeId: "r1"}); err != nil || a == nil {
		t.Fatalf("OnRunScopeTerminal: ack=%v err=%v", a, err)
	}
	if a, err := s.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{}); err != nil || a == nil {
		t.Fatalf("OnTemplateRegistered: ack=%v err=%v", a, err)
	}
	if a, err := s.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{}); err != nil || a == nil {
		t.Fatalf("OnTemplateDeployed: ack=%v err=%v", a, err)
	}
	if a, err := s.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{}); err != nil || a == nil {
		t.Fatalf("OnTemplateUndeployed: ack=%v err=%v", a, err)
	}
	if a, err := s.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{}); err != nil || a == nil {
		t.Fatalf("OnTemplateDeregistered: ack=%v err=%v", a, err)
	}
}
