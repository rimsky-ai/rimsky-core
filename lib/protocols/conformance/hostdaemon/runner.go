// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// @concept: conformance
// @concept: host-daemon
// @decision: conformance-suite-per-protocol

package hostdaemon

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/check"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type CheckResult = check.Result

type RunOpts struct {
	Registration Registration
}

func Run(ctx context.Context, client genv1.HostDaemonClient, opts RunOpts) []CheckResult {
	primary, err := register(ctx, client, opts.Registration)
	if err != nil {
		return []CheckResult{{Name: "Register", Err: err}}
	}
	defer primary.close()

	results := []CheckResult{registerAckCheck(primary)}
	if results[0].Err != nil {
		return results
	}
	results = append(results,
		heartbeatCheck(ctx, primary),
		duplicateRegisterCheck(ctx, primary, opts.Registration),
		unknownSpawnReapedCheck(ctx, primary),
		unknownStreamDispatchCheck(ctx, primary),
		localHTTPForwardCheck(ctx, primary),
		emptyAPIKeyCheck(ctx, client, opts.Registration),
		firstFrameCheck(ctx, client),
		routingIdentityExclusivityCheck(ctx, client, primary, opts.Registration),
	)
	return results
}

func registerAckCheck(s *session) CheckResult {
	const name = "RegisterAck"
	if s.ack.GetProxyVersion() == "" {
		return CheckResult{Name: name, Err: fmt.Errorf("RegisterAck.proxy_version is empty; the ack must name the protocol version the server speaks")}
	}
	if s.ack.GetRoutingIdentity() == "" {
		return CheckResult{Name: name, Err: fmt.Errorf("RegisterAck.routing_identity is empty; a daemon cannot learn the identity dispatches reach it under")}
	}
	return CheckResult{Name: name}
}

func heartbeatCheck(ctx context.Context, s *session) CheckResult {
	const name = "HeartbeatAcked"
	ack, err := roundTripHeartbeat(ctx, s)
	if err != nil {
		return CheckResult{Name: name, Err: err}
	}
	if ack.GetReceivedAtUnixMs() <= 0 {
		return CheckResult{Name: name, Err: fmt.Errorf("HostDaemonHeartbeatAck.received_at_unix_ms is %d; the ack must carry the instant the server received the heartbeat", ack.GetReceivedAtUnixMs())}
	}
	return CheckResult{Name: name}
}

func roundTripHeartbeat(ctx context.Context, s *session) (*genv1.HostDaemonHeartbeatAck, error) {
	if err := s.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{
		Heartbeat: &genv1.HostDaemonHeartbeat{SentAtUnixMs: 1},
	}}); err != nil {
		return nil, fmt.Errorf("send heartbeat: %w", err)
	}
	return s.awaitHeartbeat(ctx)
}

func duplicateRegisterCheck(ctx context.Context, s *session, reg Registration) CheckResult {
	const name = "DuplicateRegisterIgnored"
	if err := s.send(reg.frame()); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("send second Register: %w", err)}
	}
	if _, err := roundTripHeartbeat(ctx, s); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("the connection did not survive a second Register on an established stream: %w", err)}
	}
	return CheckResult{Name: name}
}

func unknownSpawnReapedCheck(ctx context.Context, s *session) CheckResult {
	const name = "UnknownSpawnReapedIgnored"
	if err := s.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Reaped{Reaped: &genv1.Reaped{
		SpawnId: uuid.NewString(), Clean: true,
	}}}); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("send Reaped: %w", err)}
	}
	if _, err := roundTripHeartbeat(ctx, s); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("the connection did not survive a Reaped naming a spawn the server never ordered: %w", err)}
	}
	return CheckResult{Name: name}
}

