// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostdaemon

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeProxy struct {
	genv1.UnimplementedHostDaemonServer

	addr string

	mu            sync.Mutex
	stream        genv1.HostDaemon_ConnectServer
	register      *genv1.Register
	connected     chan struct{}
	connectedOnce sync.Once
	clientFrame   chan *genv1.ClientFrame
	disconnect    chan struct{}
}

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
		disconnect:  make(chan struct{}),
	}
	srv := grpc.NewServer()
	genv1.RegisterHostDaemonServer(srv, fp)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return fp
}

func (fp *fakeProxy) Connect(stream genv1.HostDaemon_ConnectServer) error {
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

	if err := stream.Send(&genv1.ServerFrame{Body: &genv1.ServerFrame_RegisterAck{RegisterAck: &genv1.RegisterAck{
		ProxyVersion: "test",
	}}}); err != nil {
		return err
	}
	fp.connectedOnce.Do(func() { close(fp.connected) })

	recvCh := make(chan *genv1.ClientFrame)
	errCh := make(chan error, 1)
	go func() {
		for {
			frame, recvErr := stream.Recv()
			if recvErr != nil {
				errCh <- recvErr
				return
			}
			recvCh <- frame
		}
	}()

	for {
		select {
		case frame := <-recvCh:
			if _, isHB := frame.GetBody().(*genv1.ClientFrame_Heartbeat); isHB {
				continue
			}
			select {
			case fp.clientFrame <- frame:
			case <-fp.disconnect:
				return errors.New("forced disconnect")
			}
		case recvErr := <-errCh:
			return recvErr
		case <-fp.disconnect:
			return errors.New("forced disconnect")
		}
	}
}

func (fp *fakeProxy) forceDisconnect() {
	close(fp.disconnect)
}

func (fp *fakeProxy) waitConnected(t *testing.T) *genv1.Register {
	t.Helper()
	<-fp.connected
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.register
}

func (fp *fakeProxy) sendToDaemon(t *testing.T, frame *genv1.ServerFrame) {
	t.Helper()
	fp.mu.Lock()
	stream := fp.stream
	fp.mu.Unlock()
	if stream == nil {
		t.Fatal("no daemon stream captured")
	}
	if err := stream.Send(frame); err != nil {
		t.Fatalf("send to daemon: %v", err)
	}
}

func (fp *fakeProxy) nextClientFrame(t *testing.T) *genv1.ClientFrame {
	t.Helper()
	return <-fp.clientFrame
}

func runDaemonInBackground(t *testing.T, cfg Config) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel
}

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
