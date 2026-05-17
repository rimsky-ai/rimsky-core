// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N5 scenario — lifecycle_start_stop.
//
// The Sensor service-protocol's lifecycle: StartWatch creates an
// in-memory watch, ListWatches enumerates active watches, StopWatch
// removes one. Sensors are required to be idempotent on retries.
// The scenario pins the contract using a minimal in-process sensor.
package sensor

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// fixtureSensor is a minimal Sensor impl used to exercise the
// lifecycle contract. Mirrors sensors/sensor-cron's shape at a
// helper level (cron parsing + fire-loop are out of scope).
type fixtureSensor struct {
	genv1.UnimplementedSensorServer
	mu      sync.Mutex
	watches map[string]watch
}

type watch struct {
	WatchID    string
	InstanceID string
	Kind       string
	StartedAt  time.Time
}

func newFixtureSensor() *fixtureSensor {
	return &fixtureSensor{watches: make(map[string]watch)}
}

func (s *fixtureSensor) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{{Kind: "cron"}, {Kind: "http"}},
		Protocols:      []string{"sensor"},
	}, nil
}

func (s *fixtureSensor) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetWatchId()]; ok {
		return &genv1.StartWatchResponse{}, nil
	}
	s.watches[req.GetWatchId()] = watch{
		WatchID: req.GetWatchId(), InstanceID: req.GetInstanceId(),
		Kind: req.GetKind(), StartedAt: time.Now(),
	}
	return &genv1.StartWatchResponse{}, nil
}

func (s *fixtureSensor) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.watches, req.GetWatchId())
	return &genv1.StopWatchResponse{}, nil
}

func (s *fixtureSensor) ListWatches(_ context.Context, _ *emptypb.Empty) (*genv1.ListWatchesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.WatchDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.WatchDescriptor{
			WatchId: w.WatchID, InstanceId: w.InstanceID,
			Kind: w.Kind, StartedAt: timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
}

func TestLifecycleStartStop_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newFixtureSensor()
	ctx := context.Background()
	if _, err := s.StartWatch(ctx, &genv1.StartWatchRequest{
		WatchId: "w1", InstanceId: "i1", Kind: "cron",
	}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	resp, err := s.ListWatches(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(resp.GetWatches()) != 1 {
		t.Errorf("ListWatches: expected 1 watch, got %d", len(resp.GetWatches()))
	}
	if _, err := s.StopWatch(ctx, &genv1.StopWatchRequest{WatchId: "w1"}); err != nil {
		t.Fatalf("StopWatch: %v", err)
	}
	resp, err = s.ListWatches(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(resp.GetWatches()) != 0 {
		t.Errorf("ListWatches after stop: expected 0, got %d", len(resp.GetWatches()))
	}
}

func TestLifecycleStartStop_Idempotent(t *testing.T) {
	t.Parallel()
	s := newFixtureSensor()
	ctx := context.Background()
	if _, err := s.StartWatch(ctx, &genv1.StartWatchRequest{WatchId: "w1", Kind: "cron"}); err != nil {
		t.Fatalf("StartWatch #1: %v", err)
	}
	if _, err := s.StartWatch(ctx, &genv1.StartWatchRequest{WatchId: "w1", Kind: "cron"}); err != nil {
		t.Errorf("StartWatch #2 (idempotent): %v", err)
	}
	if _, err := s.StopWatch(ctx, &genv1.StopWatchRequest{WatchId: "w1"}); err != nil {
		t.Fatalf("StopWatch: %v", err)
	}
	if _, err := s.StopWatch(ctx, &genv1.StopWatchRequest{WatchId: "w1"}); err != nil {
		t.Errorf("StopWatch idempotent: %v", err)
	}
}
