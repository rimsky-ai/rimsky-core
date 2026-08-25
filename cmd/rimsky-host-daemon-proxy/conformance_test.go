// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	claimproducerconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	executorconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	stub "github.com/rimsky-ai/rimsky-core/test/support/executors/stub"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"

	hostdaemonconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/hostdaemon"
)

type conformanceExecStub struct {
	*stub.Stub
}

func newConformanceExecStub() *conformanceExecStub {
	return &conformanceExecStub{Stub: stub.New().EnableStubMode()}
}

func (s *conformanceExecStub) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	if _, invalid := attrs[stubmode.MalformedShapeAttribute]; invalid {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: "conformance_stub/malformed_attributes",
		}}}, nil
	}
	return s.Stub.Execute(ctx, req)
}

func startConformanceExecChild(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, newConformanceExecStub())
	stub.RegisterObservability(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func startRealProxyServer(t *testing.T, owner, bindingName, bindingPath string) (addr string, state *proxyState) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	state = newProxyState()
	srv := grpc.NewServer()
	genv1.RegisterHostDaemonServer(srv, newDaemonServer(state, presentedKeyIsIdentity))

	fetch := func(_ context.Context, _ string) (*instanceCacheEntry, bool, error) {
		return &instanceCacheEntry{
			serviceBindings:       map[string]bindingSpec{bindingName: {Path: bindingPath}},
			targetRoutingIdentity: owner,
			params:                map[string]any{"cwd": "."},
		}, true, nil
	}
	cfg := Config{SpawnReadyTimeout: 10 * time.Second, ReapTimeout: 5 * time.Second}
	genv1.RegisterExecutorServer(srv, &executorHandler{state: state, fetch: fetch, spawnTimeout: cfg.SpawnReadyTimeout})
	genv1.RegisterExecutorObservabilityServer(srv, newExecutorObsHandler())
	genv1.RegisterClaimProducerServer(srv, &claimProducerHandler{state: state, fetch: fetch, spawnTimeout: cfg.SpawnReadyTimeout, callTimeout: cfg.SpawnReadyTimeout})
	genv1.RegisterClaimProducerObservabilityServer(srv, newClaimProducerObsHandler())

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), state
}

type tunnelDaemon struct {
	stream genv1.HostDaemon_ConnectClient
	sendCh chan *genv1.ClientFrame

	execConn  *grpc.ClientConn
	claimConn *grpc.ClientConn

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func startTunnelDaemon(t *testing.T, proxyAddr, apiKey, execAddr, claimAddr string) *tunnelDaemon {
	t.Helper()
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := genv1.NewHostDaemonClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey: apiKey,
	}}}); err != nil {
		t.Fatalf("send register: %v", err)
	}
	ackFrame, err := stream.Recv()
	if err != nil || ackFrame.GetRegisterAck() == nil {
		t.Fatalf("register ack: err=%v frame=%T", err, ackFrame.GetBody())
	}

	ta := &tunnelDaemon{
		stream:  stream,
		sendCh:  make(chan *genv1.ClientFrame, 64),
		cancels: map[string]context.CancelFunc{},
	}
	if execAddr != "" {
		ta.execConn, err = grpc.NewClient(execAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial exec child: %v", err)
		}
		t.Cleanup(func() { _ = ta.execConn.Close() })
	}
	if claimAddr != "" {
		ta.claimConn, err = grpc.NewClient(claimAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial claim child: %v", err)
		}
		t.Cleanup(func() { _ = ta.claimConn.Close() })
	}

	go ta.writeLoop()
	go ta.readLoop()
	return ta
}

func (ta *tunnelDaemon) writeLoop() {
	for frame := range ta.sendCh {
		if err := ta.stream.Send(frame); err != nil {
			return
		}
	}
}

func (ta *tunnelDaemon) send(frame *genv1.ClientFrame) {
	ta.sendCh <- frame
}

