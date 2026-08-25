// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorauth"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorpub"
)

const maxPollBodyBytes = 10 * 1024 * 1024

type Watch struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	PollInterval   time.Duration
	MatchStatus    []int
	MatchJSONKey   string
	MatchJSONVal   string
	MessageType    string
	ResolvedConfig []byte
	Auth           *sensorauth.AuthConfig
	StartedAt      time.Time

	LastPollAt time.Time
	LastHash   string
}

type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	watermarks     map[string]SubscriptionWatermark
	rimskyEndpoint string
	httpClient     *http.Client
	pollClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
	state          *stateDB
	pollsInFlight  atomic.Int64
}

func (s *SensorService) PollsInFlight() int { return int(s.pollsInFlight.Load()) }

func (s *SensorService) AttachStateDB(state *stateDB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if state == nil {
		return
	}
	// @decision: secret-at-rest-posture
	rows, err := state.ListWatermarks(context.Background())
	if err != nil {
		s.logger.Warn("SENSORHTTP.STATEDBATTACH.LISTFAILED", "error", err.Error())
		return
	}
	for _, r := range rows {
		s.watermarks[r.SubscriptionID] = r
	}
	s.logger.Info("SENSORHTTP.WATERMARKS.RESTORED", "count", len(rows))
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

func (s *SensorService) SetPublishClient(c *http.Client) {
	if c != nil {
		s.httpClient = c
	}
}

func NewSensorService(rimskyEndpoint string, pollGuard egress.Guard, log logger) *SensorService {
	return &SensorService{
		watches:        make(map[string]*Watch),
		watermarks:     make(map[string]SubscriptionWatermark),
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		pollClient:     pollGuard.HTTPClient(30 * time.Second),
		clock:          time.Now,
		logger:         log,
		tickInterval:   time.Second,
	}
}

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
						},
						"auth": {
							"type": "object",
							"properties": {
								"mode": {"type": "string", "enum": ["secret_header", "none"]},
								"header": {"type": "string"},
								"secret": {"type": "string"}
							},
							"required": ["mode"]
						}
					},
					"required": ["url"]
				}`),
			},
		},
		Protocols: []string{"publisher"},
	}, nil
}

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
		Auth *sensorauth.AuthConfig `json:"auth"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("resolved_config.url required")
	}
	// @decision: http-poll-sensor-auth-outbound
	if err := sensorauth.ValidateOutbound(cfg.Auth); err != nil {
		return nil, err
	}
	interval := 30 * time.Second
	if cfg.PollInterval != "" {
		d, err := time.ParseDuration(cfg.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid poll_interval %q: %w", cfg.PollInterval, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("poll_interval %q must be positive", cfg.PollInterval)
		}
		interval = d
	}
	messageType := req.GetMessageType()
	now := s.clock()
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		URL:            cfg.URL,
		PollInterval:   interval,
		MatchStatus:    cfg.Match.Status,
		MatchJSONKey:   cfg.Match.JSONPath.Path,
		MatchJSONVal:   cfg.Match.JSONPath.Value,
		MessageType:    messageType,
		ResolvedConfig: append([]byte(nil), req.GetResolvedConfig()...),
		Auth:           cfg.Auth,
		StartedAt:      now,
	}
	s.mu.Lock()
	state := s.state
	watermark, cached := s.watermarks[w.SubscriptionID]
	s.mu.Unlock()
	// @decision: secret-at-rest-posture
	if !cached && state != nil {
		if persisted, err := state.GetWatermark(ctx, w.SubscriptionID); err != nil {
			s.logger.Warn("SENSORHTTP.SUBSCRIBE.STATEGETFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		} else if persisted != nil {
			watermark, cached = *persisted, true
		}
	}
	if cached {
		w.LastHash = watermark.LastHash
		w.LastPollAt = watermark.LastPollAt
		w.StartedAt = watermark.StartedAt
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		s.mu.Unlock()
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	s.mu.Unlock()
	s.logger.Info("SENSORHTTP.SUBSCRIPTION.MOUNTED",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"url", w.URL,
		"poll_interval", interval.String(),
		"restored_last_hash", w.LastHash != "")
	if state != nil {
		if err := state.UpsertSubscription(ctx, w); err != nil {
			s.mu.Lock()
			if s.watches[w.SubscriptionID] == w {
				delete(s.watches, w.SubscriptionID)
			}
			s.mu.Unlock()
			s.logger.Warn("SENSORHTTP.SUBSCRIBE.STATEUPSERTFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			return nil, fmt.Errorf("sensor-http: persist subscription %s: %w", w.SubscriptionID, err)
		}
	}
	return &genv1.SubscribeResponse{}, nil
}

func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetPublisherSubscriptionId()]; ok {
		delete(s.watches, req.GetPublisherSubscriptionId())
		delete(s.watermarks, req.GetPublisherSubscriptionId())
		s.logger.Info("SENSORHTTP.SUBSCRIPTION.STOPPED", "publisher_subscription_id", req.GetPublisherSubscriptionId())
		if s.state != nil {
			if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
				s.logger.Warn("SENSORHTTP.UNSUBSCRIBE.STATEDELETEFAILED",
					"publisher_subscription_id", req.GetPublisherSubscriptionId(), "error", err.Error())
			}
		}
	}
	return &genv1.UnsubscribeResponse{}, nil
}

