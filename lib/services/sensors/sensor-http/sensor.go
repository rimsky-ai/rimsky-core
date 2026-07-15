// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

type Watch struct {
	SubscriptionID string
	InstanceID     string
	URL            string
	PollInterval   time.Duration
	MatchStatus    []int
	MatchJSONKey   string
	MatchJSONVal   string
	MessageType    string

	LastPollAt time.Time
	LastHash   string
}

type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	rimskyEndpoint string
	httpClient     *http.Client
	pollClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
	state          *stateDB
}

func (s *SensorService) AttachStateDB(state *stateDB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if state == nil {
		return
	}
	rows, err := state.ListAll(context.Background())
	if err != nil {
		s.logger.Warn("sensor-http.attach_state_db.list_failed", "error", err.Error())
		return
	}
	for _, r := range rows {
		s.watches[r.SubscriptionID] = &Watch{
			SubscriptionID: r.SubscriptionID,
			InstanceID:     r.InstanceID,
			URL:            r.URL,
			PollInterval:   r.PollInterval,
			MatchStatus:    r.MatchStatus,
			MatchJSONKey:   r.MatchJSONKey,
			MatchJSONVal:   r.MatchJSONVal,
			MessageType:    r.MessageType,
			LastHash:       r.LastHash,
		}
		s.logger.Info("sensor-http.state_recovered",
			"publisher_subscription_id", r.SubscriptionID,
			"url", r.URL,
			"poll_interval", r.PollInterval.String(),
			"restored_last_hash", r.LastHash != "")
	}
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

func NewSensorService(rimskyEndpoint string, pollGuard egress.Guard, log logger) *SensorService {
	return &SensorService{
		watches:        make(map[string]*Watch),
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
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		URL:            cfg.URL,
		PollInterval:   interval,
		MatchStatus:    cfg.Match.Status,
		MatchJSONKey:   cfg.Match.JSONPath.Path,
		MatchJSONVal:   cfg.Match.JSONPath.Value,
		MessageType:    messageType,
	}
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
			StartedAt:               timestamppb.New(s.clock()),
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
	for _, w := range due {
		s.pollOne(ctx, w, now)
	}
}

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
	resp, err := s.pollClient.Do(req)
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
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	idemKey := fmt.Sprintf("%s+%s", w.SubscriptionID, hash)
	if err := s.postMessage(ctx, w, obs, idemKey); err != nil {
		s.logger.Warn("sensor-http.message_post_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
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
	return strings.Contains(s, value)
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
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"type":                      w.MessageType,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-http",
		"publisher_subscription_id": w.SubscriptionID,
	}
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	url := s.rimskyEndpoint + "/v1/instances/" + w.InstanceID + "/messages"
	res := publisherkit.Send(ctx, s.httpClient, s.logger, nil, publisherkit.Request{
		URL:            url,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		PublisherName:  "sensor-http",
		SubscriptionID: w.SubscriptionID,
	})
	return res.Err
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
