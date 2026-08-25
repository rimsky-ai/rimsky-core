// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorauth"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/sensorpub"
)

const maxWebhookBodyBytes int64 = 1 << 20

const (
	hmacSignaturePrefix = "sha256="
)

type Watch struct {
	SubscriptionID    string
	InstanceID        string
	PathPrefix        string
	IdempotencyHeader string
	MessageType       string
	ResolvedConfig    []byte
	Auth              *sensorauth.AuthConfig

	mu              sync.Mutex
	StartedAt       time.Time
	LastIdempotency string
}

type SensorService struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	watches        map[string]*Watch
	pathToWatch    map[string]*Watch
	watermarkCache map[string]string
	router         *chi.Mux
	rimskyEndpoint string
	httpClient     *http.Client
	clock          func() time.Time
	logger         logger
	state          *stateDB
}

func (s *SensorService) AttachStateDB(state *stateDB) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	if state == nil {
		return
	}
	rows, err := state.ListWatermarks(context.Background())
	if err != nil {
		s.logger.Warn("SENSORWEBHOOK.STATEDBATTACH.LISTFAILED", "error", err.Error())
		return
	}
	s.mu.Lock()
	for _, r := range rows {
		s.watermarkCache[r.SubscriptionID] = r.LastIdempotency
	}
	restored := len(rows)
	s.mu.Unlock()
	s.logger.Info("SENSORWEBHOOK.WATERMARKS.RESTORED", "count", restored)
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

func NewSensorService(rimskyEndpoint string, router *chi.Mux, log logger) *SensorService {
	s := &SensorService{
		watches:        make(map[string]*Watch),
		pathToWatch:    make(map[string]*Watch),
		watermarkCache: make(map[string]string),
		router:         router,
		rimskyEndpoint: rimskyEndpoint,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		clock:          time.Now,
		logger:         log,
	}
	router.Post("/*", s.dispatchWebhook)
	return s
}

func (s *SensorService) dispatchWebhook(rw http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	w := matchPathPrefix(s.pathToWatch, req.URL.Path)
	s.mu.Unlock()
	if w == nil {
		http.Error(rw, "no active sensor-webhook subscription for this path", http.StatusNotFound)
		return
	}
	s.serveWebhook(w, rw, req)
}

func matchPathPrefix(watches map[string]*Watch, path string) *Watch {
	var best *Watch
	bestLen := -1
	for prefix, w := range watches {
		if !pathUnderPrefix(path, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			best = w
			bestLen = len(prefix)
		}
	}
	return best
}

func pathUnderPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if strings.HasSuffix(prefix, "/") {
		return true
	}
	return path[len(prefix)] == '/'
}

func (s *SensorService) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
			{
				Kind: "webhook",
				ConfigSchema: []byte(`{
					"type": "object",
					"properties": {
						"path_prefix": {"type": "string"},
						"idempotency_header": {"type": "string"},
						"auth": {
							"type": "object",
							"properties": {
								"mode": {"type": "string", "enum": ["hmac", "secret_header", "none"]},
								"secret": {"type": "string"},
								"signature_header": {"type": "string"},
								"timestamp_header": {"type": "string"},
								"replay_window_seconds": {"type": "integer", "minimum": 0},
								"header": {"type": "string"}
							},
							"required": ["mode"]
						}
					},
					"required": ["path_prefix", "auth"]
				}`),
			},
		},
		Protocols: []string{"publisher"},
	}, nil
}

