// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//	@concept: sensor
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
)

type Watch struct {
	SubscriptionID string
	InstanceID     string
	CronExpr       string
	TargetNode     string
	MessageType    string
	NextFireAt     time.Time
	StartedAt      time.Time
	LastFireAt     *time.Time
}

type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
	tickInterval   time.Duration
	state *stateDB
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
		s.logger.Warn("sensor-cron.attach_state_db.list_failed", "error", err.Error())
		return
	}
	for _, r := range rows {
		lastFire := r.LastFireAt
		s.watches[r.SubscriptionID] = &Watch{
			SubscriptionID: r.SubscriptionID,
			InstanceID:     r.InstanceID,
			CronExpr:       r.CronExpr,
			TargetNode:     r.TargetNode,
			MessageType:    r.MessageType,
			NextFireAt:     r.NextFireAt,
			StartedAt:      r.StartedAt,
			LastFireAt:     lastFire,
		}
		s.logger.Info("sensor-cron.state_recovered",
			"publisher_subscription_id", r.SubscriptionID,
			"next_fire_at", r.NextFireAt.Format(time.RFC3339))
	}
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

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

func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
			{
				Kind: "cron",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"cron": {"type": "string"}
					},
					"required": ["cron"]
				}`),
			},
		},
		Protocols: []string{"publisher"},
	}, nil
}

func (s *SensorService) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	if req.GetKind() != "cron" {
		return nil, fmt.Errorf("sensor-cron does not support kind %q", req.GetKind())
	}
	var cfg struct {
		Cron string `json:"cron"`
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
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		CronExpr:       cfg.Cron,
		TargetNode:     req.GetTargetNode(),
		MessageType:    messageType,
		NextFireAt:     sched.Next(now),
		StartedAt:      now,
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		s.mu.Unlock()
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	state := s.state
	s.mu.Unlock()
	s.logger.Info("sensor-cron.subscribe",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"cron", cfg.Cron,
		"next_fire_at", w.NextFireAt.Format(time.RFC3339))
	if state != nil {
		if err := state.UpsertSubscription(context.Background(), w); err != nil {
			s.logger.Warn("sensor-cron.subscribe.state_upsert_failed",
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
		s.logger.Info("sensor-cron.unsubscribe", "publisher_subscription_id", req.GetPublisherSubscriptionId())
		if s.state != nil {
			if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
				s.logger.Warn("sensor-cron.unsubscribe.state_delete_failed",
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
			Kind:                    "cron",
			TargetNode:              w.TargetNode,
			MessageType:             w.MessageType,
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
		if !now.Before(w.NextFireAt) {
			due = append(due, w)
		}
	}
	s.mu.Unlock()

	for _, w := range due {
		s.fireOne(ctx, w, now)
	}
}

func (s *SensorService) fireOne(ctx context.Context, w *Watch, now time.Time) {
	body := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"cron":        w.CronExpr,
		"fire_at":     w.NextFireAt.UTC().Format(time.RFC3339),
	}
	idemKey := fmt.Sprintf("%s+%s", w.SubscriptionID, w.NextFireAt.UTC().Format(time.RFC3339))
	if err := s.postMessage(ctx, w, body, idemKey); err != nil {
		s.logger.Warn("sensor-cron.message_post_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		return
	}
	sched, err := cron.ParseStandard(w.CronExpr)
	if err != nil {
		s.logger.Error("sensor-cron.cron_parse_failed",
			"publisher_subscription_id", w.SubscriptionID, "cron", w.CronExpr)
		return
	}
	s.mu.Lock()
	cur, ok := s.watches[w.SubscriptionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	t := now
	cur.LastFireAt = &t
	cur.NextFireAt = sched.Next(cur.NextFireAt)
	nextFireAt := cur.NextFireAt
	lastFireAt := cur.LastFireAt
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if err := state.UpdateNextFire(ctx, w.SubscriptionID, nextFireAt, lastFireAt); err != nil {
			s.logger.Warn("sensor-cron.fire.state_update_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}
}

func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"type":                      w.MessageType,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-cron",
		"sender_kind":               "publisher",
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
		SensorName:     "sensor-cron",
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
