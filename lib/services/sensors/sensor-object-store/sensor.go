// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package main — sensor-object-store bundled sensor. Implements the
// Publisher gRPC protocol; per publisher-subscription, polls an
// object-store bucket+prefix on a fixed interval and emits one message
// envelope per new object (or new object version, per
// `watermark_field`).
//
// Spec .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
//	@concept: sensor
//
// Backends: the sensor is structured around a narrow `ObjectLister`
// interface (List(prefix) -> []ObjectMeta) so a single poll loop drives
// every backend. The default bundled image registers ONLY the in-memory
// lister ("memory"), so it advertises and accepts only "memory" — it
// rejects s3/gcs/azure at Subscribe rather than no-op'ing on them at
// poll time. S3 / GCS / Azure are not implemented here (deliberately
// cut to keep the cloud SDKs out of the default build); a production
// build registers its own listers via SetBackend before Run, after
// which Capabilities advertises and Subscribe accepts exactly the
// registered set.
//
// Watermarking: per-subscription high-watermark is the maximum value
// seen for the configured `watermark_field` (one of `name`,
// `last_modified`). New observations are objects whose watermark value
// strictly exceeds the prior watermark. Idempotency: re-listing the
// same set without any new object produces zero observations.
package main

import (
	"context"
	"encoding/json"
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
)

// ObjectMeta is the per-object snapshot the lister returns. Inert in
// the sensor — we only project (name, last_modified, size, etag) into
// the observation body.
type ObjectMeta struct {
	Name         string
	LastModified time.Time
	Size         int64
	ETag         string
}

// ObjectLister is the narrow per-backend interface. Production wires
// S3 / GCS / Azure implementations; tests inject a fake via SetBackend.
type ObjectLister interface {
	List(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
}

// Watch is the in-memory state for one active object-store publisher-
// subscription. Sensor-internal vocabulary stays as "Watch."
type Watch struct {
	SubscriptionID string
	InstanceID     string
	Backend        string // a registered backend name; "memory" in the default build
	Bucket         string
	Prefix         string
	PollInterval   time.Duration
	WatermarkField string // "name" | "last_modified"
	TargetNode     string
	MessageKind    string

	LastPollAt    time.Time
	WatermarkName string    // when WatermarkField == "name"
	WatermarkTime time.Time // when WatermarkField == "last_modified"
}

// SensorService implements genv1.PublisherServer for object-store
// polling.
//
// `listers` is keyed by backend name. The default build registers only
// "memory"; production builds register additional backends (e.g. s3,
// gcs, azure) at startup via SetBackend. Subscribe and Capabilities are
// both driven off this map so the sensor only ever accepts/advertises
// backends it can actually service.
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
	// state is the optional persistence layer. nil → in-memory mode.
	state *stateDB
}

// AttachStateDB binds an optional persistence layer.
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

// NewSensorService constructs the service with an empty lister registry.
// Callers register backends via SetBackend.
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

// SetBackend registers an ObjectLister under the given backend name.
//
// This is the sole extension point for object-store backends. The
// default bundled image registers only the in-memory backend
// ("memory"), so it advertises and accepts only "memory". A production
// build that needs s3/gcs/azure constructs its own binary, registers
// the corresponding listers via SetBackend before calling Run, and the
// sensor then advertises (Capabilities) and accepts (Subscribe) exactly
// the set of registered backends — keeping the cloud SDKs out of the
// default build. Used by tests (memory fake) and by main() at startup.
func (s *SensorService) SetBackend(name string, l ObjectLister) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listers[name] = l
}

// registeredBackends returns the sorted set of backend names this build
// can service. Callers must hold s.mu. Used by Subscribe (to name the
// serviceable set in a rejection) and Capabilities (to advertise only
// backends that are actually wired), so the sensor never accepts or
// advertises a backend it could only no-op on at poll time.
func (s *SensorService) registeredBackends() []string {
	out := make([]string, 0, len(s.listers))
	for name := range s.listers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Capabilities advertises the object-store kind. The `backend` enum is
// built from the listers actually registered on this build (J3) so the
// sensor never advertises a backend it cannot service — the default
// image advertises only ["memory"]; a production build that registered
// s3/gcs/azure listers advertises those too.
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

// Subscribe parses resolved_config and registers the publisher-
// subscription. When a state DB is attached, looks up persisted state
// and pre-populates the watermark cursor so restart-replay does not
// re-emit objects emitted before the restart.
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
	// Validate the backend against the listers actually registered on this
	// build (J3). The default image services only "memory"; a production
	// build that wires s3/gcs/azure listers via SetBackend before Run
	// auto-accepts them here. Rejecting unregistered backends keeps the
	// sensor from accepting a subscription it could only no-op on at poll
	// time.
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
	messageKind := req.GetMessageKind()
	if messageKind == "" {
		messageKind = "invalidate"
	}
	w := &Watch{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		Backend:        cfg.Backend,
		Bucket:         cfg.Bucket,
		Prefix:         cfg.Prefix,
		PollInterval:   interval,
		WatermarkField: cfg.WatermarkField,
		TargetNode:     req.GetTargetNode(),
		MessageKind:    messageKind,
	}
	// Restart-replay: load the persisted watermark cursor before
	// registering the Watch so the first poll skips already-emitted
	// objects.
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
		// Idempotent Subscribe: the state-DB row is already present from
		// the prior call, so we skip the UpsertSubscription below.
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

// Unsubscribe removes the publisher-subscription. Idempotent.
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

// ListSubscriptions enumerates active publisher-subscriptions.
func (s *SensorService) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: w.SubscriptionID,
			InstanceId:              w.InstanceID,
			Kind:                    "object-store",
			TargetNode:              w.TargetNode,
			MessageKind:             w.MessageKind,
			StartedAt:               timestamppb.New(s.clock()),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

// Tick polls due subscriptions. One message envelope per new object.
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

// pollOne lists the bucket+prefix, filters by the watermark, and
// pushes one message envelope per new object.
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

	// Sort by watermark field ascending so we emit observations in order
	// AND advance the watermark deterministically.
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
		// Advance watermark BEFORE post so a post failure does not
		// re-emit. The plan's pre-resolved decision favors at-most-once.
		switch cur.WatermarkField {
		case "last_modified":
			cur.WatermarkTime = o.LastModified
		default:
			cur.WatermarkName = o.Name
		}
		state := s.state
		watermarkField := cur.WatermarkField
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
		}
	}
}

// postMessage sends one message envelope to rimsky's generic messages
// endpoint with sender_kind="publisher". Retry-with-backoff is
// handled by `pkg:github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit`.
func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"kind":                      w.MessageKind,
		"target":                    w.TargetNode,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-object-store",
		"sender_kind":               "publisher",
		"publisher_subscription_id": w.SubscriptionID,
	}
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	url := strings.TrimRight(s.rimskyEndpoint, "/") + "/instances/" + w.InstanceID + "/messages"
	res := publisherkit.Send(ctx, s.httpClient, s.logger, nil, publisherkit.Request{
		URL:            url,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		SensorName:     "sensor-object-store",
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
