// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
// State persistence is DSN-gated. When env
// RIMSKY_SENSOR_CRON_STATE_DSN is set, active cron publisher-
// subscriptions and their `next_fire_at` watermarks persist to a real
// Postgres state DB (see state_db.go) and survive a process restart:
// on restart the binary rebuilds its in-memory watches from the durable
// rows, recovering each subscription's ORIGINALLY-scheduled
// `next_fire_at` rather than recomputing `sched.Next(now)`. That
// recovery — restore the watermark, do not recompute it — is what lets a
// restarted binary fire on the in-flight window instead of silently
// skipping it.
//
// When the env var is empty/unset, the binary runs in-memory only
// (today's default): subscriptions are dropped on process restart and
// rimsky's `runtime/publishers.go::ResyncPublisherSubscriptions`
// re-issues `Subscribe` for each active row in
// `rimsky_publisher_subscriptions` at control-api startup, so
// subscriptions return to active state with
// `next_fire_at = sched.Next(now)`. This in-memory path produces at most
// one MISSED fire per restart per publisher-subscription, which the spec
// accepts (see concept:sensor invariants — persist only when state is
// non-reconstructible); the DSN-gated path closes that window when
// durability is required.
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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
)

// Watch is the in-memory state for one active cron publisher-
// subscription. Sensor-internal vocabulary stays as "Watch" —
// the operator-facing name is publisher-subscription, but inside the
// sensor binary the per-tick fire-state is just a watch.
//
// Missed-fire backfill is intentionally NOT implemented (see the
// package doc): a long outage produces a single post-outage fire,
// not a backfilled herd. There is therefore no per-subscription
// backfill knob on this struct.
type Watch struct {
	SubscriptionID string
	InstanceID     string
	CronExpr       string
	TargetNode     string
	MessageKind    string
	NextFireAt     time.Time
	StartedAt      time.Time
	LastFireAt     *time.Time
}

// SensorService implements genv1.PublisherServer. There is no
// per-subscription advisory-lock or cross-replica coordination in V1
// (see the package doc comment for the single-replica posture).
// Persistence is optional and DSN-gated via the attached stateDB.
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
// next-fire watermarks. Pass nil to run in pure in-memory mode.
//
// When non-nil, it also rebuilds s.watches from state.ListAll so a
// restarted binary resumes the durable subscriptions with their
// ORIGINALLY-scheduled next_fire_at (the recovered watermark), rather
// than waiting for a Subscribe replay. Recovery is by watermark, never
// by recompute: the rebuilt Watch keeps the persisted NextFireAt so the
// in-flight window still fires.
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
			MessageKind:    r.MessageKind,
			NextFireAt:     r.NextFireAt,
			StartedAt:      r.StartedAt,
			LastFireAt:     lastFire,
		}
		s.logger.Info("sensor-cron.state_recovered",
			"publisher_subscription_id", r.SubscriptionID,
			"next_fire_at", r.NextFireAt.Format(time.RFC3339))
	}
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
						"cron": {"type": "string"}
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
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		// Already active; idempotent. The state-DB row is already present
		// from the prior call, so we skip the UpsertSubscription below.
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

// Unsubscribe removes the watch from the in-memory map. Idempotent.
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
	// Re-find to defend against concurrent Unsubscribe.
	cur, ok := s.watches[w.SubscriptionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	t := now
	cur.LastFireAt = &t
	cur.NextFireAt = sched.Next(cur.NextFireAt) // advance from prior, not now
	// Snapshot the advanced watermark under the lock, then persist it
	// off-lock: the durable next_fire_at must advance with each fire so a
	// restart resumes from the next un-fired window, never a re-fire.
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

// postMessage sends one message envelope to rimsky's generic messages
// endpoint with sender_kind="publisher" + the publisher-subscription
// capability token. Retry-with-backoff is handled by
// `pkg:github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit`.
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
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	url := s.rimskyEndpoint + "/instances/" + w.InstanceID + "/messages"
	res := publisherkit.Send(ctx, s.httpClient, s.logger, nil, publisherkit.Request{
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