func (s *SensorService) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	if req.GetKind() != "webhook" {
		return nil, fmt.Errorf("sensor-webhook does not support kind %q", req.GetKind())
	}
	var cfg struct {
		PathPrefix        string                 `json:"path_prefix"`
		IdempotencyHeader string                 `json:"idempotency_header"`
		Auth              *sensorauth.AuthConfig `json:"auth"`
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
	if err := sensorauth.ValidateInbound(cfg.Auth); err != nil {
		return nil, err
	}
	messageType := req.GetMessageType()
	w := &Watch{
		SubscriptionID:    req.GetPublisherSubscriptionId(),
		InstanceID:        req.GetInstanceId(),
		PathPrefix:        cfg.PathPrefix,
		IdempotencyHeader: cfg.IdempotencyHeader,
		MessageType:       messageType,
		ResolvedConfig:    append([]byte(nil), req.GetResolvedConfig()...),
		Auth:              cfg.Auth,
		StartedAt:         s.clock(),
	}
	s.mu.Lock()
	state := s.state
	cachedWatermark, hasCachedWatermark := s.watermarkCache[w.SubscriptionID]
	s.mu.Unlock()
	switch {
	case hasCachedWatermark:
		w.LastIdempotency = cachedWatermark
	case state != nil:
		if key, err := state.GetLastIdempotency(ctx, w.SubscriptionID); err != nil {
			s.logger.Warn("SENSORWEBHOOK.SUBSCRIBE.STATEGETFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
		} else {
			w.LastIdempotency = key
		}
	}
	s.mu.Lock()
	if _, exists := s.watches[w.SubscriptionID]; exists {
		s.mu.Unlock()
		return &genv1.SubscribeResponse{}, nil
	}
	if existing, taken := s.pathToWatch[w.PathPrefix]; taken {
		s.mu.Unlock()
		return nil, fmt.Errorf("path_prefix %q already bound to subscription %s", w.PathPrefix, existing.SubscriptionID)
	}
	s.watches[w.SubscriptionID] = w
	s.pathToWatch[w.PathPrefix] = w
	delete(s.watermarkCache, w.SubscriptionID)
	s.mu.Unlock()
	s.logger.Info("SENSORWEBHOOK.SUBSCRIPTION.MOUNTED",
		"publisher_subscription_id", w.SubscriptionID,
		"instance_id", w.InstanceID,
		"path", w.PathPrefix,
		"restored_idempotency", w.LastIdempotency != "")
	return &genv1.SubscribeResponse{}, nil
}

func (s *SensorService) serveWebhook(w *Watch, rw http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, maxWebhookBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(rw, "request body exceeds size limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(rw, "read body", http.StatusBadRequest)
		return
	}
	if code, authErr := s.authenticate(w, req, body); authErr != nil {
		s.logger.Warn("SENSORWEBHOOK.REQUEST.UNAUTHORIZED",
			"publisher_subscription_id", w.SubscriptionID, "error", authErr.Error())
		http.Error(rw, http.StatusText(code), code)
		return
	}
	var inboundIdem string
	if w.IdempotencyHeader != "" {
		inboundIdem = req.Header.Get(w.IdempotencyHeader)
		if inboundIdem != "" {
			w.mu.Lock()
			seen := w.LastIdempotency == inboundIdem
			w.mu.Unlock()
			if seen {
				rw.WriteHeader(http.StatusOK)
				return
			}
		}
	}
	now := s.clock()
	obs := map[string]any{
		"observed_at": now.UTC().Format(time.RFC3339),
		"path":        req.URL.Path,
		"method":      req.Method,
	}
	var decoded any
	switch {
	case json.Unmarshal(body, &decoded) == nil:
		obs["body"] = decoded
	case utf8.Valid(body):
		obs["body"] = string(body)
	default:
		obs["body_base64"] = base64.StdEncoding.EncodeToString(body)
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
		var rejected *publisherkit.RejectedError
		if !errors.As(err, &rejected) {
			s.logger.Warn("SENSORWEBHOOK.MESSAGE.POSTFAILED",
				"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			http.Error(rw, "rimsky push failed", http.StatusBadGateway)
			return
		}
		s.logger.Error("SENSORWEBHOOK.MESSAGE.REJECTED", "detail", "the message was dropped",
			"publisher_subscription_id", w.SubscriptionID, "status", rejected.Status, "error", err.Error())
	}
	if inboundIdem != "" {
		w.mu.Lock()
		w.LastIdempotency = inboundIdem
		w.mu.Unlock()
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		if state != nil {
			if err := state.UpdateLastIdempotency(req.Context(), w.SubscriptionID, inboundIdem); err != nil {
				s.logger.Warn("SENSORWEBHOOK.SERVE.STATEUPDATEFAILED",
					"publisher_subscription_id", w.SubscriptionID, "error", err.Error())
			}
		}
	}
	rw.WriteHeader(http.StatusOK)
}

func (s *SensorService) authenticate(w *Watch, req *http.Request, body []byte) (int, error) {
	if w.Auth == nil {
		return http.StatusUnauthorized, errors.New("no auth configured for subscription")
	}
	switch w.Auth.Mode {
	case sensorauth.ModeNone:
		return http.StatusOK, nil
	case sensorauth.ModeSecretHeader:
		provided := req.Header.Get(w.Auth.Header)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(w.Auth.Secret)) != 1 {
			return http.StatusUnauthorized, errors.New("secret header mismatch")
		}
		return http.StatusOK, nil
	case sensorauth.ModeHMAC:
		return s.authenticateHMAC(w.Auth, req, body)
	default:
		return http.StatusUnauthorized, fmt.Errorf("unknown auth mode %q", w.Auth.Mode)
	}
}

func (s *SensorService) authenticateHMAC(auth *sensorauth.AuthConfig, req *http.Request, body []byte) (int, error) {
	provided := req.Header.Get(auth.SignatureHeader)
	if provided == "" {
		return http.StatusUnauthorized, errors.New("missing signature header")
	}
	tsHeader := req.Header.Get(auth.TimestampHeader)
	if code, err := s.verifyTimestamp(auth, tsHeader); err != nil {
		return code, err
	}
	mac := hmac.New(sha256.New, []byte(auth.Secret))
	mac.Write([]byte(tsHeader))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := hmacSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return http.StatusUnauthorized, errors.New("signature mismatch")
	}
	return http.StatusOK, nil
}

