// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorpub"
)

type ObjectMeta struct {
	Name         string
	LastModified time.Time
	Size         int64
	ETag         string
}

type ObjectLister interface {
	List(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
}

type Watch struct {
	SubscriptionID string
	InstanceID     string
	Backend        string
	Bucket         string
	Prefix         string
	PollInterval   time.Duration
	WatermarkField string
	SettleWindow   time.Duration
	MessageType    string
	ResolvedConfig []byte

	LastPollAt    time.Time
	WatermarkName string
	WatermarkTime time.Time
	SeenNames     map[string]struct{}
}

type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	listers        map[string]ObjectLister
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
		s.logger.Warn("sensor-object-store.attach_state_db.list_failed", "error", err.Error())
		return
	}
	for _, r := range rows {
		interval, err := time.ParseDuration(r.PollInterval)
		if err != nil || interval <= 0 {
			interval = 30 * time.Second
		}
		w := &Watch{
			SubscriptionID: r.SubscriptionID,
			InstanceID:     r.InstanceID,
			Backend:        r.Backend,
			Bucket:         r.Bucket,
			Prefix:         r.Prefix,
			PollInterval:   interval,
			WatermarkField: r.WatermarkField,
			MessageType:    r.MessageType,
			WatermarkName:  r.WatermarkName,
		}
		if r.WatermarkTime != nil {
			w.WatermarkTime = *r.WatermarkTime
		}
		names, err := state.ListSeenNames(context.Background(), r.SubscriptionID)
		if err != nil {
			s.logger.Warn("sensor-object-store.attach_state_db.seen_names_failed",
				"publisher_subscription_id", r.SubscriptionID, "error", err.Error())
		} else {
			w.SeenNames = seenNameSet(names)
		}
		s.watches[r.SubscriptionID] = w
		s.logger.Info("sensor-object-store.state_recovered",
			"publisher_subscription_id", r.SubscriptionID,
			"backend", r.Backend,
			"bucket", r.Bucket,
			"prefix", r.Prefix,
			"watermark_field", r.WatermarkField,
			"watermark_name", r.WatermarkName,
			"poll_interval", interval.String())
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
		listers:        make(map[string]ObjectLister),
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		clock:          time.Now,
		logger:         log,
		tickInterval:   time.Second,
	}
}

func (s *SensorService) SetBackend(name string, l ObjectLister) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listers[name] = l
}

func (s *SensorService) registeredBackends() []string {
	out := make([]string, 0, len(s.listers))
	for name := range s.listers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	s.mu.Lock()
	backends := s.registeredBackends()
	s.mu.Unlock()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"backend":         map[string]any{"type": "string", "enum": backends},
			"bucket":          map[string]any{"type": "string"},
			"prefix":          map[string]any{"type": "string"},
			"poll_interval":   map[string]any{"type": "string"},
			"settle_window":   map[string]any{"type": "string"},
			"watermark_field": map[string]any{"type": "string", "enum": []string{"name", "last_modified"}},
		},
		"required": []string{"backend", "bucket"},
	}
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal config schema: %w", err)
	}
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
			{
				Kind:         "object-store",
				ConfigSchema: schemaBytes,
			},
		},
		Protocols: []string{"publisher"},
	}, nil
}

