// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-http bundled sensor. Implements the Publisher
// gRPC protocol; per publisher-subscription, polls a configured URL on
// a fixed interval, applies a match predicate (status code and / or
// JSONPath substring match), and POSTs a message envelope to rimsky's
// generic `POST /instances/{instance_id}/messages` endpoint when the
// response body's content-hash changes.
//
// Spec .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
//	@concept: sensor
//
// State persistence is optional. When env
// RIMSKY_SENSOR_HTTP_STATE_DSN is set, subscriptions and body-hash
// watermarks survive restart; otherwise the binary runs in-memory.
// Per `concept:replica`, the v1 contract is single-replica.
//
// Watermarking: per-subscription high-water-mark is the SHA-256 of
// the last-observed response body. The sensor pushes only when the
// new body hash differs from the prior — operator-visible churn
// reduction for sources that don't carry a monotonic version.
// Operators wanting "push every poll regardless of body" omit `match`
// and set a single status-only `match.status`.
package main

import (
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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/sensors/internal/post"
)

// Watch is the in-memory state for one active HTTP publisher-
// subscription. Sensor-internal vocabulary stays as "Watch."
type Watch struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	PollInterval   time.Duration
	MatchStatus    []int  // empty → any 2xx is a match
	MatchJSONKey   string // dotted path within response JSON; empty → no JSON match
	MatchJSONVal   string // expected value at that path (substring match); empty → presence-only
	TargetNode     string
	MessageKind    string

	LastPollAt time.Time
	LastHash   string // sha256 hex of last response body that matched
}

// SensorService implements genv1.PublisherServer for HTTP polling.
type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
	// state is the optional persistence layer. nil → in-memory mode.
	state *stateDB
}

// AttachStateDB binds an optional persistence layer for subscriptions +
// body-hash watermarks. Pass nil to run in pure in-memory mode.
func (s *SensorService) AttachStateDB(state *stateDB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
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
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
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
		Protocols: []string{"publisher"},
	}, nil
}

// Subscribe parses the resolved_config and registers the
// publisher-subscription. Idempotent on publisher_subscription_id.
// When a state DB is attached, looks up persisted state and pre-
// populates `LastHash` so restart-replay does not re-emit on the first
// poll with an unchanged body.
func (s *SensorService) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
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
	messageKind := req.GetMessageKind()
	if messageKind == "" {
		messageKind = "invalidate"
	}
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		URL:            cfg.URL,
		PollInterval:   interval,
		MatchStatus:    cfg.Match.Status,
		MatchJSONKey:   cfg.Match.JSONPath.Path,
		MatchJSONVal:   cfg.Match.JSONPath.Value,
		TargetNode:     req.GetTargetNode(),
		MessageKind:    messageKind,
	}
	// Restart-replay: look up persisted state and pre-populate the
	// body-hash watermark before publishing the Watch into the in-memory
	// map. Without this, the first poll after a restart re-emits even
	// when the body hasn't changed.
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if persisted, err := state.GetSubscription(ctx, w.SubscriptionID); err != nil {
			s.logger.Warn("sensor-http.subscribe.state_get_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		} else if persisted != nil {
			w.LastHash = persisted.LastHash
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		// Idempotent Subscribe: the state-DB row is already present from
		// the prior call, so we skip the UpsertSubscription below.
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	s.logger.Info("sensor-http.subscribe",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"url", w.URL,
		"poll_interval", interval.String(),
		"restored_last_hash", w.LastHash != "")
	if state != nil {
		if err := state.UpsertSubscription(context.Background(), w); err != nil {
			s.logger.Warn("sensor-http.subscribe.state_upsert_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}
	return &genv1.SubscribeResponse{}, nil
}

// Unsubscribe removes the subscription. Idempotent.
func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetPublisherSubscriptionId()]; ok {
		delete(s.watches, req.GetPublisherSubscriptionId())
		s.logger.Info("sensor-http.unsubscribe", "publisher_subscription_id", req.GetPublisherSubscriptionId())
		if s.state != nil {
			if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
				s.logger.Warn("sensor-http.unsubscribe.state_delete_failed",
					"publisher_subscription_id", req.GetPublisherSubscriptionId(), "error", err.Error())
			}
		}
	}
	return &genv1.UnsubscribeResponse{}, nil
}

// ListSubscriptions enumerates active publisher-subscriptions.
func (s *SensorService) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: w.SubscriptionID,
			InstanceId:              w.InstanceID,
			Kind:                    "http",
			TargetNode:              w.TargetNode,
			MessageKind:             w.MessageKind,
			StartedAt:               timestamppb.New(s.clock()),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

// Tick polls any subscription whose `last_poll_at + poll_interval <=
// now`.
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
// pushes a message envelope when the body hash differs from the prior.
// Updates the watermark on every poll regardless of match outcome
// (so transient mismatches don't repeatedly fire).
func (s *SensorService) pollOne(ctx context.Context, w *Watch, now time.Time) {
	s.mu.Lock()
	w.LastPollAt = now
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.URL, nil)
	if err != nil {
		s.logger.Warn("sensor-http.poll_build_request_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("sensor-http.poll_dial_failed",
			"publisher_subscription_id", w.SubscriptionID, "url", w.URL, "error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Warn("sensor-http.poll_read_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
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
	cur, ok := s.watches[w.SubscriptionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if cur.LastHash == hash {
		s.mu.Unlock()
		return
	}
	cur.LastHash = hash
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if err := state.UpdateLastHash(ctx, w.SubscriptionID, hash); err != nil {
			s.logger.Warn("sensor-http.poll.state_update_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}

	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"url":         w.URL,
		"status":      resp.StatusCode,
		"body_hash":   hash,
	}
	// Best-effort include a decoded JSON body so substitution can read
	// `{{trigger.message.payload.body.<path>}}`. Non-JSON bodies surface
	// as a string.
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	// Idempotency key: subscription_id + body hash. Re-emitting the
	// same body is a no-op at the server.
	idemKey := fmt.Sprintf("%s+%s", w.SubscriptionID, hash)
	if err := s.postMessage(ctx, w, obs, idemKey); err != nil {
		s.logger.Warn("sensor-http.message_post_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
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

// postMessage sends one message envelope to rimsky's generic messages
// endpoint with sender_kind="publisher". Retry-with-backoff is
// handled by `pkg:github.com/fallguyconsulting/rimsky/sensors/internal/post`.
func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"kind":                      w.MessageKind,
		"target":                    w.TargetNode,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-http",
		"sender_kind":               "publisher",
		"publisher_subscription_id": w.SubscriptionID,
	}
	raw, err := post.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	url := s.rimskyEndpoint + "/instances/" + w.InstanceID + "/messages"
	res := post.Send(ctx, s.httpClient, s.logger, nil, post.Request{
		URL:            url,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		SensorName:     "sensor-http",
		SubscriptionID: w.SubscriptionID,
	})
	return res.Err
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
