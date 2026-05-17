// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main_test.go drives the Sensor conformance suite against an
// in-process Sensor server that mimics the bundled sensor-cron
// shape: a single supported kind "cron", a poll-loop that fires
// on the per-watch interval. The fixture lives inside the cmd test
// package because cross-cmd-main imports aren't possible in Go.
//
// Wire conformance against the bundled binary is exercised by the
// smoke fixture (O1).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// TestSensorConformance_FixtureCron drives the suite against an
// in-process fixture sensor whose only advertised kind is "cron".
// The observation-push check is wired up via the in-process
// receiver.
func TestSensorConformance_FixtureCron(t *testing.T) {
	receiver := NewObservationReceiver()
	receiverEndpoint, stopReceiver := startReceiver(t, receiver)
	t.Cleanup(stopReceiver)

	fixture := newFixtureSensor(receiverEndpoint)
	sensorEndpoint, stopSensor := startSensorServer(t, fixture)
	t.Cleanup(stopSensor)

	conn, err := grpc.NewClient(sensorEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewSensorClient(conn)

	opts := RunOpts{
		Kind:                "cron",
		ResolvedConfig:      []byte(`{"cron":"* * * * *"}`),
		ObservationReceiver: receiver,
		ObservationTimeout:  3 * time.Second,
		WatchID:             "self-test-watch",
		InstanceID:          "self-test-instance",
	}
	results := RunSensorConformance(context.Background(), client, opts)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	wantNames := []string{
		"Capabilities", "StartWatch", "ListWatches", "StartWatchIdempotent",
		"ObservationPush", "StopWatch", "StopWatchIdempotent",
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Name] = true
	}
	for _, name := range wantNames {
		if !seen[name] {
			t.Errorf("expected check %q to run, did not see it", name)
		}
	}
}

// startReceiver spawns the observation receiver HTTP server.
func startReceiver(t *testing.T, r *ObservationReceiver) (endpoint string, teardown func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sensors/", func(w http.ResponseWriter, req *http.Request) {
		// Path: /sensors/{watch_id}/observations
		// Best-effort parse: split the path and pull the {watch_id}.
		u, _ := url.Parse(req.URL.Path)
		parts := splitNonEmpty(u.Path, '/')
		var watchID string
		if len(parts) >= 2 && parts[0] == "sensors" {
			watchID = parts[1]
		}
		// Drain the body so the sender's roundtripper finishes.
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		if watchID != "" {
			r.NoteObservation(watchID)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("receiver listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(lis) }()
	endpoint = "http://" + lis.Addr().String()
	return endpoint, func() {
		_ = srv.Close()
	}
}

func splitNonEmpty(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// fixtureSensor is a minimal Sensor impl. Each watch fires an
// observation every 200ms (cron-like cadence; the conformance
// observation-push check tolerates up to 3s).
type fixtureSensor struct {
	genv1.UnimplementedSensorServer
	mu             sync.Mutex
	rimskyEndpoint string
	watches        map[string]*fixtureWatch
	httpClient     *http.Client
}

type fixtureWatch struct {
	watchID    string
	instanceID string
	kind       string
	startedAt  time.Time
	cancel     context.CancelFunc
}

func newFixtureSensor(rimskyEndpoint string) *fixtureSensor {
	return &fixtureSensor{
		rimskyEndpoint: rimskyEndpoint,
		watches:        map[string]*fixtureWatch{},
		httpClient:     &http.Client{Timeout: 2 * time.Second},
	}
}

func (s *fixtureSensor) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{{Kind: "cron"}},
		Protocols:      []string{"sensor"},
	}, nil
}

func (s *fixtureSensor) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	if req.GetKind() != "cron" {
		return nil, fmt.Errorf("fixture sensor: unsupported kind %q", req.GetKind())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetWatchId()]; ok {
		return &genv1.StartWatchResponse{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &fixtureWatch{
		watchID:    req.GetWatchId(),
		instanceID: req.GetInstanceId(),
		kind:       req.GetKind(),
		startedAt:  time.Now(),
		cancel:     cancel,
	}
	s.watches[w.watchID] = w
	go s.tick(ctx, w)
	return &genv1.StartWatchResponse{}, nil
}

func (s *fixtureSensor) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.watches[req.GetWatchId()]; ok {
		w.cancel()
		delete(s.watches, req.GetWatchId())
	}
	return &genv1.StopWatchResponse{}, nil
}

func (s *fixtureSensor) ListWatches(_ context.Context, _ *emptypb.Empty) (*genv1.ListWatchesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.WatchDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.WatchDescriptor{
			WatchId:    w.watchID,
			InstanceId: w.instanceID,
			Kind:       w.kind,
			StartedAt:  timestamppb.New(w.startedAt),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
}

func (s *fixtureSensor) tick(ctx context.Context, w *fixtureWatch) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			body := map[string]any{"observed_at": time.Now().UTC().Format(time.RFC3339)}
			raw, _ := json.Marshal(body)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				s.rimskyEndpoint+"/sensors/"+w.watchID+"/observations", bytes.NewReader(raw))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := s.httpClient.Do(req)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
}

// startSensorServer spawns the gRPC server.
func startSensorServer(t *testing.T, srv *fixtureSensor) (endpoint string, teardown func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sensor listen: %v", err)
	}
	g := grpc.NewServer()
	genv1.RegisterSensorServer(g, srv)
	done := make(chan struct{})
	go func() {
		_ = g.Serve(lis)
		close(done)
	}()
	return lis.Addr().String(), func() {
		g.GracefulStop()
		<-done
	}
}
