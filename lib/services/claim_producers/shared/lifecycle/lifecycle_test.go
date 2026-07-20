// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package lifecycle

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ genv1.LifecycleSubscriberServer = (*Server)(nil)

func TestServer_AllSevenRPCsImplemented(t *testing.T) {
	srv := NewServer()
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"OnTemplateRegistered", func() error {
			_, err := srv.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{})
			return err
		}},
		{"OnTemplateDeployed", func() error {
			_, err := srv.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{})
			return err
		}},
		{"OnTemplateUndeployed", func() error {
			_, err := srv.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{})
			return err
		}},
		{"OnTemplateDeregistered", func() error {
			_, err := srv.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{})
			return err
		}},
		{"OnInstanceCreated", func() error {
			_, err := srv.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{})
			return err
		}},
		{"OnInstanceTerminated", func() error {
			_, err := srv.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{})
			return err
		}},
		{"OnRunScopeTerminal", func() error {
			_, err := srv.OnRunScopeTerminal(ctx, &genv1.OnRunScopeTerminalRequest{})
			return err
		}},
	}

	if len(calls) != 7 {
		t.Fatalf("expected 7 LifecycleSubscriber RPCs under test, got %d", len(calls))
	}

	for _, c := range calls {
		err := c.call()
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if status.Code(err) == codes.Unimplemented {
			t.Errorf("%s: falls through to Unimplemented base", c.name)
		}
	}
}
