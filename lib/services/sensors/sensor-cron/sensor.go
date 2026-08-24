// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorpub"
)

type Watch struct {
	SubscriptionID string
	InstanceID     string
	CronExpr       string
	Schedule       cron.Schedule
	MessageType    string
	ResolvedConfig []byte
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
		s.logger.Warn("SENSORCRON.STATEDBATTACH.LISTFAILED", "error", err.Error())
		return
	}
	for _, r := range rows {
		sched, err := cron.ParseStandard(r.CronExpr)
		if err != nil {
			s.logger.Error("SENSORCRON.STATEDBATTACH.CRONPARSEFAILED",
				"publisher_subscription_id", r.SubscriptionID, "cron", r.CronExpr, "error", err.Error())
			continue
		}
		lastFire := r.LastFireAt
		s.watches[r.SubscriptionID] = &Watch{
			SubscriptionID: r.SubscriptionID,
			InstanceID:     r.InstanceID,
			CronExpr:       r.CronExpr,
			Schedule:       sched,
			MessageType:    r.MessageType,
			NextFireAt:     r.NextFireAt,
			StartedAt:      r.StartedAt,
			LastFireAt:     lastFire,
		}
		s.logger.Info("SENSORCRON.STATE.RECOVERED",
			"publisher_subscription_id", r.SubscriptionID,
			"next_fire_at", r.NextFireAt.Format(time.RFC3339))
	}
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

func (s *SensorService) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
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

	s.mu.Lock()
	state := s.state
	s.mu.Unlock()

	var persisted *SubscriptionState
	if state != nil {
		p, getErr := state.GetSubscription(ctx, req.GetPublisherSubscriptionId())
		if getErr != nil {
			s.logger.Warn("SENSORCRON.SUBSCRIBE.STATEGETFAILED",
				"publisher_subscription_id", req.GetPublisherSubscriptionId(), "error", getErr.Error())
		} else {
			persisted = p
		}
	}

	now := s.clock().UTC()
	nextFireAt := sched.Next(now)
	startedAt := now
	var lastFireAt *time.Time
	if persisted != nil {
		nextFireAt = persisted.NextFireAt
		startedAt = persisted.StartedAt
		lastFireAt = persisted.LastFireAt
	}
	if nextFireAt.IsZero() {
		s.logger.Warn("SENSORCRON.SCHEDULE.NEVERFIRES",
			"publisher_subscription_id", req.GetPublisherSubscriptionId(), "cron", cfg.Cron)
	}
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		CronExpr:       cfg.Cron,
		Schedule:       sched,
		MessageType:    messageType,
		ResolvedConfig: append([]byte(nil), req.GetResolvedConfig()...),
		NextFireAt:     nextFireAt,
		StartedAt:      startedAt,
		LastFireAt:     lastFireAt,
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		s.mu.Unlock()
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	s.mu.Unlock()
	s.logger.Info("SENSORCRON.SUBSCRIPTION.MOUNTED",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"cron", cfg.Cron,
		"next_fire_at", w.NextFireAt.Format(time.RFC3339),
		"restored_from_state", persisted != nil)
	if state != nil {
		if err := state.UpsertSubscription(ctx, w); err != nil {
			s.mu.Lock()
			if s.watches[w.SubscriptionID] == w {
				delete(s.watches, w.SubscriptionID)
			}
			s.mu.Unlock()
			s.logger.Warn("SENSORCRON.SUBSCRIBE.STATEUPSERTFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			return nil, fmt.Errorf("sensor-cron: persist subscription %s: %w", w.SubscriptionID, err)
		}
	}
	return &genv1.SubscribeResponse{}, nil
}

func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetPublisherSubscriptionId()]; ok {
		delete(s.watches, req.GetPublisherSubscriptionId())
		s.logger.Info("SENSORCRON.SUBSCRIPTION.STOPPED", "publisher_subscription_id", req.GetPublisherSubscriptionId())
		if s.state != nil {
			if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
				s.logger.Warn("SENSORCRON.UNSUBSCRIBE.STATEDELETEFAILED",
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
			MessageType:             w.MessageType,
			ResolvedConfig:          w.ResolvedConfig,
			StartedAt:               timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func (s *SensorService) Tick(ctx context.Context) {
	now := s.clock().UTC()
	s.mu.Lock()
	due := make([]*Watch, 0)
	for _, w := range s.watches {
		if !w.NextFireAt.IsZero() && !now.Before(w.NextFireAt) {
			due = append(due, w)
		}
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, w := range due {
		wg.Add(1)
		go func(w *Watch) {
			defer wg.Done()
			s.fireOne(ctx, w, now)
		}(w)
	}
	wg.Wait()
}

func (s *SensorService) fireOne(ctx context.Context, w *Watch, now time.Time) {
	if w.Schedule == nil {
		s.logger.Error("SENSORCRON.FIRE.NOSCHEDULE",
			"publisher_subscription_id", w.SubscriptionID, "cron", w.CronExpr)
		return
	}
	missedWindows, caughtUpTo := coalesceMissedWindows(w.Schedule, w.NextFireAt, now)
	body := map[string]any{
		"observed_at":    now.UTC().Format(time.RFC3339),
		"cron":           w.CronExpr,
		"fire_at":        w.NextFireAt.UTC().Format(time.RFC3339),
		"missed_windows": missedWindows - 1,
	}
	idemKey := cronIdempotencyKey(w.SubscriptionID, w.NextFireAt)
	if err := s.postMessage(ctx, w, body, idemKey); err != nil {
		var rejected *publisherkit.RejectedError
		if !errors.As(err, &rejected) {
			s.logger.Warn("SENSORCRON.MESSAGE.POSTFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			return
		}
		s.logger.Error("SENSORCRON.MESSAGE.REJECTED", "detail", "the message was dropped",
			"publisher_subscription_id", w.SubscriptionID, "status", rejected.Status, "error", err.Error())
	}
	s.mu.Lock()
	cur, ok := s.watches[w.SubscriptionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	t := now
	cur.LastFireAt = &t
	cur.NextFireAt = caughtUpTo
	nextFireAt := cur.NextFireAt
	lastFireAt := cur.LastFireAt
	state := s.state
	s.mu.Unlock()
	if nextFireAt.IsZero() {
		s.logger.Warn("SENSORCRON.SCHEDULE.EXHAUSTED", "detail", "the schedule never fires again",
			"publisher_subscription_id", w.SubscriptionID, "cron", w.CronExpr)
	}
	if missedWindows > 1 {
		s.logger.Warn("SENSORCRON.CATCHUP.COALESCED",
			"publisher_subscription_id", w.SubscriptionID, "missed_windows", missedWindows-1)
	}
	if state != nil {
		if err := state.UpdateNextFire(ctx, w.SubscriptionID, nextFireAt, lastFireAt); err != nil {
			s.logger.Warn("SENSORCRON.FIRE.STATEUPDATEFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}
}

func coalesceMissedWindows(sched cron.Schedule, dueAt, now time.Time) (int, time.Time) {
	missed := 0
	next := dueAt
	for !next.After(now) {
		missed++
		advanced := sched.Next(next)
		if advanced.IsZero() || !advanced.After(next) {
			return missed, advanced
		}
		next = advanced
	}
	return missed, next
}

func cronIdempotencyKey(subscriptionID string, fireAt time.Time) string {
	return fmt.Sprintf("%s+%s", subscriptionID, fireAt.UTC().Format(time.RFC3339))
}

func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	return sensorpub.PostMessage(ctx, s.httpClient, s.logger, s.rimskyEndpoint, "sensor-cron",
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
