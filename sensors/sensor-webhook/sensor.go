// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-webhook bundled sensor. Implements the Sensor
// gRPC protocol; runs an HTTP server on a configured port and, per
// watch, registers a path under `path_prefix`. Inbound POSTs to a
// registered route → push observation to rimsky.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors as a service kind / sensor-webhook.
//
//	@concept: sensor
//
// Idempotency: optional `idempotency_header` config — when the inbound
// POST carries that header, the sensor deduplicates against the prior
// value per watch (last-seen wins). Useful for webhook providers that
// retry — the provider's idempotency key suppresses duplicate emissions.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Watch is the in-memory state for one active webhook watch.
type Watch struct {
	WatchID           string
	InstanceID        string
	PathPrefix        string
	IdempotencyHeader string

	mu              sync.Mutex
	StartedAt       time.Time
	LastIdempotency string // most-recent idempotency key seen
}

// SensorService implements genv1.SensorServer for HTTP webhook
// reception. The chi router is wired once at construction with a
// single catch-all `POST /*` dispatcher (`dispatchWebhook`); per-watch
// path lookup is then a map check against `pathToWatch`. This
// sidesteps chi's lack of route unregistration — `StopWatch` deletes
// the map entry and the dispatcher returns 404 for stale paths
// without leaking chi routes.
type SensorService struct {
	genv1.UnimplementedSensorServer
	mu      sync.Mutex
	watches map[string]*Watch
	// pathToWatch indexes active watches by their `path_prefix` so the
	// catch-all dispatcher can resolve an inbound POST to the right
	// watch in O(1). Updated transactionally with `watches`.
	pathToWatch    map[string]*Watch
	router         *chi.Mux
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewSensorService constructs the service. router is the chi mux the
// binary mounts on the inbound-webhook port; pre-existing routes are
// preserved. The constructor installs a single catch-all `POST /*`
// dispatcher; `StartWatch` and `StopWatch` mutate the watch map only,
// which means re-using a (instance, path_prefix) pair after a stop +
// start no longer accumulates chi routes (chi has no unregister API).
func NewSensorService(rimskyEndpoint string, router *chi.Mux, log logger) *SensorService {
	s := &SensorService{
		watches:        make(map[string]*Watch),
		pathToWatch:    make(map[string]*Watch),
		router:         router,
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		clock:          time.Now,
		logger:         log,
	}
	// Single dispatcher route per service instance. Subsequent
	// StartWatch / StopWatch calls only touch the in-memory map; the
	// chi tree is never mutated after this point.
	router.Post("/*", s.dispatchWebhook)
	return s
}

// dispatchWebhook is the single catch-all POST handler the chi router
// is wired to at construction. It resolves the inbound URL.Path to an
// active watch via `pathToWatch`; misses surface as 404 with an
// operator-visible message. The lookup is keyed on the exact path
// the watch registered — sensor-webhook does not currently support
// nested path matching under a prefix; if/when that's needed the
// dispatcher gains a longest-prefix match.
func (s *SensorService) dispatchWebhook(rw http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	w, ok := s.pathToWatch[req.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.Error(rw, "no active sensor-webhook watch for this path", http.StatusNotFound)
		return
	}
	s.serveWebhook(w, rw, req)
}

// Capabilities advertises the webhook kind.
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.SensorCapabilities, error) {
	return &genv1.SensorCapabilities{
		SupportedKinds: []*genv1.SensorKindCapability{
			{
				Kind: "webhook",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"path_prefix": {"type": "string"},
						"idempotency_header": {"type": "string"}
					},
					"required": ["path_prefix"]
				}`),
			},
		},
		Protocols: []string{"sensor"},
	}, nil
}

// StartWatch parses the resolved_config and mounts a webhook route under
// `path_prefix`. The route handler captures the watch_id via closure
// (rather than re-parsing the URL) so the lookup is O(1).
func (s *SensorService) StartWatch(_ context.Context, req *genv1.StartWatchRequest) (*genv1.StartWatchResponse, error) {
	if req.GetKind() != "webhook" {
		return nil, fmt.Errorf("sensor-webhook does not support kind %q", req.GetKind())
	}
	var cfg struct {
		PathPrefix        string `json:"path_prefix"`
		IdempotencyHeader string `json:"idempotency_header"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.PathPrefix == "" {
		return nil, fmt.Errorf("resolved_config.path_prefix required")
	}
	if !strings.HasPrefix(cfg.PathPrefix, "/") {
		cfg.PathPrefix = "/" + cfg.PathPrefix
	}
	w := &Watch{
		WatchID:           req.GetWatchId(),
		InstanceID:        req.GetInstanceId(),
		PathPrefix:        cfg.PathPrefix,
		IdempotencyHeader: cfg.IdempotencyHeader,
		StartedAt:         s.clock(),
	}
	s.mu.Lock()
	if _, exists := s.watches[w.WatchID]; exists {
		s.mu.Unlock()
		return &genv1.StartWatchResponse{}, nil
	}
	if existing, taken := s.pathToWatch[w.PathPrefix]; taken {
		s.mu.Unlock()
		return nil, fmt.Errorf("path_prefix %q already bound to watch %s", w.PathPrefix, existing.WatchID)
	}
	s.watches[w.WatchID] = w
	s.pathToWatch[w.PathPrefix] = w
	s.mu.Unlock()
	s.logger.Info("sensor-webhook.start_watch",
		"watch_id", w.WatchID, "instance_id", w.InstanceID, "path", w.PathPrefix)
	return &genv1.StartWatchResponse{}, nil
}

// serveWebhook executes the per-watch reception logic. Called by the
// catch-all chi dispatcher in `dispatchWebhook` after resolving the
// inbound URL.Path to a live watch via `pathToWatch`. Splitting the
// dispatch from the per-watch logic keeps chi route registration
// O(routes) regardless of watch churn.
//
// Idempotency: when `IdempotencyHeader` is set and the inbound request
// carries that header, suppress emission if the value matches the
// most-recent seen.
func (s *SensorService) serveWebhook(w *Watch, rw http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(rw, "read body", http.StatusBadRequest)
		return
	}
	// Idempotency suppression — operator-opt-in.
	if w.IdempotencyHeader != "" {
		val := req.Header.Get(w.IdempotencyHeader)
		if val != "" {
			w.mu.Lock()
			if w.LastIdempotency == val {
				w.mu.Unlock()
				rw.WriteHeader(http.StatusOK)
				return
			}
			w.LastIdempotency = val
			w.mu.Unlock()
		}
	}
	now := s.clock()
	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"path":        req.URL.Path,
		"method":      req.Method,
	}
	// Best-effort: decode the body as JSON for substitution-friendly
	// observations. Non-JSON surfaces as a string.
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	// Forward a select set of headers (operator might add more here
	// later — kept narrow for now to avoid leaking auth bearer tokens
	// or other secrets into observations).
	if w.IdempotencyHeader != "" {
		if v := req.Header.Get(w.IdempotencyHeader); v != "" {
			obs["idempotency_key"] = v
		}
	}
	if err := s.postObservation(req.Context(), w.WatchID, obs); err != nil {
		s.logger.Warn("sensor-webhook.observation_post_failed",
			"watch_id", w.WatchID, "error", err.Error())
		http.Error(rw, "rimsky push failed", http.StatusBadGateway)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

// StopWatch removes the watch from the dispatcher's path index. The
// chi router carries a single catch-all `POST /*` dispatcher
// (installed by `NewSensorService`); stopping a watch deletes the
// `pathToWatch` entry so the next inbound POST returns 404. The chi
// router itself is never mutated post-construction, so there is no
// route leak even when watches churn (the cause of the original
// route-leak finding).
func (s *SensorService) StopWatch(_ context.Context, req *genv1.StopWatchRequest) (*genv1.StopWatchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.watches[req.GetWatchId()]
	if !ok {
		return &genv1.StopWatchResponse{}, nil
	}
	delete(s.watches, req.GetWatchId())
	if w != nil {
		// Tolerate a path having been rebound to a different watch
		// between StartWatch and StopWatch (defensive — should not
		// happen because StartWatch refuses path collisions).
		if cur, present := s.pathToWatch[w.PathPrefix]; present && cur == w {
			delete(s.pathToWatch, w.PathPrefix)
		}
	}
	s.logger.Info("sensor-webhook.stop_watch", "watch_id", req.GetWatchId())
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
			Kind:       "webhook",
			StartedAt:  timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListWatchesResponse{Watches: out}, nil
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
