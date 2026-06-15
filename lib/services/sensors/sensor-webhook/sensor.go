// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package main — sensor-webhook bundled sensor. Implements the
// Publisher gRPC protocol; runs an HTTP server on a configured port
// and, per publisher-subscription, registers a path under
// `path_prefix`. Inbound POSTs to a registered route → push a message
// envelope to rimsky's generic messages endpoint.
//
// Spec .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
//	@concept: sensor
//
// Idempotency: optional `idempotency_header` config — when the inbound
// POST carries that header, the sensor (a) deduplicates against the
// prior value per subscription (last-seen wins) and (b) propagates
// the header value as the `Idempotency-Key` on the rimsky message
// POST. Useful for webhook providers that retry — the provider's
// idempotency key suppresses duplicate emissions both locally and at
// rimsky.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
)

// Watch is the in-memory state for one active webhook publisher-
// subscription. Sensor-internal vocabulary stays as "Watch."
type Watch struct {
	SubscriptionID    string
	InstanceID        string
	PathPrefix        string
	IdempotencyHeader string
	TargetNode        string
	MessageType       string

	mu        sync.Mutex
	StartedAt time.Time
	// @constraint: holds the most-recent idempotency-header value seen on
	// this subscription; serveWebhook reads/writes it under w.mu to
	// suppress duplicate emissions when the inbound header repeats.
	LastIdempotency string
}

// SensorService implements genv1.PublisherServer for HTTP webhook
// reception. The chi router is wired once at construction with a
// single catch-all `POST /*` dispatcher (`dispatchWebhook`); per-
// subscription path lookup is then a map check against
// `pathToWatch`. This sidesteps chi's lack of route unregistration —
// `Unsubscribe` deletes the map entry and the dispatcher returns 404
// for stale paths without leaking chi routes.
type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu      sync.Mutex
	watches map[string]*Watch
	// pathToWatch indexes active subscriptions by their `path_prefix`
	// so the catch-all dispatcher can resolve an inbound POST to the
	// right subscription in O(1). Updated transactionally with
	// `watches`.
	pathToWatch    map[string]*Watch
	router         *chi.Mux
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
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

// NewSensorService constructs the service. router is the chi mux the
// binary mounts on the inbound-webhook port; pre-existing routes are
// preserved. The constructor installs a single catch-all `POST /*`
// dispatcher; `Subscribe` and `Unsubscribe` mutate the subscription map
// only.
func NewSensorService(rimskyEndpoint string, router *chi.Mux, log logger) *SensorService {
	s := &SensorService{
		watches:        make(map[string]*Watch),
		pathToWatch:    make(map[string]*Watch),
		router:         router,
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		clock:          time.Now,
		logger:         log,
	}
	// @deliberate: single dispatcher route per service instance — chi has
	// no route-unregistration API, so Subscribe / Unsubscribe mutate only
	// the in-memory map and the chi tree is never touched after this point.
	router.Post("/*", s.dispatchWebhook)
	return s
}

// dispatchWebhook is the single catch-all POST handler the chi router
// is wired to at construction. It resolves the inbound URL.Path to an
// active subscription via `pathToWatch`; misses surface as 404 with an
// operator-visible message.
func (s *SensorService) dispatchWebhook(rw http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	w, ok := s.pathToWatch[req.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.Error(rw, "no active sensor-webhook subscription for this path", http.StatusNotFound)
		return
	}
	s.serveWebhook(w, rw, req)
}

