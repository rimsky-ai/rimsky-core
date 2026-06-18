// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.


package conformance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const (
	syntheticTemplateID = "sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	syntheticInstanceID = "00000000-0000-0000-0000-000000000001"
)

func RunLifecycleCheck(parent context.Context, endpoint, tlsMode string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	target := stripScheme(endpoint)
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(transportCredsFor(tlsMode)))
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

func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return s[len(prefix):]
		}
	}
	return s
}
