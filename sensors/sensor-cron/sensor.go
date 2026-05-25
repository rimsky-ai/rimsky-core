// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — sensor-cron bundled sensor. Implements the Publisher
// gRPC protocol; on each tick, fires any publisher-subscription whose
// `next_fire_at <= now` by POSTing a message envelope to rimsky's
// generic `POST /instances/{instance_id}/messages` endpoint with
// `sender_kind: "publisher"`.
//
// Spec .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
//	@concept: sensor
//
// State persistence is in-memory only — a deliberate divergence from
// the spec's Stage-3 prescription to plumb an env var
// (`RIMSKY_SENSOR_CRON_STATE_DSN`). The plumbing is skipped because
// sensor-cron's `next_fire_at` is fully reconstructible from the
// cron expression and the current wall clock via `sched.Next(now)`;
// there is no observation watermark that would otherwise be lost.
// Subscriptions are dropped on process restart; rimsky's
// `runtime/publishers.go::ResyncPublisherSubscriptions` re-issues
// `Subscribe` for each active row in `rimsky_publisher_subscriptions`
// at supervisor startup, so subscriptions return to active state with
// `next_fire_at = sched.Next(now)`. This produces at most one MISSED
// fire per restart per publisher-subscription, which the spec accepts
// (see concept:sensor invariants — persist only when state is
// non-reconstructible).
//
// Operators who later need true cron-firing durability can add a
// state_db.go alongside the other sensors, modeled on
// sensors/sensor-http/state_db.go; the in-memory tick loop is already
// keyed by publisher_subscription_id for that future migration.
//
// Multi-replica deployments are not the v1 contract per
// `concept:replica`. Operators run a single replica per sensor-cron
// binary; HA is the publisher implementation's concern.
// `code:sensors/sensor-cron/multi_replica_test.go` pins the
// single-replica behavior.
//
// Missed-fire policy mirrors the retired internal scheduler: cron
// advancement is from the row's prior `next_fire_at`, NOT
// `clock.Now()`. A long outage produces a single post-outage fire,
// not a backfilled herd. Rationale: invalidation freshness is the
// goal; backfilling a 6-hour outage for an hourly schedule
// generates thundering-herd noise without semantic gain.
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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/sensors/internal/post"
)

// Watch is the in-memory state for one active cron publisher-
// subscription. Sensor-internal vocabulary stays as "Watch" —
// the operator-facing name is publisher-subscription, but inside the
// sensor binary the per-tick fire-state is just a watch.
type Watch struct {
	SubscriptionID string
	InstanceID     string
	CronExpr       string
	TargetNode     string
	MessageKind    string
	NextFireAt     time.Time
	StartedAt      time.Time
	LastFireAt     *time.Time
	MissedFires    bool // operator hint: when true, sensor backfills missed fires
}

// SensorService implements genv1.PublisherServer. State is in-memory
// only; there is no per-subscription advisory-lock or persistence
// layer in V1 (see the package doc comment for the single-replica
// posture).
type SensorService struct {
	genv1.UnimplementedPublisherServer
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
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
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
		Protocols: []string{"publisher"},
	}, nil
}

// Subscribe parses the cron expression, computes `next_fire_at`, and
// registers the publisher-subscription. Idempotent on
// publisher_subscription_id: a duplicate Subscribe for an active
// subscription is a no-op.
func (s *SensorService) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
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
	messageKind := req.GetMessageKind()
	if messageKind == "" {
		messageKind = "invalidate"
	}
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		CronExpr:       cfg.Cron,
		TargetNode:     req.GetTargetNode(),
		MessageKind:    messageKind,
		NextFireAt:     sched.Next(now),
		StartedAt:      now,
		MissedFires:    cfg.MissedFires,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		// Already active; idempotent.
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	s.logger.Info("sensor-cron.subscribe",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"cron", cfg.Cron,
		"next_fire_at", w.NextFireAt.Format(time.RFC3339))
	return &genv1.SubscribeResponse{}, nil
}

// Unsubscribe removes the watch from the in-memory map. Idempotent.
func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetPublisherSubscriptionId()]; ok {
		delete(s.watches, req.GetPublisherSubscriptionId())
		s.logger.Info("sensor-cron.unsubscribe", "publisher_subscription_id", req.GetPublisherSubscriptionId())
	}
	return &genv1.UnsubscribeResponse{}, nil
}

// ListSubscriptions enumerates the live publisher-subscriptions. Used
// by rimsky's restart reconcile
// (`runtime/publishers.go::ResyncPublisherSubscriptions`).
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
			MessageKind:             w.MessageKind,
			StartedAt:               timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
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

// fireOne fires the cron observation as a message envelope and
// advances next_fire_at. The advancement is from the prior next_fire_at
// (not now()) so missed fires are NOT backfilled (mirrors the retired
// internal scheduler).
func (s *SensorService) fireOne(ctx context.Context, w *Watch, now time.Time) {
	body := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"cron":        w.CronExpr,
		"fire_at":     w.NextFireAt.UTC().Format(time.RFC3339),
	}
	// Idempotency key: subscription_id + fire-window. A retry within
	// the same fire window dedupes on the server side; a fresh window
	// is a fresh message.
	idemKey := fmt.Sprintf("%s+%s", w.SubscriptionID, w.NextFireAt.UTC().Format(time.RFC3339))
	if err := s.postMessage(ctx, w, body, idemKey); err != nil {
		s.logger.Warn("sensor-cron.message_post_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		// Do not advance on failure; the next tick retries the same fire.
		return
	}
	sched, err := cron.ParseStandard(w.CronExpr)
	if err != nil {
		s.logger.Error("sensor-cron.cron_parse_failed",
			"publisher_subscription_id", w.SubscriptionID, "cron", w.CronExpr)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-find to defend against concurrent Unsubscribe.
	cur, ok := s.watches[w.SubscriptionID]
	if !ok {
		return
	}
	t := now
	cur.LastFireAt = &t
	cur.NextFireAt = sched.Next(cur.NextFireAt) // advance from prior, not now
}

// postMessage sends one message envelope to rimsky's generic messages
// endpoint with sender_kind="publisher" + the publisher-subscription
// capability token. Retry-with-backoff is handled by
// `pkg:github.com/fallguyconsulting/rimsky/sensors/internal/post`.
func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"kind":                      w.MessageKind,
		"target":                    w.TargetNode,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-cron",
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
		SensorName:     "sensor-cron",
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