func (ta *tunnelDaemon) readLoop() {
	for {
		frame, err := ta.stream.Recv()
		if err != nil {
			return
		}
		switch body := frame.GetBody().(type) {
		case *genv1.ServerFrame_Spawn:
			go ta.handleSpawn(body.Spawn)
		case *genv1.ServerFrame_DispatchFrame:
			df := body.DispatchFrame
			if df.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
				ta.cancelStream(df.GetStreamId())
				continue
			}
			go ta.handleDispatch(df)
		case *genv1.ServerFrame_Reap:
			ta.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Reaped{Reaped: &genv1.Reaped{
				SpawnId: body.Reap.GetSpawnId(), Clean: true,
			}}})
		}
	}
}

func (ta *tunnelDaemon) handleSpawn(sp *genv1.Spawn) {
	caps := map[string][]byte{}
	for _, p := range sp.GetExpectedProtocols() {
		switch p {
		case protocolExecutor:
			resp, err := genv1.NewExecutorObservabilityClient(ta.execConn).Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
			if err != nil {
				ta.sendSpawnFailed(sp.GetSpawnId(), err)
				return
			}
			b, err := proto.Marshal(resp)
			if err != nil {
				ta.sendSpawnFailed(sp.GetSpawnId(), err)
				return
			}
			caps[p] = b
		case protocolClaimProducer:
			resp, err := genv1.NewClaimProducerClient(ta.claimConn).Capabilities(context.Background(), &genv1.CapabilitiesRequest{})
			if err != nil {
				ta.sendSpawnFailed(sp.GetSpawnId(), err)
				return
			}
			b, err := proto.Marshal(resp)
			if err != nil {
				ta.sendSpawnFailed(sp.GetSpawnId(), err)
				return
			}
			caps[p] = b
		}
	}
	ta.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: &genv1.SpawnAck{
		SpawnId: sp.GetSpawnId(), Status: genv1.SpawnAck_SPAWN_STATUS_READY, Capabilities: caps,
	}}})
}

func (ta *tunnelDaemon) sendSpawnFailed(spawnID string, err error) {
	ta.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: &genv1.SpawnAck{
		SpawnId: spawnID,
		Status:  genv1.SpawnAck_SPAWN_STATUS_FAILED,
		Error:   &genv1.HostDaemonError{Class: errClassSpawnFailed, Message: err.Error()},
	}}})
}

func (ta *tunnelDaemon) registerCancel(streamID string, cancel context.CancelFunc) {
	ta.mu.Lock()
	ta.cancels[streamID] = cancel
	ta.mu.Unlock()
}

func (ta *tunnelDaemon) clearCancel(streamID string) {
	ta.mu.Lock()
	delete(ta.cancels, streamID)
	ta.mu.Unlock()
}

func (ta *tunnelDaemon) cancelStream(streamID string) {
	ta.mu.Lock()
	cancel, ok := ta.cancels[streamID]
	ta.mu.Unlock()
	if ok {
		cancel()
	}
}

func (ta *tunnelDaemon) handleDispatch(df *genv1.DispatchFrame) {
	ctx, cancel := context.WithCancel(context.Background())
	ta.registerCancel(df.GetStreamId(), cancel)
	defer ta.clearCancel(df.GetStreamId())
	defer cancel()

	switch df.GetProtocol() {
	case protocolExecutor:
		ta.dispatchExecute(ctx, df)
	case protocolClaimProducer:
		ta.dispatchClaimProducer(ctx, df)
	default:
		ta.sendDispatchCancel(df)
	}
}

func (ta *tunnelDaemon) dispatchExecute(ctx context.Context, df *genv1.DispatchFrame) {
	var req genv1.ExecuteRequest
	if err := proto.Unmarshal(df.GetPayload(), &req); err != nil {
		ta.sendDispatchCancel(df)
		return
	}
	outcome, err := genv1.NewExecutorClient(ta.execConn).Execute(ctx, &req)
	if err != nil {
		ta.sendDispatchCancel(df)
		return
	}
	payload, err := proto.Marshal(outcome)
	if err != nil {
		ta.sendDispatchCancel(df)
		return
	}
	ta.sendDispatchData(df, payload)
}

