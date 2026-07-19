// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type proxyTestServer struct {
	state    *proxyState
	hostConn *grpc.ClientConn
	supConn  *grpc.ClientConn
}

func newProxyTestServer(t *testing.T, fetch instanceFetcher) *proxyTestServer {
	t.Helper()
	state := newProxyState()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()

	genv1.RegisterHostAgentServer(srv, newAgentServer(state, presentedKeyIsIdentity))

	cfg := Config{SpawnReadyTimeout: 2 * time.Second, ReapTimeout: 2 * time.Second}
	if fetch == nil {
		fetch = func(context.Context, string) (*instanceCacheEntry, bool, error) { return nil, false, nil }
	}
	genv1.RegisterExecutorServer(srv, &executorHandler{state: state, fetch: fetch, spawnTimeout: cfg.SpawnReadyTimeout})
	genv1.RegisterClaimProducerServer(srv, &claimProducerHandler{state: state, fetch: fetch, spawnTimeout: cfg.SpawnReadyTimeout, callTimeout: 2 * time.Second})
	genv1.RegisterLifecycleSubscriberServer(srv, newLifecycleHandler(state, cfg))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dial := func() *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	return &proxyTestServer{state: state, hostConn: dial(), supConn: dial()}
}

func presentedKeyIsIdentity(_ context.Context, presentedAPIKey string) (string, error) {
	return presentedAPIKey, nil
}

type dispatchHandler func(protocol string, payload []byte) [][]byte

type fakeAgent struct {
	stream genv1.HostAgent_ConnectClient

	mu              sync.Mutex
	spawnFail       bool
	spawnDelay      time.Duration
	dropOnFirst     bool
	stallData       bool
	handler         dispatchHandler
	reaped          chan string
	canceled        chan string
	spawnObserver   func(*genv1.Spawn)
	dispatchCount   int
	crashOnDispatch int
}

func connectFakeAgent(t *testing.T, ts *proxyTestServer, apiKey, localBase string, handler dispatchHandler) *fakeAgent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream, err := genv1.NewHostAgentClient(ts.hostConn).Connect(ctx)
	if err != nil {
		t.Fatalf("connect agent: %v", err)
	}
	if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               apiKey,
		LocalCallbackBaseUrl: localBase,
	}}}); err != nil {
		t.Fatalf("send register: %v", err)
	}
	frame, err := stream.Recv()
	if err != nil || frame.GetRegisterAck() == nil {
		t.Fatalf("register ack: err=%v frame=%T", err, frame.GetBody())
	}

	fa := &fakeAgent{stream: stream, handler: handler, reaped: make(chan string, 8), canceled: make(chan string, 8)}
	go fa.loop(t)

	waitFor(t, func() bool { _, ok := ts.state.lookupAgent(apiKey); return ok })
	return fa
}

func (fa *fakeAgent) setSpawnFail(v bool)           { fa.mu.Lock(); fa.spawnFail = v; fa.mu.Unlock() }
func (fa *fakeAgent) setSpawnDelay(d time.Duration) { fa.mu.Lock(); fa.spawnDelay = d; fa.mu.Unlock() }
func (fa *fakeAgent) setDropOnFirst(v bool)         { fa.mu.Lock(); fa.dropOnFirst = v; fa.mu.Unlock() }
func (fa *fakeAgent) setStallData(v bool)           { fa.mu.Lock(); fa.stallData = v; fa.mu.Unlock() }

func (fa *fakeAgent) setSpawnObserver(f func(*genv1.Spawn)) {
	fa.mu.Lock()
	fa.spawnObserver = f
	fa.mu.Unlock()
}

func (fa *fakeAgent) setCrashOnDispatch(n int) {
	fa.mu.Lock()
	fa.crashOnDispatch = n
	fa.mu.Unlock()
}

func (fa *fakeAgent) loop(t *testing.T) {
	for {
		frame, err := fa.stream.Recv()
		if err != nil {
			return
		}
		switch body := frame.GetBody().(type) {
		case *genv1.ServerFrame_Spawn:
			fa.handleSpawn(body.Spawn)
		case *genv1.ServerFrame_DispatchFrame:
			if fa.handleDispatch(body.DispatchFrame) {
				return
			}
		case *genv1.ServerFrame_Reap:
			fa.mu.Lock()
			_ = fa.stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Reaped{Reaped: &genv1.Reaped{
				SpawnId: body.Reap.GetSpawnId(),
				Clean:   true,
			}}})
			fa.mu.Unlock()
			select {
			case fa.reaped <- body.Reap.GetSpawnId():
			default:
			}
		}
	}
}

func (fa *fakeAgent) handleSpawn(sp *genv1.Spawn) {
	fa.mu.Lock()
	fail := fa.spawnFail
	delay := fa.spawnDelay
	observer := fa.spawnObserver
	fa.mu.Unlock()
	if observer != nil {
		observer(sp)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	ack := &genv1.SpawnAck{SpawnId: sp.GetSpawnId(), Status: genv1.SpawnAck_SPAWN_STATUS_READY}
	if fail {
		ack.Status = genv1.SpawnAck_SPAWN_STATUS_FAILED
		ack.Error = &genv1.HostAgentError{Class: "exec_failed", Message: "binary not found"}
	}
	_ = fa.stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: ack}})
}

func (fa *fakeAgent) handleDispatch(df *genv1.DispatchFrame) bool {
	if df.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		select {
		case fa.canceled <- df.GetStreamId():
		default:
		}
		return false
	}
	fa.mu.Lock()
	drop := fa.dropOnFirst
	stall := fa.stallData
	handler := fa.handler
	fa.dispatchCount++
	crash := fa.crashOnDispatch != 0 && fa.dispatchCount == fa.crashOnDispatch
	fa.mu.Unlock()
	if drop {
		_ = fa.stream.CloseSend()
		return true
	}
	if crash {
		_ = fa.stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
			SpawnId:  df.GetSpawnId(),
			Protocol: df.GetProtocol(),
			StreamId: df.GetStreamId(),
			Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
		}}})
		return false
	}
	if stall {
		return false
	}
	if handler == nil {
		return false
	}
	for _, payload := range handler(df.GetProtocol(), df.GetPayload()) {
		_ = fa.stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
			SpawnId:  df.GetSpawnId(),
			Protocol: df.GetProtocol(),
			Payload:  payload,
			StreamId: df.GetStreamId(),
			Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		}}})
	}
	return false
}

func callCtx(name string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), serviceNameHeader, name)
}

func staticFetcher(instanceID string, entry *instanceCacheEntry) instanceFetcher {
	return func(_ context.Context, id string) (*instanceCacheEntry, bool, error) {
		if id == instanceID {
			return entry, true, nil
		}
		return nil, false, nil
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within deadline")
}