func (s *SensorService) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	if req.GetKind() != "object-store" {
		return nil, fmt.Errorf("sensor-object-store does not support kind %q", req.GetKind())
	}
	var cfg struct {
		Backend        string `json:"backend"`
		Bucket         string `json:"bucket"`
		Prefix         string `json:"prefix"`
		PollInterval   string `json:"poll_interval"`
		SettleWindow   string `json:"settle_window"`
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
	s.mu.Lock()
	_, backendRegistered := s.listers[cfg.Backend]
	registered := s.registeredBackends()
	s.mu.Unlock()
	if !backendRegistered {
		return nil, fmt.Errorf("resolved_config.backend %q is not serviceable by this build (registered backends: %s)", cfg.Backend, strings.Join(registered, "|"))
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
		if d <= 0 {
			return nil, fmt.Errorf("poll_interval %q must be positive", cfg.PollInterval)
		}
		interval = d
	}
	settle := time.Duration(0)
	if cfg.SettleWindow != "" {
		d, err := time.ParseDuration(cfg.SettleWindow)
		if err != nil {
			return nil, fmt.Errorf("invalid settle_window %q: %w", cfg.SettleWindow, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("settle_window %q must not be negative", cfg.SettleWindow)
		}
		settle = d
	}
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		Backend:        cfg.Backend,
		Bucket:         cfg.Bucket,
		Prefix:         cfg.Prefix,
		PollInterval:   interval,
		WatermarkField: cfg.WatermarkField,
		SettleWindow:   settle,
		MessageType:    messageType,
		ResolvedConfig: append([]byte(nil), req.GetResolvedConfig()...),
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if persisted, err := state.GetSubscription(ctx, w.SubscriptionID); err != nil {
			s.logger.Warn("sensor-object-store.subscribe.state_get_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		} else if persisted != nil {
			w.WatermarkName = persisted.WatermarkName
			if persisted.WatermarkTime != nil {
				w.WatermarkTime = *persisted.WatermarkTime
			}
			if names, err := state.ListSeenNames(ctx, w.SubscriptionID); err != nil {
				s.logger.Warn("sensor-object-store.subscribe.seen_names_failed",
					"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			} else {
				w.SeenNames = seenNameSet(names)
			}
		}
	}
	s.mu.Lock()
	if cur, exists := s.watches[w.SubscriptionID]; exists {
		resetWatermark := cur.Bucket != w.Bucket || cur.Prefix != w.Prefix || cur.WatermarkField != w.WatermarkField
		cur.InstanceID = w.InstanceID
		cur.Backend = w.Backend
		cur.Bucket = w.Bucket
		cur.Prefix = w.Prefix
		cur.PollInterval = w.PollInterval
		cur.WatermarkField = w.WatermarkField
		cur.MessageType = w.MessageType
		cur.ResolvedConfig = w.ResolvedConfig
		if resetWatermark {
			cur.WatermarkName = ""
			cur.WatermarkTime = time.Time{}
			cur.SeenNames = nil
		}
		updated := *cur
		s.mu.Unlock()
		if state != nil {
			if err := state.UpsertSubscription(context.Background(), &updated); err != nil {
				s.logger.Warn("sensor-object-store.resubscribe.state_upsert_failed",
					"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			}
			if resetWatermark {
				if err := state.ResetWatermark(context.Background(), w.SubscriptionID); err != nil {
					s.logger.Warn("sensor-object-store.resubscribe.watermark_reset_failed",
						"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
				}
			}
		}
		s.logger.Info("sensor-object-store.resubscribe_config_updated",
			"publisher_subscription_id", w.SubscriptionID,
			"backend", cfg.Backend, "bucket", cfg.Bucket, "prefix", cfg.Prefix,
			"poll_interval", interval.String(), "watermark_field", cfg.WatermarkField,
			"watermark_reset", resetWatermark)
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	s.mu.Unlock()
	if state != nil {
		if err := state.UpsertSubscription(ctx, w); err != nil {
			s.mu.Lock()
			if s.watches[w.SubscriptionID] == w {
				delete(s.watches, w.SubscriptionID)
			}
			s.mu.Unlock()
			s.logger.Warn("sensor-object-store.subscribe.state_upsert_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			return nil, fmt.Errorf("sensor-object-store: persist subscription %s: %w", w.SubscriptionID, err)
		}
	}
	s.logger.Info("sensor-object-store.subscribe",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"backend", cfg.Backend,
		"bucket", cfg.Bucket,
		"prefix", cfg.Prefix,
		"poll_interval", interval.String(),
		"watermark_field", cfg.WatermarkField,
		"restored_watermark", w.WatermarkName != "" || !w.WatermarkTime.IsZero())
	return &genv1.SubscribeResponse{}, nil
}

func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[req.GetPublisherSubscriptionId()]; ok {
		delete(s.watches, req.GetPublisherSubscriptionId())
		s.logger.Info("sensor-object-store.unsubscribe", "publisher_subscription_id", req.GetPublisherSubscriptionId())
		if s.state != nil {
			if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
				s.logger.Warn("sensor-object-store.unsubscribe.state_delete_failed",
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
			Kind:                    "object-store",
			MessageType:             w.MessageType,
			ResolvedConfig:          w.ResolvedConfig,
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
	lister, ok := s.listers[w.Backend]
	state := s.state
	s.mu.Unlock()
	if !ok {
		s.logger.Warn("sensor-object-store.no_backend",
			"publisher_subscription_id", w.SubscriptionID, "backend", w.Backend)
		return
	}
	objs, err := lister.List(ctx, w.Bucket, w.Prefix)
	if err != nil {
		s.logger.Warn("sensor-object-store.list_failed",
			"publisher_subscription_id", w.SubscriptionID, "bucket", w.Bucket, "prefix", w.Prefix, "error", err.Error())
		return
	}
	if state != nil {
		if err := state.TouchLastPoll(ctx, w.SubscriptionID, now); err != nil {
			s.logger.Warn("sensor-object-store.poll.touch_last_poll_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}

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
		cur, exists := s.watches[w.SubscriptionID]
		if !exists {
			s.mu.Unlock()
			return
		}
		if cur.SettleWindow > 0 && now.Sub(o.LastModified) < cur.SettleWindow {
			s.mu.Unlock()
			continue
		}
		if !isNewObject(cur, o) {
			s.mu.Unlock()
			continue
		}
		state := s.state
		watermarkField := cur.WatermarkField
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
		idemKey := fmt.Sprintf("%s+%s+%s", w.SubscriptionID, o.Name, o.ETag)
		if err := s.postMessage(ctx, w, obs, idemKey); err != nil {
			var rejected *publisherkit.RejectedError
			if !errors.As(err, &rejected) {
				s.logger.Warn("sensor-object-store.message_post_failed",
					"publisher_subscription_id", w.SubscriptionID, "object_name", o.Name, "error", err.Error())
				return
			}
			s.logger.Error("sensor-object-store.message_rejected_dropped",
				"publisher_subscription_id", w.SubscriptionID, "object_name", o.Name, "status", rejected.Status, "error", err.Error())
		}

		s.mu.Lock()
		cur, exists = s.watches[w.SubscriptionID]
		if !exists {
			s.mu.Unlock()
			return
		}
		watermarkAdvanced := markObjectSeen(cur, o)
		s.mu.Unlock()
		if state != nil {
			var err error
			switch watermarkField {
			case "last_modified":
				if watermarkAdvanced {
					err = state.AdvanceWatermarkTime(ctx, w.SubscriptionID, o.LastModified, o.Name)
				} else {
					err = state.AddSeenName(ctx, w.SubscriptionID, o.Name)
				}
			default:
				err = state.AddSeenName(ctx, w.SubscriptionID, o.Name)
				if err == nil {
					err = state.UpdateWatermarkName(ctx, w.SubscriptionID, o.Name)
				}
			}
			if err != nil {
				s.logger.Warn("sensor-object-store.poll.state_update_failed",
					"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			}
		}
	}
}

func isNewObject(w *Watch, o ObjectMeta) bool {
	switch w.WatermarkField {
	case "last_modified":
		switch {
		case o.LastModified.After(w.WatermarkTime):
			return true
		case o.LastModified.Equal(w.WatermarkTime):
			_, seen := w.SeenNames[o.Name]
			return !seen
		default:
			return false
		}
	default:
		_, seen := w.SeenNames[o.Name]
		return !seen
	}
}

func markObjectSeen(w *Watch, o ObjectMeta) bool {
	switch w.WatermarkField {
	case "last_modified":
		if o.LastModified.After(w.WatermarkTime) {
			w.WatermarkTime = o.LastModified
			w.SeenNames = map[string]struct{}{o.Name: {}}
			return true
		}
		if w.SeenNames == nil {
			w.SeenNames = make(map[string]struct{})
		}
		w.SeenNames[o.Name] = struct{}{}
		return false
	default:
		if w.SeenNames == nil {
			w.SeenNames = make(map[string]struct{})
		}
		w.SeenNames[o.Name] = struct{}{}
		w.WatermarkName = o.Name
		return false
	}
}

func seenNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	return sensorpub.PostMessage(ctx, s.httpClient, s.logger, s.rimskyEndpoint, "sensor-object-store",
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