func (ta *tunnelDaemon) dispatchClaimProducer(ctx context.Context, df *genv1.DispatchFrame) {
	client := genv1.NewClaimProducerClient(ta.claimConn)
	payload, err := forwardConformanceClaimProducerVerb(ctx, client, df.GetClaimProducerVerb(), df.GetPayload())
	if err != nil {
		ta.sendDispatchCancel(df)
		return
	}
	ta.sendDispatchData(df, payload)
}

func forwardConformanceClaimProducerVerb(ctx context.Context, client genv1.ClaimProducerClient, verb genv1.DispatchFrame_ClaimProducerVerb, payload []byte) ([]byte, error) {
	switch verb {
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN:
		var req genv1.OpenRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		resp, err := client.Open(ctx, &req)
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_COMMIT:
		var req genv1.CommitRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		resp, err := client.Commit(ctx, &req)
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON:
		var req genv1.AbandonRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		resp, err := client.Abandon(ctx, &req)
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE:
		var req genv1.ReleaseRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		resp, err := client.Release(ctx, &req)
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	default:
		return nil, errUnknownClaimProducerVerb
	}
}

var errUnknownClaimProducerVerb = errors.New("unknown claim_producer_verb")

func (ta *tunnelDaemon) sendDispatchData(df *genv1.DispatchFrame, payload []byte) {
	ta.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		Payload:  payload,
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
}

func (ta *tunnelDaemon) sendDispatchCancel(df *genv1.DispatchFrame) {
	ta.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
	}}})
}

func TestProxyPassesExecutorConformanceSuite(t *testing.T) {
	execAddr := startConformanceExecChild(t)
	proxyAddr, _ := startRealProxyServer(t, "owner-1", "codegen", "./codegen")
	startTunnelDaemon(t, proxyAddr, "owner-1", execAddr, "")

	ctx := metadata.AppendToOutgoingContext(context.Background(), serviceNameHeader, "codegen")
	results, err := executorconf.Run(ctx, executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: proxyAddr},
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("executor conformance run: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("no executor conformance scenarios ran")
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("scenario %q unexpectedly skipped through the proxy: %s", r.Scenario, r.Error)
			continue
		}
		if !r.Passed {
			t.Errorf("scenario %q failed through the proxy: %s", r.Scenario, r.Error)
		}
	}
}

type proxyClaimProducerClient struct {
	name       string
	instanceID string
	rpc        genv1.ClaimProducerClient
	caps       claimproducer.Capabilities
}

func dialProxyClaimProducer(t *testing.T, proxyAddr, bindingName, instanceID string) *proxyClaimProducerClient {
	t.Helper()
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	rpc := genv1.NewClaimProducerClient(conn)
	ctx := metadata.AppendToOutgoingContext(context.Background(), serviceNameHeader, bindingName)
	resp, err := rpc.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("proxy Capabilities: %v", err)
	}
	caps, err := bridge.ClaimProducerCapabilitiesFromProto(resp)
	if err != nil {
		t.Fatalf("decode proxy Capabilities: %v", err)
	}
	return &proxyClaimProducerClient{name: bindingName, instanceID: instanceID, rpc: rpc, caps: caps}
}

func (c *proxyClaimProducerClient) Name() string { return c.name }

func (c *proxyClaimProducerClient) ctxWithHeader(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, serviceNameHeader, c.name)
}

func (c *proxyClaimProducerClient) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return c.caps, nil
}

func (c *proxyClaimProducerClient) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	spec.InstanceID = c.instanceID
	resp, err := c.rpc.Open(c.ctxWithHeader(ctx), bridge.OpenRequestFromSpec(claimID, spec))
	if err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	return bridge.OpenOutcomeFromProto(resp)
}