func (s *SensorService) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: w.SubscriptionID,
			InstanceId:              w.InstanceID,
			Kind:                    "http",
			MessageType:             w.MessageType,
			ResolvedConfig:          w.ResolvedConfig,
			StartedAt:               timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

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
	var wg sync.WaitGroup
	for _, w := range due {
		wg.Add(1)
		go func(w *Watch) {
			defer wg.Done()
			s.pollOne(ctx, w, now)
		}(w)
	}
	wg.Wait()
}

func (s *SensorService) pollOne(ctx context.Context, w *Watch, now time.Time) {
	s.pollsInFlight.Add(1)
	defer s.pollsInFlight.Add(-1)
	s.mu.Lock()
	w.LastPollAt = now
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.URL, nil)
	if err != nil {
		s.logger.Warn("SENSORHTTP.POLL.BUILDREQUESTFAILED",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		return
	}
	// @decision: http-poll-sensor-auth-outbound
	sensorauth.ApplyOutbound(w.Auth, req)
	resp, err := s.pollClient.Do(req)
	if err != nil {
		s.logger.Warn("SENSORHTTP.POLL.DIALFAILED",
			"publisher_subscription_id", w.SubscriptionID, "url", w.URL, "error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPollBodyBytes+1))
	if err != nil {
		s.logger.Warn("SENSORHTTP.POLL.READFAILED",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		return
	}
	if int64(len(body)) > maxPollBodyBytes {
		s.logger.Warn("SENSORHTTP.POLL.BODYTOOLARGE",
			"publisher_subscription_id", w.SubscriptionID, "url", w.URL, "limit_bytes", maxPollBodyBytes)
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
	s.mu.Unlock()

	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"url":         w.URL,
		"status":      resp.StatusCode,
		"body_hash":   hash,
	}
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	idemKey := fmt.Sprintf("%s+%s+%d", w.SubscriptionID, hash, now.UnixNano())
	if err := s.postMessage(ctx, w, obs, idemKey); err != nil {
		var rejected *publisherkit.RejectedError
		if !errors.As(err, &rejected) {
			s.logger.Warn("SENSORHTTP.MESSAGE.POSTFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			return
		}
		s.logger.Error("SENSORHTTP.MESSAGE.REJECTED", "detail", "the message was dropped",
			"publisher_subscription_id", w.SubscriptionID, "status", rejected.Status, "error", err.Error())
	}

	s.mu.Lock()
	cur, ok = s.watches[w.SubscriptionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	cur.LastHash = hash
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if err := state.UpdateLastHash(ctx, w.SubscriptionID, hash); err != nil {
			s.logger.Warn("SENSORHTTP.POLL.STATEUPDATEFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}
}

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
		s = fmt.Sprintf("%v", got)
	}
	return s == value
}

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

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	return sensorpub.PostMessage(ctx, s.httpClient, s.logger, s.rimskyEndpoint, "sensor-http",
		w.MessageType, w.InstanceID, w.SubscriptionID, payload, idempotencyKey)
}

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
