// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

// ValidationClient is a remote-gRPC implementation of the rimsky-side
// clientiface.ValidationClient interface. One client per peer that
// advertises the `validation` protocol in rimsky.yml.
type ValidationClient struct {
	name           string
	conn           *grpc.ClientConn
	rpc            genv1.ValidationClient
	supportedRoles []string
}

// Compile-time interface check.
var _ clientiface.ValidationClient = (*ValidationClient)(nil)

// Name returns the operator-configured peer name.
func (c *ValidationClient) Name() string { return c.name }

// SupportedRoles returns the cached `validation_supported_roles` list
// populated at Dial time from the peer's Capabilities (via the
// ClaimProducer or Executor handshake — the Validation service itself
// has no Capabilities verb; the supported roles ride on the host's
// capability surface).
func (c *ValidationClient) SupportedRoles() []string { return c.supportedRoles }

// ValidateExecutor runs the executor-role check.
func (c *ValidationClient) ValidateExecutor(ctx context.Context, in clientiface.ValidateExecutorInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
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
		return nil, nil, fmt.Errorf("remote validation %q: Validate(executor): %w", c.name, err)
	}
	return projectFindings(c.name, "executor", in.NodeAlias, resp.GetErrors()),
		projectFindings(c.name, "executor", in.NodeAlias, resp.GetWarnings()),
		nil
}

// ValidateClaimProducer runs the claim_producer-role check.
func (c *ValidationClient) ValidateClaimProducer(ctx context.Context, in clientiface.ValidateClaimProducerInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	claims := make([]*genv1.ClaimBinding, 0, len(in.Claims))
	for _, b := range in.Claims {
		claims = append(claims, &genv1.ClaimBinding{
			NodeAlias:        b.NodeAlias,
			ClaimAlias:       b.ClaimAlias,
			Selector:         b.Selector,
			Intent:           b.Intent,
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
		return nil, nil, fmt.Errorf("remote validation %q: Validate(claim_producer): %w", c.name, err)
	}
	return projectFindings(c.name, "claim_producer", "", resp.GetErrors()),
		projectFindings(c.name, "claim_producer", "", resp.GetWarnings()),
		nil
}

// ValidateSensor runs the sensor-role check.
func (c *ValidationClient) ValidateSensor(ctx context.Context, in clientiface.ValidateSensorInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	resp, err := c.rpc.Validate(ctx, &genv1.ValidateRequest{
		Role: "sensor",
		Context: &genv1.ValidateRequest_Sensor{
			Sensor: &genv1.SensorContext{
				SensorName:     in.SensorName,
				Kind:           in.Kind,
				ResolvedConfig: in.ResolvedConfig,
			},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("remote validation %q: Validate(sensor): %w", c.name, err)
	}
	return projectFindings(c.name, "sensor", "", resp.GetErrors()),
		projectFindings(c.name, "sensor", "", resp.GetWarnings()),
		nil
}

// ValidateLifecycleSubscriber runs the lifecycle_subscriber-role check.
func (c *ValidationClient) ValidateLifecycleSubscriber(ctx context.Context, in clientiface.ValidateLifecycleSubscriberInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
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
		return nil, nil, fmt.Errorf("remote validation %q: Validate(lifecycle_subscriber): %w", c.name, err)
	}
	return projectFindings(c.name, "lifecycle_subscriber", "", resp.GetErrors()),
		projectFindings(c.name, "lifecycle_subscriber", "", resp.GetWarnings()),
		nil
}

// Close releases the gRPC connection.
func (c *ValidationClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// DialValidation connects to a peer that implements the Validation
// service. `supportedRoles` mirrors the `validation_supported_roles`
// the peer advertised through its host Capabilities handshake (e.g.
// the matching ClaimProducer or Executor Capabilities response).
// tlsMode is the peer entry's validated `tls:` mode (TLSModeOff /
// TLSModeRequired; empty → off).
func DialValidation(_ context.Context, name, endpoint, tlsMode string, supportedRoles []string) (*ValidationClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	// TODO(host-agent-proxy v2): install ServiceName interceptor here when this protocol gains late-bind support
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
		grpc.WithStreamInterceptor(TLSModeStreamInterceptor(name, tlsMode)),
	)
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

// FetchExecutorValidationRoles dials an executor peer's
// ExecutorObservability service and returns the
// validation_supported_roles list from its Capabilities response.
// The Validation service has no Capabilities verb, so an executor
// advertising the validation mix-in carries its supported roles on
// the observability capability surface
// (executor_observability.proto::ObservabilityCapabilities). The
// connection is opened and closed within this call; the RPC is bounded
// by ctx. tlsMode is the peer entry's validated `tls:` mode.
func FetchExecutorValidationRoles(ctx context.Context, name, endpoint, tlsMode string) ([]string, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
	)
	if err != nil {
		return nil, fmt.Errorf("remote executor %q: dial %q: %w", name, endpoint, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err := genv1.NewExecutorObservabilityClient(conn).Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("remote executor %q: ExecutorObservability.Capabilities handshake: %w", name, err)
	}
	// Defensive copy: the caller caches the slice past the proto's lifetime.
	return append([]string(nil), resp.GetValidationSupportedRoles()...), nil
}

// FetchPublisherValidationRoles dials a publisher peer's Publisher
// service and returns the validation_supported_roles list from its
// Capabilities response (publisher.proto::PublisherCapabilities). The
// connection is opened and closed within this call; the RPC is bounded
// by ctx. tlsMode is the peer entry's validated `tls:` mode.
func FetchPublisherValidationRoles(ctx context.Context, name, endpoint, tlsMode string) ([]string, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(TLSModeUnaryInterceptor(name, tlsMode)),
	)
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: dial %q: %w", name, endpoint, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err := genv1.NewPublisherClient(conn).Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("remote publisher %q: Publisher.Capabilities handshake: %w", name, err)
	}
	// Defensive copy: the caller caches the slice past the proto's lifetime.
	return append([]string(nil), resp.GetValidationSupportedRoles()...), nil
}

// projectFindings maps proto ValidationFinding entries to the
// runtime-side ValidationFinding shape.
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
