// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// @concept: conformance
// @concept: host-daemon-proxy

package hostdaemon

import (
	"context"
	"errors"
	"sync"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const frameBuffer = 16

var errStreamClosed = errors.New("host-daemon stream closed before the frame arrived")

type Registration struct {
	APIKey          string
	DaemonLabel     string
	DaemonVersion   string
	RoutingLabel    string
	CallbackBaseURL string
}

func (r Registration) frame() *genv1.ClientFrame {
	return &genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               r.APIKey,
		DaemonLabel:          r.DaemonLabel,
		DaemonVersion:        r.DaemonVersion,
		RoutingLabel:         r.RoutingLabel,
		LocalCallbackBaseUrl: r.CallbackBaseURL,
	}}}
}

type session struct {
	stream     genv1.HostDaemon_ConnectClient
	cancel     context.CancelFunc
	ack        *genv1.RegisterAck
	heartbeats chan *genv1.HostDaemonHeartbeatAck
	forwards   chan *genv1.LocalHttpResponse

	once   sync.Once
	done   chan struct{}
	readMu sync.Mutex
	readIn error
}

func openStream(ctx context.Context, client genv1.HostDaemonClient) (genv1.HostDaemon_ConnectClient, context.CancelFunc, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := client.Connect(streamCtx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return stream, cancel, nil
}

func register(ctx context.Context, client genv1.HostDaemonClient, reg Registration) (*session, error) {
	stream, cancel, err := openStream(ctx, client)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(reg.frame()); err != nil {
		cancel()
		return nil, err
	}
	frame, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, err
	}
	ack := frame.GetRegisterAck()
	if ack == nil {
		cancel()
		return nil, errors.New("the proxy answered Register with a frame that is not a RegisterAck")
	}
	s := &session{
		stream:     stream,
		cancel:     cancel,
		ack:        ack,
		heartbeats: make(chan *genv1.HostDaemonHeartbeatAck, frameBuffer),
		forwards:   make(chan *genv1.LocalHttpResponse, frameBuffer),
		done:       make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

func (s *session) readLoop() {
	for {
		frame, err := s.stream.Recv()
		if err != nil {
			s.readMu.Lock()
			s.readIn = err
			s.readMu.Unlock()
			s.once.Do(func() { close(s.done) })
			return
		}
		switch body := frame.GetBody().(type) {
		case *genv1.ServerFrame_HeartbeatAck:
			s.deliverHeartbeat(body.HeartbeatAck)
		case *genv1.ServerFrame_HttpResponse:
			s.deliverForward(body.HttpResponse)
		}
	}
}

func (s *session) deliverHeartbeat(ack *genv1.HostDaemonHeartbeatAck) {
	select {
	case s.heartbeats <- ack:
	default:
	}
}

func (s *session) deliverForward(resp *genv1.LocalHttpResponse) {
	select {
	case s.forwards <- resp:
	default:
	}
}

func (s *session) send(frame *genv1.ClientFrame) error {
	return s.stream.Send(frame)
}

func (s *session) streamError() error {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	return s.readIn
}

func (s *session) awaitHeartbeat(ctx context.Context) (*genv1.HostDaemonHeartbeatAck, error) {
	select {
	case ack := <-s.heartbeats:
		return ack, nil
	case <-s.done:
		return nil, s.closedErr()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *session) awaitForward(ctx context.Context) (*genv1.LocalHttpResponse, error) {
	select {
	case resp := <-s.forwards:
		return resp, nil
	case <-s.done:
		return nil, s.closedErr()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *session) closedErr() error {
	if err := s.streamError(); err != nil {
		return err
	}
	return errStreamClosed
}

func (s *session) close() {
	s.cancel()
}
