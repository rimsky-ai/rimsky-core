// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-object-store bundled sensor. Implements the
// Sensor gRPC protocol; per watch, polls an object-store bucket+prefix
// on a fixed interval and emits one observation per new object (or new
// object version, per `watermark_field`).
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind / sensor-object-store.
//
//	@concept: sensor
//
// Backends: the sensor is structured around a narrow `ObjectLister`
// interface (List(prefix) -> []ObjectMeta) so a single poll loop drives
// every backend. The reference implementation ships an in-memory lister
// for tests; S3 / GCS / Azure listers are wired in production builds
// via the `backend` config string. The in-memory backend is exposed
// via SetBackend for fakes so callers can drive scenario tests without
// LocalStack.
//
// Watermarking: per-watch high-watermark is the maximum value seen for
// the configured `watermark_field` (one of `name`, `last_modified`).
// New observations are objects whose watermark value strictly exceeds
// the prior watermark. Idempotency: re-listing the same set without
// any new object produces zero observations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// ObjectMeta is the per-object snapshot the lister returns. Inert in
// the sensor — we only project (name, last_modified, size, etag) into
// the observation body.
type ObjectMeta struct {
	Name         string
	LastModified time.Time
	Size         int64
	ETag         string
}

// ObjectLister is the narrow per-backend interface. Production wires
// S3 / GCS / Azure implementations; tests inject a fake via SetBackend.
type ObjectLister interface {
	List(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
}

// Watch is the in-memory state for one active object-store watch.
type Watch struct {
	WatchID        string
	InstanceID     string
	Backend        string // s3 | gcs | azure | memory
	Bucket         string
	Prefix         string
	PollInterval   time.Duration
	WatermarkField string // "name" | "last_modified"

	LastPollAt    time.Time
	WatermarkName string    // when WatermarkField == "name"
	WatermarkTime time.Time // when WatermarkField == "last_modified"
}

// SensorService implements genv1.SensorServer for object-store polling.
//
// `lister` is keyed by backend name ("s3", "gcs", "azure", "memory").
// Production code registers backends at startup; tests inject the
// "memory" lister via SetBackend.
type SensorService struct {
	genv1.UnimplementedSensorServer
	mu             sync.Mutex
	watches        map[string]*Watch
	listers        map[string]ObjectLister
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewSensorService constructs the service with an empty lister registry.
// Callers register backends via SetBackend.
func NewSensorService(rimskyEndpoint string, log logger) *SensorService {
	return &SensorService{
		watches:        make(map[string]*Watch),
		listers:        make(map[string]ObjectLister),
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		clock:          time.Now,
		logger:         log,
		tickInterval:   time.Second,
	}
}

// SetBackend registers an ObjectLister under the given backend name.
// Used by tests (memory fake) and by main() at startup for production
// backends.
func (s *SensorService) SetBackend(name string, l ObjectLister) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listers[name] = l
}

// Capabilities advertises the object-store kind.
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{
			{
				Kind: "object-store",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"backend": {"type": "string", "enum": ["s3", "gcs", "azure", "memory"]},
						"bucket": {"type": "string"},
						"prefix": {"type": "string"},
						"poll_interval": {"type": "string"},
						"watermark_field": {"type": "string", "enum": ["name", "last_modified"]}
					},
					"required": ["backend", "bucket"]
				}`),
			},
		},
		Protocols: []string{"sensor"},
	}, nil
}

// StartWatch parses resolved_config and registers the watch.
func (s *SensorService) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	if req.GetKind() != "object-store" {
		return nil, fmt.Errorf("sensor-object-store does not support kind %q", req.GetKind())
	}
	var cfg struct {
		Backend        string `json:"backend"`
		Bucket         string `json:"bucket"`
		Prefix         string `json:"prefix"`
		PollInterval   string `json:"poll_interval"`
		WatermarkField string `json:"watermark_field"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.Backend == "" {
		return nil, fmt.Errorf("resolved_config.backend required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("resolved_config.bucket required")
	}
	switch cfg.Backend {
	case "s3", "gcs", "azure", "memory":
	default:
		return nil, fmt.Errorf("resolved_config.backend must be s3|gcs|azure|memory (got %q)", cfg.Backend)
	}
	if cfg.WatermarkField == "" {
		cfg.WatermarkField = "name"
	}
	if cfg.WatermarkField != "name" && cfg.WatermarkField != "last_modified" {
		return nil, fmt.Errorf("resolved_config.watermark_field must be name|last_modified (got %q)", cfg.WatermarkField)
	}
	interval := 30 * time.Second
	if cfg.PollInterval != "" {
		d, err := time.ParseDuration(cfg.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid poll_interval %q: %w", cfg.PollInterval, err)
		}
		interval = d
	}
	w := &Watch{
		WatchID:        req.GetWatchId(),
		InstanceID:     req.GetInstanceId(),
		Backend:        cfg.Backend,
		Bucket:         cfg.Bucket,
		Prefix:         cfg.Prefix,
		PollInterval:   interval,
		WatermarkField: cfg.WatermarkField,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.WatchID]; exists {
		return &genv1.StartWatchResponse{}, nil
	}
	s.watches[w.WatchID] = w
	s.logger.Info("sensor-object-store.start_watch",
		"watch_id", w.WatchID,
		"instance_id", w.InstanceID,
		"backend", cfg.Backend,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix,
		"poll_interval", interval.String(),
		"watermark_field", cfg.WatermarkField)
	return &genv1.StartWatchResponse{}, nil
}

// StopWatch removes the watch. Idempotent.
func (s *SensorService) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetWatchId()]; ok {
		delete(s.watches, req.GetWatchId())
		s.logger.Info("sensor-object-store.stop_watch", "watch_id", req.GetWatchId())
	}
	return &genv1.StopWatchResponse{}, nil
}

