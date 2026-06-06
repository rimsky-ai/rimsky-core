// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestSubscriberAcksLifecycle asserts the subscriber returns a non-nil ack and
// no error for representative notifications across the lifecycle surface.
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
}
