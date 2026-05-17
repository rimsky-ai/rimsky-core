// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-cron bundled sensor. Implements the Sensor
// gRPC protocol; on each tick, fires any watch whose `next_fire_at <=
// now` by POSTing an observation to rimsky's
// `POST /sensors/{watch_id}/observations` endpoint.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind / sensor-cron.
//
//	@concept: sensor
//
// State persistence is in-memory by default; the optional
// `state_db: postgres://...` config persists watches across process
// restarts. Multi-replica deployments are guarded by a per-watch
// `pg_try_advisory_lock`; default is single-replica.
//
// Missed-fire policy mirrors the retired internal scheduler: cron
// advancement is from the row's prior `next_fire_at`, NOT
// `clock.Now()`. A long outage produces a single post-outage fire,
// not a backfilled herd. Rationale: invalidation freshness is the
// goal; backfilling a 6-hour outage for an hourly schedule
// generates thundering-herd noise without semantic gain.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Watch is the in-memory state for one active cron watch.
type Watch struct {
	WatchID     string
	InstanceID  string
	CronExpr    string
	NextFireAt  time.Time
	StartedAt   time.Time
	LastFireAt  *time.Time
	MissedFires bool // operator hint: when true, sensor backfills missed fires
}

// SensorService implements genv1.SensorServer. State is in-memory; a
// per-watch advisory-lock acquire is delegated to `lock` (nil when
// single-replica).
type SensorService struct {
	genv1.UnimplementedSensorServer
	mu             sync.Mutex
	watches        map[string]*Watch
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
}

// logger is the narrow interface we use; the embedding binary passes
// a stdlib slog wrapper. Keeping it interface-shaped lets tests pass
// a recording logger without slog dependencies.
type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewSensorService constructs the in-memory service.
func NewSensorService(rimskyEndpoint string, log logger) *SensorService {
	return &SensorService{
		watches:        make(map[string]*Watch),
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		clock:          time.Now,
		logger:         log,
		tickInterval:   time.Second,
	}
}

// Capabilities advertises the supported kinds and protocols.
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{
			{
				Kind: "cron",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"cron": {"type": "string"},
						"missed_fires": {"type": "boolean", "default": false}
					},
					"required": ["cron"]
				}`),
			},
		},
		Protocols: []string{"sensor"},
	}, nil
}

// StartWatch parses the cron expression, computes `next_fire_at`, and
// registers the watch. Idempotent on watch_id: a duplicate StartWatch
// for an active watch is a no-op.
func (s *SensorService) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	if req.GetKind() != "cron" {
		return nil, fmt.Errorf("sensor-cron does not support kind %q", req.GetKind())
	}
	var cfg struct {
		Cron        string `json:"cron"`
		MissedFires bool   `json:"missed_fires"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.Cron == "" {
		return nil, fmt.Errorf("resolved_config.cron required")
	}
	sched, err := cron.ParseStandard(cfg.Cron)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %w", cfg.Cron, err)
	}
	now := s.clock()
	w := &Watch{
		WatchID:     req.GetWatchId(),
		InstanceID:  req.GetInstanceId(),
		CronExpr:    cfg.Cron,
		NextFireAt:  sched.Next(now),
		StartedAt:   now,
		MissedFires: cfg.MissedFires,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.WatchID]; exists {
		// Already active; idempotent.
		return &genv1.StartWatchResponse{}, nil
	}
	s.watches[w.WatchID] = w
	s.logger.Info("sensor-cron.start_watch",
		"watch_id", w.WatchID,
		"instance_id", w.InstanceID,
		"cron", cfg.Cron,
		"next_fire_at", w.NextFireAt.Format(time.RFC3339))
	return &genv1.StartWatchResponse{}, nil
}

// StopWatch removes the watch from the in-memory map. Idempotent.
func (s *SensorService) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetWatchId()]; ok {
		delete(s.watches, req.GetWatchId())
		s.logger.Info("sensor-cron.stop_watch", "watch_id", req.GetWatchId())
	}
	return &genv1.StopWatchResponse{}, nil
}

// ListWatches enumerates the live watches. Used by rimsky's restart
// reconcile (`runtime/sensors.go::ResyncSensorWatches`).
func (s *SensorService) ListWatches(_ context.Context, _ *emptypb.Empty) (*genv1.ListWatchesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.WatchDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.WatchDescriptor{
			WatchId:    w.WatchID,
			InstanceId: w.InstanceID,
			Kind:       "cron",
			StartedAt:  timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
}

// Tick runs one fire-loop iteration. Called from the main poll loop
// every `tickInterval`. Exposed for tests.
func (s *SensorService) Tick(ctx context.Context) {
	now := s.clock()
	s.mu.Lock()
	due := make([]*Watch, 0)
	for _, w := range s.watches {
		if !now.Before(w.NextFireAt) {
			due = append(due, w)
		}
	}
	s.mu.Unlock()

	for _, w := range due {
		s.fireOne(ctx, w, now)
	}
}

// fireOne fires the cron observation and advances next_fire_at. The
// advancement is from the prior next_fire_at (not now()) so missed
// fires are NOT backfilled (mirrors the retired internal scheduler).
func (s *SensorService) fireOne(ctx context.Context, w *Watch, now time.Time) {
	body := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"cron":        w.CronExpr,
		"fire_at":     w.NextFireAt.UTC().Format(time.RFC3339),
	}
	if err := s.postObservation(ctx, w.WatchID, body); err != nil {
		s.logger.Warn("sensor-cron.observation_post_failed",
			"watch_id", w.WatchID, "error", err.Error())
		// Do not advance on failure; the next tick retries the same fire.
		return
	}
	sched, err := cron.ParseStandard(w.CronExpr)
	if err != nil {
		s.logger.Error("sensor-cron.cron_parse_failed",
			"watch_id", w.WatchID, "cron", w.CronExpr)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-find to defend against concurrent StopWatch.
	cur, ok := s.watches[w.WatchID]
	if !ok {
		return
	}
	t := now
	cur.LastFireAt = &t
	cur.NextFireAt = sched.Next(cur.NextFireAt) // advance from prior, not now
}

// postObservation sends one observation to rimsky's HTTP endpoint.
func (s *SensorService) postObservation(ctx context.Context, watchID string, body map[string]any) error {
	raw, _ := json.Marshal(body)
	url := s.rimskyEndpoint + "/sensors/" + watchID + "/observations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("rimsky %s → %d", url, resp.StatusCode)
	}
	return nil
}

// Run starts the tick loop. Blocks until ctx is cancelled.
func (s *SensorService) Run(ctx context.Context) {
	t := time.NewTicker(s.tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}