// ListWatches enumerates active watches.
func (s *SensorService) ListWatches(_ context.Context, _ *emptypb.Empty) (*genv1.ListWatchesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.WatchDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.WatchDescriptor{
			WatchId:    w.WatchID,
			InstanceId: w.InstanceID,
			Kind:       "object-store",
			StartedAt:  timestamppb.New(s.clock()),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
}

// Tick polls due watches. One observation per new object.
func (s *SensorService) Tick(ctx context.Context) {
	now := s.clock()
	s.mu.Lock()
	due := make([]*Watch, 0)
	for _, w := range s.watches {
		if w.LastPollAt.IsZero() || !now.Before(w.LastPollAt.Add(w.PollInterval)) {
			due = append(due, w)
		}
	}
	s.mu.Unlock()
	for _, w := range due {
		s.pollOne(ctx, w, now)
	}
}

// pollOne lists the bucket+prefix, filters by the watermark, and pushes
// one observation per new object.
func (s *SensorService) pollOne(ctx context.Context, w *Watch, now time.Time) {
	s.mu.Lock()
	w.LastPollAt = now
	lister, ok := s.listers[w.Backend]
	s.mu.Unlock()
	if !ok {
		s.logger.Warn("sensor-object-store.no_backend",
			"watch_id", w.WatchID, "backend", w.Backend)
		return
	}
	objs, err := lister.List(ctx, w.Bucket, w.Prefix)
	if err != nil {
		s.logger.Warn("sensor-object-store.list_failed",
			"watch_id", w.WatchID, "bucket", w.Bucket, "prefix", w.Prefix, "error", err.Error())
		return
	}

	// Sort by watermark field ascending so we emit observations in order
	// AND advance the watermark deterministically.
	sort.Slice(objs, func(i, j int) bool {
		switch w.WatermarkField {
		case "last_modified":
			return objs[i].LastModified.Before(objs[j].LastModified)
		default:
			return objs[i].Name < objs[j].Name
		}
	})

	for _, o := range objs {
		s.mu.Lock()
		cur, exists := s.watches[w.WatchID]
		if !exists {
			s.mu.Unlock()
			return
		}
		isNew := false
		switch cur.WatermarkField {
		case "last_modified":
			isNew = o.LastModified.After(cur.WatermarkTime)
		default:
			isNew = o.Name > cur.WatermarkName
		}
		if !isNew {
			s.mu.Unlock()
			continue
		}
		// Advance watermark BEFORE post so a post failure does not
		// re-emit. The plan's pre-resolved decision favors at-most-once.
		switch cur.WatermarkField {
		case "last_modified":
			cur.WatermarkTime = o.LastModified
		default:
			cur.WatermarkName = o.Name
		}
		s.mu.Unlock()

		obs := map[string]any{
			"observed_at":   now.UTC().Format(time.RFC3339),
			"backend":       w.Backend,
			"bucket":        w.Bucket,
			"prefix":        w.Prefix,
			"object_name":   o.Name,
			"size":          o.Size,
			"etag":          o.ETag,
			"last_modified": o.LastModified.UTC().Format(time.RFC3339),
		}
		if err := s.postObservation(ctx, w.WatchID, obs); err != nil {
			s.logger.Warn("sensor-object-store.observation_post_failed",
				"watch_id", w.WatchID, "object_name", o.Name, "error", err.Error())
		}
	}
}

// postObservation sends one observation to rimsky.
func (s *SensorService) postObservation(ctx context.Context, watchID string, body map[string]any) error {
	raw, _ := json.Marshal(body)
	url := strings.TrimRight(s.rimskyEndpoint, "/") + "/sensors/" + watchID + "/observations"
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
