// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// harness_test.go — in-process test scaffolding for the host-agent. Stands up
// a fakeProxy: a real HostAgent.Connect gRPC server on 127.0.0.1:0 that
// captures the connected agent's stream so a test can push Spawn / Dispatch /
// Reap ServerFrames and read back the agent's ClientFrame replies. The
// host-agent under test is the production hostagent.connectOnce/agent path
// dialing this fake. Spawn/dispatch tests build the testdata/stubchild
// fixture once (buildStubChild) and exec it for real.

package hostagent

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// fakeProxy is a HostAgent server that captures one connected agent's stream.
type fakeProxy struct {
	genv1.UnimplementedHostAgentServer

	addr string

	mu          sync.Mutex
	stream      genv1.HostAgent_ConnectServer
	register    *genv1.Register
	connected   chan struct{}
	clientFrame chan *genv1.ClientFrame // every non-Register frame the agent sent
}

// startFakeProxy binds a real gRPC server on 127.0.0.1:0 and returns the proxy
// plus its dial address. The server is stopped on test cleanup.
func startFakeProxy(t *testing.T) *fakeProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fp := &fakeProxy{
		addr:        lis.Addr().String(),
		connected:   make(chan struct{}),
		clientFrame: make(chan *genv1.ClientFrame, 64),
	}
	srv := grpc.NewServer()
	genv1.RegisterHostAgentServer(srv, fp)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return fp
}

// Connect captures the agent stream, acks Register, and relays every
// subsequent ClientFrame to clientFrame for assertions.
func (fp *fakeProxy) Connect(stream genv1.HostAgent_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return context.Canceled
	}

	fp.mu.Lock()
	fp.stream = stream
	fp.register = reg
	fp.mu.Unlock()
	close(fp.connected)

	if err := stream.Send(&genv1.ServerFrame{Body: &genv1.ServerFrame_RegisterAck{RegisterAck: &genv1.RegisterAck{
		ProxyVersion: "test",
	}}}); err != nil {
		return err
	}

	for {
		frame, recvErr := stream.Recv()
		if recvErr != nil {
			return recvErr
		}
		// Skip heartbeats so assertions aren't drowned by liveness frames.
		if _, isHB := frame.GetBody().(*genv1.ClientFrame_Heartbeat); isHB {
			continue
		}
		select {
		case fp.clientFrame <- frame:
		default:
		}
	}
}

// waitConnected blocks until the agent has registered (or the test deadline).
func (fp *fakeProxy) waitConnected(t *testing.T) *genv1.Register {
	t.Helper()
	select {
	case <-fp.connected:
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.register
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not connect within deadline")
		return nil
	}
}

// sendToAgent pushes a ServerFrame down the captured stream.
func (fp *fakeProxy) sendToAgent(t *testing.T, frame *genv1.ServerFrame) {
	t.Helper()
	fp.mu.Lock()
	stream := fp.stream
	fp.mu.Unlock()
	if stream == nil {
		t.Fatal("no agent stream captured")
	}
	if err := stream.Send(frame); err != nil {
		t.Fatalf("send to agent: %v", err)
	}
}

// nextClientFrame reads the next non-heartbeat ClientFrame the agent sent.
func (fp *fakeProxy) nextClientFrame(t *testing.T) *genv1.ClientFrame {
	t.Helper()
	select {
	case f := <-fp.clientFrame:
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("no client frame from agent within deadline")
		return nil
	}
}

// runAgentInBackground starts hostagent.Run with the fake proxy as RIMSKY_URL
// and returns a cancel func. Run exits when the returned context is cancelled.
func runAgentInBackground(t *testing.T, cfg Config) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("hostagent.Run did not exit after cancel")
		}
	})
	return cancel
}

// buildStubChild compiles testdata/stubchild into a temp dir once per test and
// returns the binary path. The binary honors RIMSKY_AGENT_PORT.
func buildStubChild(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "stubchild")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/stubchild")
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stubchild: %v\n%s", err, out)
	}
	return bin
}