func unknownStreamDispatchCheck(ctx context.Context, s *session) CheckResult {
	const name = "UnknownStreamDispatchIgnored"
	if err := s.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  uuid.NewString(),
		Protocol: "executor",
		StreamId: uuid.NewString(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}}); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("send DispatchFrame: %w", err)}
	}
	if _, err := roundTripHeartbeat(ctx, s); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("the connection did not survive a DispatchFrame naming a stream the server never opened: %w", err)}
	}
	return CheckResult{Name: name}
}

func localHTTPForwardCheck(ctx context.Context, s *session) CheckResult {
	const name = "LocalHttpForwardAnswered"
	forwardID := uuid.NewString()
	if err := s.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_HttpForward{HttpForward: &genv1.LocalHttpForward{
		ForwardId: forwardID,
		Method:    "POST",
		Url:       "http://conformance.invalid/v1/callback/conformance",
		SpawnId:   uuid.NewString(),
	}}}); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("send LocalHttpForward: %w", err)}
	}
	resp, err := s.awaitForward(ctx)
	if err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("the server left a LocalHttpForward unanswered; it must answer every forward, so a daemon never blocks on a lost callback: %w", err)}
	}
	if resp.GetForwardId() != forwardID {
		return CheckResult{Name: name, Err: fmt.Errorf("LocalHttpResponse.forward_id is %q, want the forward's own id %q", resp.GetForwardId(), forwardID)}
	}
	if resp.GetStatus() == 0 {
		return CheckResult{Name: name, Err: fmt.Errorf("LocalHttpResponse.status is 0; the answer must carry an HTTP status the daemon can relay back to the spawned process")}
	}
	return CheckResult{Name: name}
}

func emptyAPIKeyCheck(ctx context.Context, client genv1.HostDaemonClient, reg Registration) CheckResult {
	const name = "RegisterRefusesAnEmptyApiKey"
	anonymous := reg
	anonymous.APIKey = ""
	return refusalCheck(name, codes.InvalidArgument, func() error {
		s, err := register(ctx, client, anonymous)
		if err == nil {
			s.close()
		}
		return err
	})
}

func firstFrameCheck(ctx context.Context, client genv1.HostDaemonClient) CheckResult {
	const name = "ConnectRefusesANonRegisterFirstFrame"
	return refusalCheck(name, codes.InvalidArgument, func() error {
		stream, cancel, err := openStream(ctx, client)
		if err != nil {
			return err
		}
		defer cancel()
		if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{
			Heartbeat: &genv1.HostDaemonHeartbeat{SentAtUnixMs: 1},
		}}); err != nil {
			return err
		}
		_, err = stream.Recv()
		return err
	})
}

func refusalCheck(name string, want codes.Code, attempt func() error) CheckResult {
	err := attempt()
	if err == nil {
		return CheckResult{Name: name, Err: fmt.Errorf("the server accepted the connection; it must refuse with %s", want)}
	}
	if got := status.Code(err); got != want {
		return CheckResult{Name: name, Err: fmt.Errorf("the server refused with %s (%v), want %s", got, err, want)}
	}
	return CheckResult{Name: name}
}

func routingIdentityExclusivityCheck(ctx context.Context, client genv1.HostDaemonClient, primary *session, reg Registration) CheckResult {
	const name = "OneConnectionPerRoutingIdentity"
	second, err := register(ctx, client, reg)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return CheckResult{Name: name}
		}
		return CheckResult{Name: name, Err: fmt.Errorf("a second Register under the same credentials failed with an unexpected code %s: %w", status.Code(err), err)}
	}
	defer second.close()
	if second.ack.GetRoutingIdentity() != primary.ack.GetRoutingIdentity() {
		return CheckResult{Name: name}
	}
	if !second.ack.GetDisplacedPrior() {
		return CheckResult{Name: name, Err: fmt.Errorf("two live connections share routing identity %q and the second ack does not report displaced_prior; a dispatch for that identity would reach an arbitrary one of them", second.ack.GetRoutingIdentity())}
	}
	return CheckResult{Name: name}
}