// Capabilities advertises the webhook kind.
func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
			{
				Kind: "webhook",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"path_prefix": {"type": "string"},
						"idempotency_header": {"type": "string"}
					},
					"required": ["path_prefix"]
				}`),
			},
		},
		Protocols: []string{"publisher"},
	}, nil
}

// Subscribe parses the resolved_config and mounts a webhook route
// under `path_prefix`. Per `concept:publisher-subscription`, the
// in-process pathToWatch index drives O(1) dispatch. When a state DB
// is attached, looks up persisted state and pre-populates
// `LastIdempotency` so dedup continues across restarts.
func (s *SensorService) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	if req.GetKind() != "webhook" {
		return nil, fmt.Errorf("sensor-webhook does not support kind %q", req.GetKind())
	}
	var cfg struct {
		PathPrefix        string `json:"path_prefix"`
		IdempotencyHeader string `json:"idempotency_header"`
	}
	if err := json.Unmarshal(req.GetResolvedConfig(), &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved_config: %w", err)
	}
	if cfg.PathPrefix == "" {
		return nil, fmt.Errorf("resolved_config.path_prefix required")
	}
	if !strings.HasPrefix(cfg.PathPrefix, "/") {
		cfg.PathPrefix = "/" + cfg.PathPrefix
	}
	// The legacy "invalidate" default retired with the 2026-06-14
	// message-schema-layer reshape: the runtime side rejects an empty
	// message_type at publisher-subscription mount time, so by the
	// time Subscribe is called here the value is non-empty by
	// construction. Pass through verbatim.
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID:    req.GetPublisherSubscriptionId(),
		InstanceID:        req.GetInstanceId(),
		PathPrefix:        cfg.PathPrefix,
		IdempotencyHeader: cfg.IdempotencyHeader,
		TargetNode:        req.GetTargetNode(),
		MessageType:       messageType,
		StartedAt:         s.clock(),
	}
	// @deliberate: restart-replay — pre-populate the most-recent
	// idempotency key from durable storage before registering the Watch
	// so header-based dedup continues to function across restarts.
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if persisted, err := state.GetSubscription(ctx, w.SubscriptionID); err != nil {
			s.logger.Warn("sensor-webhook.subscribe.state_get_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		} else if persisted != nil {
			w.LastIdempotency = persisted.LastIdempotencyKey
		}
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		s.mu.Unlock()
		// @deliberate: idempotent Subscribe — the state-DB row is already
		// present from the prior call, so skip UpsertSubscription below.
		return &genv1.SubscribeResponse{}, nil
	}
	if existing, taken := s.pathToWatch[w.PathPrefix]; taken {
		s.mu.Unlock()
		return nil, fmt.Errorf("path_prefix %q already bound to subscription %s", w.PathPrefix, existing.SubscriptionID)
	}
	s.watches[w.SubscriptionID] = w
	s.pathToWatch[w.PathPrefix] = w
	s.mu.Unlock()
	if state != nil {
		if err := state.UpsertSubscription(context.Background(), w); err != nil {
			s.logger.Warn("sensor-webhook.subscribe.state_upsert_failed",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		}
	}
	s.logger.Info("sensor-webhook.subscribe",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"path", w.PathPrefix,
		"restored_idempotency", w.LastIdempotency != "")
	return &genv1.SubscribeResponse{}, nil
}

// serveWebhook executes the per-subscription reception logic. Called
// by the catch-all chi dispatcher in `dispatchWebhook` after resolving
// the inbound URL.Path to a live subscription via `pathToWatch`.
//
// Idempotency: when `IdempotencyHeader` is set and the inbound request
// carries that header, suppress emission if the value matches the
// most-recent seen and propagate the value as `Idempotency-Key` on
// the rimsky POST.
func (s *SensorService) serveWebhook(w *Watch, rw http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(rw, "read body", http.StatusBadRequest)
		return
	}
	// inboundIdem suppression — operator-opt-in.
	var inboundIdem string
	if w.IdempotencyHeader != "" {
		inboundIdem = req.Header.Get(w.IdempotencyHeader)
		if inboundIdem != "" {
			w.mu.Lock()
			if w.LastIdempotency == inboundIdem {
				w.mu.Unlock()
				rw.WriteHeader(http.StatusOK)
				return
			}
			w.LastIdempotency = inboundIdem
			w.mu.Unlock()
			// @constraint: read s.state under s.mu — AttachStateDB writes
			// it under the same mu, so a bare read here would race.
			s.mu.Lock()
			state := s.state
			s.mu.Unlock()
			if state != nil {
				if err := state.UpdateLastIdempotency(req.Context(), w.SubscriptionID, inboundIdem); err != nil {
					s.logger.Warn("sensor-webhook.serve.state_update_failed",
						"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
				}
			}
		}
	}
	now := s.clock()
	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"path":        req.URL.Path,
		"method":      req.Method,
	}
	// decoded decode the body as JSON for substitution-friendly
	// observations. Non-JSON surfaces as a string.
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		obs["body"] = decoded
	} else {
		obs["body"] = string(body)
	}
	if inboundIdem != "" {
		obs["idempotency_key"] = inboundIdem
	}
	idemKey := inboundIdem
	if idemKey == "" {
		idemKey = fmt.Sprintf("%s+%d", w.SubscriptionID, now.UnixNano())
	} else {
		idemKey = fmt.Sprintf("%s+%s", w.SubscriptionID, idemKey)
	}
	if err := s.postMessage(req.Context(), w, obs, idemKey); err != nil {
		s.logger.Warn("sensor-webhook.message_post_failed",
			"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		http.Error(rw, "rimsky push failed", http.StatusBadGateway)
		return
	}
	rw.WriteHeader(http.StatusOK)
}

// Unsubscribe removes the subscription from the dispatcher's path
// index.
func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.watches[req.GetPublisherSubscriptionId()]
	if !ok {
		return &genv1.UnsubscribeResponse{}, nil
	}
	delete(s.watches, req.GetPublisherSubscriptionId())
	if w != nil {
		if cur, present := s.pathToWatch[w.PathPrefix]; present && cur == w {
			delete(s.pathToWatch, w.PathPrefix)
		}
	}
	if s.state != nil {
		if err := s.state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
			s.logger.Warn("sensor-webhook.unsubscribe.state_delete_failed",
				"publisher_subscription_id", req.GetPublisherSubscriptionId(), "error", err.Error())
		}
	}
	s.logger.Info("sensor-webhook.unsubscribe", "publisher_subscription_id", req.GetPublisherSubscriptionId())
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
			Kind:                    "webhook",
			TargetNode:              w.TargetNode,
			MessageType:             w.MessageType,
			StartedAt:               timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

// postMessage sends one message envelope to rimsky's generic messages
// endpoint with sender_kind="publisher". Retry-with-backoff is
// handled by `pkg:github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit`.
func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	// `target_node` from the subscription registers routing on the
	// rimsky side; the envelope wire body carries no `target` (the
	// receipt handler has no `target` column to land it on, and the
	// `rimsky_messages.target` column was retired in migration 010 of
	// the 2026-06-14 message-schema-layer reshape).
	envelope := map[string]any{
		"type":                      w.MessageType,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-webhook",
		"sender_kind":               "publisher",
		"publisher_subscription_id": w.SubscriptionID,
	}
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	url := strings.TrimRight(s.rimskyEndpoint, "/") + "/v1/instances/" + w.InstanceID + "/messages"
	res := publisherkit.Send(ctx, s.httpClient, s.logger, nil, publisherkit.Request{
		URL:            url,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		SensorName:     "sensor-webhook",
		SubscriptionID: w.SubscriptionID,
	})
	return res.Err
}