func (s *SensorService) verifyTimestamp(auth *sensorauth.AuthConfig, tsHeader string) (int, error) {
	if tsHeader == "" {
		return http.StatusUnauthorized, errors.New("missing timestamp header")
	}
	secs, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return http.StatusUnauthorized, fmt.Errorf("invalid timestamp header: %w", err)
	}
	window := auth.ReplayWindowSeconds
	if window <= 0 {
		window = sensorauth.DefaultReplayWindowSeconds
	}
	delta := s.clock().Sub(time.Unix(secs, 0))
	if delta < 0 {
		delta = -delta
	}
	if delta > time.Duration(window)*time.Second {
		return http.StatusUnauthorized, errors.New("timestamp outside replay window")
	}
	return http.StatusOK, nil
}

func (s *SensorService) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	w, ok := s.watches[req.GetPublisherSubscriptionId()]
	if !ok {
		s.mu.Unlock()
		return &genv1.UnsubscribeResponse{}, nil
	}
	delete(s.watches, req.GetPublisherSubscriptionId())
	if w != nil {
		if cur, present := s.pathToWatch[w.PathPrefix]; present && cur == w {
			delete(s.pathToWatch, w.PathPrefix)
		}
	}
	state := s.state
	s.mu.Unlock()
	if state != nil {
		if err := state.DeleteSubscription(context.Background(), req.GetPublisherSubscriptionId()); err != nil {
			s.logger.Warn("SENSORWEBHOOK.UNSUBSCRIBE.STATEDELETEFAILED",
				"publisher_subscription_id", req.GetPublisherSubscriptionId(), "error", err.Error())
		}
	}
	s.logger.Info("SENSORWEBHOOK.SUBSCRIPTION.STOPPED", "publisher_subscription_id", req.GetPublisherSubscriptionId())
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
			Kind:                    "webhook",
			MessageType:             w.MessageType,
			ResolvedConfig:          w.ResolvedConfig,
			StartedAt:               timestamppb.New(w.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func (s *SensorService) postMessage(ctx context.Context, w *Watch, payload map[string]any, idempotencyKey string) error {
	return sensorpub.PostMessage(ctx, s.httpClient, s.logger, s.rimskyEndpoint, "sensor-webhook",
		w.MessageType, w.InstanceID, w.SubscriptionID, payload, idempotencyKey)
}