func (c *proxyClaimProducerClient) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	resp, err := c.rpc.Commit(ctx, bridge.CommitRequestFromArgs(claimID, scope, address, leaseToken))
	if err != nil {
		return claimproducer.CommitResult{}, err
	}
	return bridge.CommitResultFromProto(resp), nil
}

func (c *proxyClaimProducerClient) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := c.rpc.Abandon(ctx, bridge.AbandonRequestFromArgs(claimID, scope, address, leaseToken))
	return err
}

func (c *proxyClaimProducerClient) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	_, err := c.rpc.Release(ctx, bridge.ReleaseRequestFromArgs(claimID, scope, address, leaseToken))
	return err
}

func (c *proxyClaimProducerClient) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	if !c.caps.SupportsSplitScope {
		return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	resp, err := c.rpc.SplitScope(ctx, bridge.SplitScopeRequestToProto(req))
	if err != nil {
		return claimproducer.SplitClaimScopeResponse{}, err
	}
	return bridge.SplitScopeResponseFromProto(resp), nil
}

func (c *proxyClaimProducerClient) ScopesConflict(ctx context.Context, a, b []byte) (bool, error) {
	if !c.caps.SupportsScopesConflict {
		return false, claimproducer.ErrScopesConflictUnsupported
	}
	resp, err := c.rpc.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{ClaimScopeA: a, ClaimScopeB: b})
	if err != nil {
		return false, err
	}
	return resp.GetConflicts(), nil
}

func TestProxyPassesClaimProducerConformanceSuite(t *testing.T) {
	claimAddr, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	proxyAddr, _ := startRealProxyServer(t, "owner-1", "fs-claims", "./fs")
	startTunnelDaemon(t, proxyAddr, "owner-1", "", claimAddr)

	client := dialProxyClaimProducer(t, proxyAddr, "fs-claims", "claimproducer-conformance")

	results := claimproducerconf.Run(context.Background(), client)
	if len(results) == 0 {
		t.Fatalf("no claim-producer conformance checks ran")
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s failed through the proxy: %v", r.Name, r.Err)
		}
	}
}

// @concept: host-daemon
// @decision: conformance-suite-per-protocol
func TestProxyPassesHostDaemonConformanceSuite(t *testing.T) {
	proxyAddr, _ := startRealProxyServer(t, "owner-1", "codegen", "./codegen")
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	results := hostdaemonconf.Run(context.Background(), genv1.NewHostDaemonClient(conn), hostdaemonconf.RunOpts{
		Registration: hostdaemonconf.Registration{
			APIKey:          "owner-1",
			DaemonLabel:     "conformance",
			DaemonVersion:   "test",
			CallbackBaseURL: "http://127.0.0.1:1/",
		},
	})
	ran := map[string]bool{}
	for _, r := range results {
		ran[r.Name] = true
		if r.Err != nil {
			t.Errorf("%s failed against the proxy: %v", r.Name, r.Err)
		}
	}
	for _, name := range []string{
		"RegisterAck",
		"HeartbeatAcked",
		"DuplicateRegisterIgnored",
		"UnknownSpawnReapedIgnored",
		"UnknownStreamDispatchIgnored",
		"LocalHttpForwardAnswered",
		"RegisterRefusesAnEmptyApiKey",
		"ConnectRefusesANonRegisterFirstFrame",
		"OneConnectionPerRoutingIdentity",
	} {
		if !ran[name] {
			t.Errorf("the suite stopped before %s; every check must reach the server", name)
		}
	}
}

func TestHostDaemonConformanceSuiteFailsAServerThatNeverAcksARegistration(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterHostDaemonServer(srv, &genv1.UnimplementedHostDaemonServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	results := hostdaemonconf.Run(context.Background(), genv1.NewHostDaemonClient(conn), hostdaemonconf.RunOpts{
		Registration: hostdaemonconf.Registration{APIKey: "owner-1"},
	})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("a server that implements nothing must fail the suite at the handshake, got %+v", results)
	}
}
