// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package peer

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type ValidationClient struct {
	name           string
	conn           *grpc.ClientConn
	rpc            genv1.ValidationClient
	supportedRoles []string
}

var _ clientiface.ValidationClient = (*ValidationClient)(nil)

func (c *ValidationClient) Name() string { return c.name }

func (c *ValidationClient) SupportedRoles() []string { return c.supportedRoles }

func (c *ValidationClient) ValidateExecutor(ctx context.Context, in clientiface.ValidateExecutorInput) (clientiface.ValidationOutcome, error) {
	resp, err := c.rpc.Validate(ctx, &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{
			Executor: &genv1.ExecutorContext{
				NodeAlias:        in.NodeAlias,
				AttributesSchema: in.AttributesSchema,
				ClaimAliases:     in.ClaimAliases,
			},
		},
	})
	if err != nil {
		return clientiface.ValidationOutcome{}, fmt.Errorf("remote validation %q: Validate(executor): %w", c.name, err)
	}
	return clientiface.ValidationOutcome{
		Errors:   projectFindings(c.name, "executor", in.NodeAlias, resp.GetErrors()),
		Warnings: projectFindings(c.name, "executor", in.NodeAlias, resp.GetWarnings()),
	}, nil
}

func (c *ValidationClient) ValidateClaimProducer(ctx context.Context, in clientiface.ValidateClaimProducerInput) (clientiface.ValidationOutcome, error) {
	claims := make([]*genv1.ClaimBinding, 0, len(in.Claims))
	for _, b := range in.Claims {
		claims = append(claims, &genv1.ClaimBinding{
			NodeAlias:        b.NodeAlias,
			ClaimAlias:       b.ClaimAlias,
			Selector:         b.Selector,
			Intent:           string(b.Intent),
			Lifetime:         b.Lifetime,
			Data:             b.Data,
			PartitionRequest: b.PartitionRequest,
		})
	}
	resp, err := c.rpc.Validate(ctx, &genv1.ValidateRequest{
		Role: "claim_producer",
		Context: &genv1.ValidateRequest_ClaimProducer{
			ClaimProducer: &genv1.ClaimProducerContext{
				ProducerName: in.ProducerName,
				Claims:       claims,
			},
		},
	})
	if err != nil {
		return clientiface.ValidationOutcome{}, fmt.Errorf("remote validation %q: Validate(claim_producer): %w", c.name, err)
	}
	return clientiface.ValidationOutcome{
		Errors:   projectFindings(c.name, "claim_producer", "", resp.GetErrors()),
		Warnings: projectFindings(c.name, "claim_producer", "", resp.GetWarnings()),
	}, nil
}

func (c *ValidationClient) ValidatePublisher(ctx context.Context, in clientiface.ValidatePublisherInput) (clientiface.ValidationOutcome, error) {
	resp, err := c.rpc.Validate(ctx, &genv1.ValidateRequest{
		Role: "publisher",
		Context: &genv1.ValidateRequest_Publisher{
			Publisher: &genv1.PublisherContext{
				PublisherName:  in.PublisherName,
				Kind:           in.Kind,
				ResolvedConfig: in.ResolvedConfig,
			},
		},
	})
	if err != nil {
		return clientiface.ValidationOutcome{}, fmt.Errorf("remote validation %q: Validate(publisher): %w", c.name, err)
	}
	return clientiface.ValidationOutcome{
		Errors:   projectFindings(c.name, "publisher", "", resp.GetErrors()),
		Warnings: projectFindings(c.name, "publisher", "", resp.GetWarnings()),
	}, nil
}

func (c *ValidationClient) ValidateLifecycleSubscriber(ctx context.Context, in clientiface.ValidateLifecycleSubscriberInput) (clientiface.ValidationOutcome, error) {
	resp, err := c.rpc.Validate(ctx, &genv1.ValidateRequest{
		Role: "lifecycle_subscriber",
		Context: &genv1.ValidateRequest_LifecycleSubscriber{
			LifecycleSubscriber: &genv1.LifecycleSubscriberContext{
				SubscriberName: in.SubscriberName,
				TemplateId:     in.TemplateID,
			},
		},
	})
	if err != nil {
		return clientiface.ValidationOutcome{}, fmt.Errorf("remote validation %q: Validate(lifecycle_subscriber): %w", c.name, err)
	}
	return clientiface.ValidationOutcome{
		Errors:   projectFindings(c.name, "lifecycle_subscriber", "", resp.GetErrors()),
		Warnings: projectFindings(c.name, "lifecycle_subscriber", "", resp.GetWarnings()),
	}, nil
}

func (c *ValidationClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func DialValidation(_ context.Context, name, endpoint, tlsMode string, supportedRoles []string) (*ValidationClient, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, dialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("remote validation %q: dial %q: %w", name, endpoint, err)
	}
	return &ValidationClient{
		name:           name,
		conn:           conn,
		rpc:            genv1.NewValidationClient(conn),
		supportedRoles: append([]string(nil), supportedRoles...),
	}, nil
}

func FetchExecutorValidationRoles(ctx context.Context, name, endpoint, tlsMode string) ([]string, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, capabilityDialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("remote executor %q: dial %q: %w", name, endpoint, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err := genv1.NewExecutorObservabilityClient(conn).Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("remote executor %q: ExecutorObservability.Capabilities handshake: %w", name, err)
	}
	return append([]string(nil), resp.GetValidationSupportedRoles()...), nil
}

func FetchPublisherValidationRoles(ctx context.Context, name, endpoint, tlsMode string) ([]string, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, capabilityDialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: dial %q: %w", name, endpoint, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err := genv1.NewPublisherClient(conn).Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: Publisher.Capabilities handshake: %w", name, err)
	}
	return append([]string(nil), resp.GetValidationSupportedRoles()...), nil
}

func projectFindings(serviceName, role, nodeAlias string, src []*genv1.ValidationFinding) []clientiface.ValidationFinding {
	if len(src) == 0 {
		return nil
	}
	out := make([]clientiface.ValidationFinding, 0, len(src))
	for _, f := range src {
		out = append(out, clientiface.ValidationFinding{
			ServiceName: serviceName,
			Role:        role,
			NodeAlias:   nodeAlias,
			Class:       f.GetClass(),
			Message:     f.GetMessage(),
			Path:        f.GetPath(),
		})
	}
	return out
}
