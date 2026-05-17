// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-http bundled sensor. Implements the Sensor
// gRPC protocol; per watch, polls a configured URL on a fixed
// interval, applies a match predicate (status code and / or JSONPath
// substring match), and POSTs an observation to rimsky's
// `POST /sensors/{watch_id}/observations` endpoint when the response
// body's content-hash changes.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind / sensor-http.
//
//	@concept: sensor
//
// State persistence is in-memory; multi-replica deployments are not
// supported v1 (a follow-up may add a state-DB-keyed advisory lock per
// watch, mirroring sensor-cron's deferred multi-replica posture).
//
// Watermarking: per-watch high-water-mark is the SHA-256 of the
// last-observed response body. The sensor pushes only when the new
// body hash differs from the prior — operator-visible churn reduction
// for sources that don't carry a monotonic version. Operators wanting
// "push every poll regardless of body" omit `match` and set a single
// status-only `match.status`.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Watch is the in-memory state for one active HTTP watch.
type Watch struct {
	WatchID      string
	InstanceID   string
	URL          string
	PollInterval time.Duration
	MatchStatus  []int  // empty → any 2xx is a match
	MatchJSONKey string // dotted path within response JSON; empty → no JSON match
	MatchJSONVal string // expected value at that path (substring match); empty → presence-only

	LastPollAt time.Time
	LastHash   string // sha256 hex of last response body that matched
}

// SensorService implements genv1.SensorServer for HTTP polling.
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
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		clock:          time.Now,
		logger:         log,
		tickInterval:   time.Second,
	}
}

// Capabilities advertises the http kind.
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{
			{
				Kind: "http",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"url": {"type": "string"},
						"poll_interval": {"type": "string"},
						"match": {
							"type": "object",
							"properties": {
								"status": {"type": "array", "items": {"type": "integer"}},
								"jsonpath": {
									"type": "object",
									"properties": {
										"path": {"type": "string"},
										"value": {"type": "string"}
									}
								}
							}
						}
					},
					"required": ["url"]
				}`),
			},
		},
		Protocols: []string{"sensor"},
	}, nil
}

// StartWatch parses the resolved_config and registers the watch.
// Idempotent on watch_id.
func (s *SensorService) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	if req.GetKind() != "http" {
		return nil, fmt.Errorf("sensor-http does not support kind %q", req.GetKind())
	}
	var cfg struct {
		URL          string `json:"url"`
		PollInterval string `json:"poll_interval"`
		Match        struct {
			Status   []int `json:"status"`
			JSONPath struct {
				Path  string `json:"path"`
				Value string `json:"value"`
			} `json:"jsonpath"`
		} `json:"match"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("resolved_config.url required")
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
		WatchID:      req.GetWatchId(),
		InstanceID:   req.GetInstanceId(),
		URL:          cfg.URL,
		PollInterval: interval,
		MatchStatus:  cfg.Match.Status,
		MatchJSONKey: cfg.Match.JSONPath.Path,
		MatchJSONVal: cfg.Match.JSONPath.Value,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.WatchID]; exists {
		return &genv1.StartWatchResponse{}, nil
	}
	s.watches[w.WatchID] = w
	s.logger.Info("sensor-http.start_watch",
		"watch_id", w.WatchID,
		"instance_id", w.InstanceID,
		"url", w.URL,
		"poll_interval", interval.String())
	return &genv1.StartWatchResponse{}, nil
}

// StopWatch removes the watch. Idempotent.
func (s *SensorService) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetWatchId()]; ok {
		delete(s.watches, req.GetWatchId())
		s.logger.Info("sensor-http.stop_watch", "watch_id", req.GetWatchId())
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
			Kind:       "http",
			StartedAt:  timestamppb.New(s.clock()),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
}

// Tick polls any watch whose `last_poll_at + poll_interval <= now`.
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

// pollOne issues one HTTP GET, evaluates the match predicate, and
// pushes an observation when the body hash differs from the prior.
// Updates the watermark on every poll regardless of match outcome
// (so transient mismatches don't repeatedly fire).
func (s *SensorService) pollOne(ctx context.Context, w *Watch, now time.Time) {
	s.mu.Lock()
	w.LastPollAt = now
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.URL, nil)
	if err != nil {
		s.logger.Warn("sensor-http.poll_build_request_failed",
			"watch_id", w.WatchID, "error", err.Error())
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("sensor-http.poll_dial_failed",
			"watch_id", w.WatchID, "url", w.URL, "error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Warn("sensor-http.poll_read_failed",
			"watch_id", w.WatchID, "error", err.Error())
		return
	}
	if !statusMatch(resp.StatusCode, w.MatchStatus) {
		return
	}
	if !jsonMatch(body, w.MatchJSONKey, w.MatchJSONVal) {
		return
	}
	hash := sha256Hex(body)
	s.mu.Lock()
	cur, ok := s.watches[w.WatchID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if cur.LastHash == hash {
		s.mu.Unlock()
		return
	}
	cur.LastHash = hash
	s.mu.Unlock()

	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"url":         w.URL,
		"status":      resp.StatusCode,
		"body_hash":   hash,
	}
	// Best-effort include a decoded JSON body so substitution can read
	// `{{observation.body.<path>}}`. Non-JSON bodies surface as a string.
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	if err := s.postObservation(ctx, w.WatchID, obs); err != nil {
		s.logger.Warn("sensor-http.observation_post_failed",
			"watch_id", w.WatchID, "error", err.Error())
	}
}

// statusMatch returns true when the response status falls within the
// configured set. Empty set defaults to "any 2xx".
func statusMatch(code int, allowed []int) bool {
	if len(allowed) == 0 {
		return code >= 200 && code < 300
	}
	for _, c := range allowed {
		if c == code {
			return true
		}
	}
	return false
}

// jsonMatch returns true when the body at `path` contains the
// configured substring (or any value when `value` is empty), or when
// no path is configured.
func jsonMatch(body []byte, path, value string) bool {
	if path == "" {
		return true
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return false
	}
	got, ok := walkDottedPath(doc, path)
	if !ok {
		return false
	}
	if value == "" {
		return true
	}
	s, ok := got.(string)
	if !ok {
		// Best-effort: stringify primitives.
		s = fmt.Sprintf("%v", got)
	}
	return strings.Contains(s, value)
}

// walkDottedPath resolves "a.b.c" against a JSON object tree.
func walkDottedPath(doc any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	cur := doc
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// sha256Hex returns the hex-encoded SHA-256 of the body.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// postObservation sends one observation to rimsky.
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
