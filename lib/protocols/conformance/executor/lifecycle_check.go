// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// lifecycle_check.go drives the six lifecycle RPCs against a peer that
// implements LifecycleSubscriber. The probe is shape-only: it sends one
// of each event with synthetic IDs and asserts the call returns success.
// Implementations that don't react to a given event return success
// immediately, which is the published contract.
//
// Lives under the executor conformance package because the cmd binary
// (`cmd/rimsky-executor-conformance`) hosts the `--check-lifecycle`
// flag — the lifecycle protocol has no dedicated conformance binary
// per `concept:conformance`.

package conformance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Synthetic IDs used by every check. The 64-char-`a` template hash is
// shape-valid (sha256-<64-hex>) but not registered anywhere — peers
// that ignore lifecycle events accept it as opaque text.
const (
	syntheticTemplateID = "sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	syntheticInstanceID = "00000000-0000-0000-0000-000000000001"
)

// RunLifecycleCheck dials the lifecycle peer at endpoint and runs the
// six-RPC probe. Returns nil when every RPC returns success; otherwise
// a wrapped error naming the offending verb.
func RunLifecycleCheck(parent context.Context, endpoint string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	target := stripScheme(endpoint)
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	client := genv1.NewLifecycleSubscriberClient(conn)

	type check struct {
		name string
		fn   func() error
	}
	checks := []check{
		{"OnTemplateRegistered", func() error {
			_, err := client.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{TemplateHash: syntheticTemplateID})
			return err
		}},
		{"OnTemplateDeployed", func() error {
			_, err := client.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{TemplateHash: syntheticTemplateID})
			return err
		}},
		{"OnTemplateUndeployed", func() error {
			_, err := client.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{TemplateHash: syntheticTemplateID})
			return err
		}},
		{"OnTemplateDeregistered", func() error {
			_, err := client.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{TemplateHash: syntheticTemplateID})
			return err
		}},
		{"OnInstanceCreated", func() error {
			_, err := client.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{
				TemplateHash: syntheticTemplateID,
				InstanceId:   syntheticInstanceID,
			})
			return err
		}},
		{"OnInstanceTerminated", func() error {
			_, err := client.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{
				TemplateHash: syntheticTemplateID,
				InstanceId:   syntheticInstanceID,
			})
			return err
		}},
	}
	for _, c := range checks {
		if err := c.fn(); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return nil
}

// stripScheme removes the grpc://, http://, https:// prefixes from a
// peer endpoint so it can be passed to grpc.NewClient.
func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return s[len(prefix):]
		}
	}
	return s
}
