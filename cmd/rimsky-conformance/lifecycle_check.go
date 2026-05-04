// lifecycle_check.go drives the six lifecycle RPCs against a peer that
// implements LifecycleSubscriber. The probe is shape-only: it sends one
// of each event with synthetic IDs and asserts the call returns success.
// Implementations that don't react to a given event return success
// immediately, which is the published contract.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/integration/remote"
	"github.com/fallguy/rimsky/foundation/locks"
)

// Synthetic IDs used by every check. The 64-char-`a` template hash is
// shape-valid (sha256-<64-hex>) but not registered anywhere — peers
// that ignore lifecycle events accept it as opaque text.
const (
	syntheticTemplateID = "sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	syntheticInstanceID = "00000000-0000-0000-0000-000000000001"
)

// runLifecycleCheck dials the lifecycle peer at endpoint and runs the
// six-RPC probe. Returns nil when every RPC returns success; otherwise
// a wrapped error naming the offending verb.
func runLifecycleCheck(parent context.Context, endpoint string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	client, err := remote.DialLifecycle(ctx, "conformance-target", endpoint)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	type check struct {
		name string
		fn   func() error
	}
	checks := []check{
		{"OnTemplateRegistered", func() error {
			return client.OnTemplateRegistered(ctx, locks.OnTemplateRegisteredRequest{TemplateHash: syntheticTemplateID})
		}},
		{"OnTemplateDeployed", func() error {
			return client.OnTemplateDeployed(ctx, locks.OnTemplateDeployedRequest{TemplateHash: syntheticTemplateID})
		}},
		{"OnTemplateUndeployed", func() error {
			return client.OnTemplateUndeployed(ctx, locks.OnTemplateUndeployedRequest{TemplateHash: syntheticTemplateID})
		}},
		{"OnTemplateDeregistered", func() error {
			return client.OnTemplateDeregistered(ctx, locks.OnTemplateDeregisteredRequest{TemplateHash: syntheticTemplateID})
		}},
		{"OnInstanceCreated", func() error {
			return client.OnInstanceCreated(ctx, locks.OnInstanceCreatedRequest{
				TemplateHash: syntheticTemplateID,
				InstanceID:   syntheticInstanceID,
			})
		}},
		{"OnInstanceTerminated", func() error {
			return client.OnInstanceTerminated(ctx, locks.OnInstanceTerminatedRequest{
				TemplateHash: syntheticTemplateID,
				InstanceID:   syntheticInstanceID,
			})
		}},
	}
	for _, c := range checks {
		if err := c.fn(); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return nil
}
