// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
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
	MessageType    string

	LastPollAt    time.Time
	WatermarkName string
	WatermarkTime time.Time
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
		interval = d
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
		MessageType:    messageType,
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
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		return &genv1.SubscribeResponse{}, nil
	}
	s.watches[w.SubscriptionID] = w
	if state != nil {
		if err := state.UpsertSubscription(context.Background(), w); err != nil {
			s.logger.Warn("sensor-object-store.subscribe.state_upsert_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
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
		isNew := false
		switch cur.WatermarkField {
		case "last_modified":
			isNew = o.LastModified.After(cur.WatermarkTime)
		default:
			isNew = o.Name > cur.WatermarkName
		}
		if !isNew {
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
		idemKey := fmt.Sprintf("%s+%s", w.SubscriptionID, o.ETag)
		if o.ETag == "" {
			idemKey = fmt.Sprintf("%s+%s", w.SubscriptionID, o.Name)
		}
		if err := s.postMessage(ctx, w, obs, idemKey); err != nil {
			s.logger.Warn("sensor-object-store.message_post_failed",
				"publisher_subscription_id", w.SubscriptionID, "object_name", o.Name, "error", err.Error())
			return
		}

		s.mu.Lock()
		cur, exists = s.watches[w.SubscriptionID]
		if !exists {
			s.mu.Unlock()
			return
		}
		switch watermarkField {
		case "last_modified":
			cur.WatermarkTime = o.LastModified
		default:
			cur.WatermarkName = o.Name
		}
		s.mu.Unlock()
		if state != nil {
			var err error
			switch watermarkField {
			case "last_modified":
				err = state.UpdateWatermarkTime(ctx, w.SubscriptionID, o.LastModified)
			default:
				err = state.UpdateWatermarkName(ctx, w.SubscriptionID, o.Name)
			}
			if err != nil {
				s.logger.Warn("sensor-object-store.poll.state_update_failed",
					"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			}
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
		"sender":                    "sensor-object-store",
		"publisher_subscription_id": w.SubscriptionID,
	}
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	messageURL := strings.TrimRight(s.rimskyEndpoint, "/") + "/v1/instances/" + url.PathEscape(w.InstanceID) + "/messages"
	res := publisherkit.Send(ctx, s.httpClient, s.logger, nil, publisherkit.Request{
		URL:            messageURL,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		PublisherName:  "sensor-object-store",
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
