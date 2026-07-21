// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func signHMAC(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func subscribeWithAuth(t *testing.T, s *SensorService, subID, pathPrefix string, auth map[string]any) {
	t.Helper()
	cfg := map[string]any{"path_prefix": pathPrefix, "auth": auth}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: subID, InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

func countingRimsky(pushed *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(pushed, 1)
		w.WriteHeader(http.StatusCreated)
	}))
}

func TestSubscribe_RefusedWhenAuthOmitted(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/no-auth"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err == nil {
		t.Fatal("expected Subscribe to refuse a subscription with no auth block")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watches) != 0 {
		t.Errorf("refused subscription still mounted a watch: %+v", s.watches)
	}
}

func TestSubscribe_RefusedWhenAuthModeUnknown(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/bad-mode", "auth": map[string]any{"mode": "basic"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err == nil {
		t.Fatal("expected Subscribe to refuse an unknown auth mode")
	}
}

func TestServeWebhook_HMAC_AcceptsSignedRejectsUnsigned(t *testing.T) {
	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	pin := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	secret := "top-secret"
	subscribeWithAuth(t, s, "w1", "/wh/hmac", map[string]any{
		"mode": "hmac", "secret": secret, "timestamp_header": "X-Rimsky-Timestamp",
	})

	srv := httptest.NewServer(router)
	defer srv.Close()
	body := []byte(`{"event":"x"}`)
	ts := strconv.FormatInt(pin.Unix(), 10)

	signed, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/hmac", bytes.NewReader(body))
	signed.Header.Set(defaultSignatureHeader, signHMAC(secret, ts, body))
	signed.Header.Set("X-Rimsky-Timestamp", ts)
	resp, err := http.DefaultClient.Do(signed)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("signed POST status: %d (want 200)", resp.StatusCode)
	}

	missing, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/hmac", bytes.NewReader(body))
	missing.Header.Set("X-Rimsky-Timestamp", ts)
	resp, err = http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned POST status: %d (want 401)", resp.StatusCode)
	}

	wrong, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/hmac", bytes.NewReader(body))
	wrong.Header.Set(defaultSignatureHeader, signHMAC("not-the-secret", ts, body))
	wrong.Header.Set("X-Rimsky-Timestamp", ts)
	resp, err = http.DefaultClient.Do(wrong)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-signature POST status: %d (want 401)", resp.StatusCode)
	}

	tampered, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/hmac", bytes.NewReader([]byte(`{"event":"tampered"}`)))
	tampered.Header.Set(defaultSignatureHeader, signHMAC(secret, ts, body))
	tampered.Header.Set("X-Rimsky-Timestamp", ts)
	resp, err = http.DefaultClient.Do(tampered)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("body-tampered POST status: %d (want 401)", resp.StatusCode)
	}

	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Errorf("upstream pushes: %d (want 1: only the correctly-signed POST forwards)", got)
	}
}

func TestSubscribe_HMAC_RefusedWithoutTimestampHeader(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/no-ts", "auth": map[string]any{"mode": "hmac", "secret": "s"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err == nil {
		t.Fatal("expected Subscribe to refuse hmac mode with no timestamp_header (replay protection mandatory)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watches) != 0 {
		t.Errorf("refused subscription still mounted a watch: %+v", s.watches)
	}
}

func TestServeWebhook_HMAC_ReplayUnderFreshTimestampRejected(t *testing.T) {
	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	var clockMu sync.Mutex
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.clock = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}
	secret := "top-secret"
	subscribeWithAuth(t, s, "w1", "/wh/replay", map[string]any{
		"mode": "hmac", "secret": secret,
		"timestamp_header": "X-Rimsky-Timestamp", "replay_window_seconds": 300,
	})

	srv := httptest.NewServer(router)
	defer srv.Close()
	body := []byte(`{"event":"legit"}`)

	do := func(sig, ts string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/replay", bytes.NewReader(body))
		req.Header.Set(defaultSignatureHeader, sig)
		req.Header.Set("X-Rimsky-Timestamp", ts)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	capturedTS := strconv.FormatInt(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Unix(), 10)
	capturedSig := signHMAC(secret, capturedTS, body)

	if code := do(capturedSig, capturedTS); code != http.StatusOK {
		t.Fatalf("original signed request: %d (want 200)", code)
	}

	advance(10 * time.Minute)
	freshTS := strconv.FormatInt(now.Unix(), 10)
	if code := do(capturedSig, freshTS); code != http.StatusUnauthorized {
		t.Errorf("replay with rewritten fresh timestamp: %d (want 401 — signature covers the timestamp, so a fresh ts breaks it)", code)
	}

	if code := do(capturedSig, capturedTS); code != http.StatusUnauthorized {
		t.Errorf("replay with original timestamp after window: %d (want 401 — stale timestamp outside replay window)", code)
	}

	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Errorf("upstream pushes: %d (want 1: only the original in-window signed POST forwards)", got)
	}
}

func TestServeWebhook_HMAC_ReplayWindowRejectsStale(t *testing.T) {
	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	pin := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	secret := "top-secret"
	subscribeWithAuth(t, s, "w1", "/wh/hmac-ts", map[string]any{
		"mode": "hmac", "secret": secret,
		"timestamp_header": "X-Rimsky-Timestamp", "replay_window_seconds": 300,
	})

	srv := httptest.NewServer(router)
	defer srv.Close()
	body := []byte(`{"event":"x"}`)

	post := func(ts time.Time) int {
		tsStr := strconv.FormatInt(ts.Unix(), 10)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/hmac-ts", bytes.NewReader(body))
		req.Header.Set(defaultSignatureHeader, signHMAC(secret, tsStr, body))
		req.Header.Set("X-Rimsky-Timestamp", tsStr)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(pin); code != http.StatusOK {
		t.Errorf("in-window timestamp status: %d (want 200)", code)
	}
	if code := post(pin.Add(-10 * time.Minute)); code != http.StatusUnauthorized {
		t.Errorf("stale timestamp status: %d (want 401 replay rejection)", code)
	}
	if code := post(pin.Add(10 * time.Minute)); code != http.StatusUnauthorized {
		t.Errorf("future timestamp status: %d (want 401 replay rejection)", code)
	}
	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Errorf("upstream pushes: %d (want 1: only the in-window POST forwards)", got)
	}
}

func TestServeWebhook_SecretHeader_AcceptReject(t *testing.T) {
	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	subscribeWithAuth(t, s, "w1", "/wh/secret", map[string]any{
		"mode": "secret_header", "header": "X-Webhook-Token", "secret": "abc123",
	})

	srv := httptest.NewServer(router)
	defer srv.Close()
	body := []byte(`{"event":"x"}`)

	ok, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/secret", bytes.NewReader(body))
	ok.Header.Set("X-Webhook-Token", "abc123")
	resp, err := http.DefaultClient.Do(ok)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("matching-secret status: %d (want 200)", resp.StatusCode)
	}

	bad, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/secret", bytes.NewReader(body))
	bad.Header.Set("X-Webhook-Token", "wrong")
	resp, err = http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-secret status: %d (want 401)", resp.StatusCode)
	}

	missing, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/secret", bytes.NewReader(body))
	resp, err = http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing-secret status: %d (want 401)", resp.StatusCode)
	}

	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Errorf("upstream pushes: %d (want 1: only the matching-secret POST forwards)", got)
	}
}

func TestServeWebhook_NoneAccepts(t *testing.T) {
	var pushed int32
	rimsky := countingRimsky(&pushed)
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	subscribeWithAuth(t, s, "w1", "/wh/none", map[string]any{"mode": "none"})

	srv := httptest.NewServer(router)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/wh/none", "application/json", bytes.NewReader([]byte(`{"event":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("none-mode status: %d (want 200)", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&pushed); got != 1 {
		t.Errorf("upstream pushes: %d (want 1)", got)
	}
}

func TestServeWebhook_NonUTF8Body_PreservedAsBase64NotCorrupted(t *testing.T) {
	var capturedBody []byte
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	subscribeWithAuth(t, s, "w1", "/wh/binary", map[string]any{"mode": "none"})

	srv := httptest.NewServer(router)
	defer srv.Close()

	binary := []byte{0xff, 0xfe, 0xfd, 0x00, 0x01}
	resp, err := http.Post(srv.URL+"/wh/binary", "application/octet-stream", bytes.NewReader(binary))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	var envelope map[string]any
	if err := json.Unmarshal(capturedBody, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	payload, _ := envelope["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("payload missing: %+v", envelope)
	}
	encoded, ok := payload["body_base64"].(string)
	if !ok {
		t.Fatalf("expected payload.body_base64 for a non-UTF-8 body, got: %+v", payload)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode body_base64: %v", err)
	}
	if !bytes.Equal(decoded, binary) {
		t.Errorf("round-tripped body = %x, want %x (byte-faithful, not corrupted)", decoded, binary)
	}
}

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func TestCapabilities_AdvertisesWebhook(t *testing.T) {
	s := NewSensorService("", chi.NewRouter(), noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "webhook" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "publisher" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestSubscribe_MountsRouteAndForwards(t *testing.T) {
	var (
		obsMu   sync.Mutex
		obsBody []map[string]any
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/instances/") || !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("path: %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		obsMu.Lock()
		obsBody = append(obsBody, body)
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/abc", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(router)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/wh/abc", "application/json", bytes.NewReader([]byte(`{"event":"created","id":42}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: %d", resp.StatusCode)
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	if len(obsBody) != 1 {
		t.Fatalf("messages: %d", len(obsBody))
	}
	body := obsBody[0]
	if sub, _ := body["publisher_subscription_id"].(string); sub == "" {
		t.Errorf("publisher_subscription_id: missing or empty (auth path discriminator)")
	}
	if _, present := body["target"]; present {
		t.Errorf("target unexpectedly present: %v", body["target"])
	}
	payload, ok := body["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload: %+v", body["payload"])
	}
	if payload["path"] != "/wh/abc" {
		t.Errorf("payload.path: %v", payload["path"])
	}
}

func TestDispatchWebhook_RoutesSubPathsUnderDeclaredPrefix(t *testing.T) {
	var (
		obsMu   sync.Mutex
		obsPath []string
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		payload, _ := body["payload"].(map[string]any)
		path, _ := payload["path"].(string)
		obsMu.Lock()
		obsPath = append(obsPath, path)
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	subscribeWithAuth(t, s, "w1", "/wh/github", map[string]any{"mode": "none"})

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/wh/github/push", "application/json", bytes.NewReader([]byte(`{"event":"push"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST under path_prefix status: %d (want 200; routes under the declared prefix must dispatch)", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/wh/github", "application/json", bytes.NewReader([]byte(`{"event":"exact"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST to exact prefix status: %d (want 200)", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/wh/githubx", "application/json", bytes.NewReader([]byte(`{"event":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST to a path merely sharing the prefix's characters (no segment boundary) status: %d (want 404)", resp.StatusCode)
	}

	obsMu.Lock()
	defer obsMu.Unlock()
	if len(obsPath) != 2 || obsPath[0] != "/wh/github/push" || obsPath[1] != "/wh/github" {
		t.Errorf("delivered paths: %v (want [/wh/github/push /wh/github])", obsPath)
	}
}

func TestDispatchWebhook_LongestPrefixWinsAmongOverlappingSubscriptions(t *testing.T) {
	var (
		obsMu   sync.Mutex
		obsSubs []string
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sub, _ := body["publisher_subscription_id"].(string)
		obsMu.Lock()
		obsSubs = append(obsSubs, sub)
		obsMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	subscribeWithAuth(t, s, "w-broad", "/wh", map[string]any{"mode": "none"})
	subscribeWithAuth(t, s, "w-narrow", "/wh/github", map[string]any{"mode": "none"})

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/wh/github/push", "application/json", bytes.NewReader([]byte(`{"event":"push"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	obsMu.Lock()
	defer obsMu.Unlock()
	if len(obsSubs) != 1 || obsSubs[0] != "w-narrow" {
		t.Errorf("routed to %v, want [w-narrow] (longest matching path_prefix wins)", obsSubs)
	}
}

func TestSubscribe_NormalizesLeadingSlash(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	cfg := map[string]any{"path_prefix": "abc/123", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches["w1"].PathPrefix != "/abc/123" {
		t.Errorf("path: %s", s.watches["w1"].PathPrefix)
	}
}

func TestSubscribeThenListSubscriptions_RoundTripsResolvedConfig(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	raw, _ := json.Marshal(map[string]any{"path_prefix": "/wh/abc", "auth": map[string]any{"mode": "none"}})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 1 {
		t.Fatalf("subscriptions: %+v", resp.Subscriptions)
	}
	if got := resp.Subscriptions[0].GetResolvedConfig(); string(got) != string(raw) {
		t.Errorf("resolved_config=%s, want %s", got, raw)
	}
}

func TestSubscribe_RejectsBadKind(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{Kind: "cron"})
	if err == nil {
		t.Fatal("expected error for non-webhook kind")
	}
}

func TestIdempotencyHeader_Deduplicates(t *testing.T) {
	var (
		mu     sync.Mutex
		pushed int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		pushed++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	cfg := map[string]any{"path_prefix": "/wh/idem", "idempotency_header": "X-Idem", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/idem", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", "k1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	mu.Lock()
	if pushed != 1 {
		t.Errorf("pushed: %d (want 1; idempotency dedup)", pushed)
	}
	mu.Unlock()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/idem", bytes.NewReader([]byte(`{"event":"y"}`)))
	req.Header.Set("X-Idem", "k2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	mu.Lock()
	if pushed != 2 {
		t.Errorf("pushed after new key: %d (want 2)", pushed)
	}
	mu.Unlock()
}

func TestIdempotencyHeader_FailedPostAllowsRetry(t *testing.T) {
	var (
		mu      sync.Mutex
		failing = true
		pushed  int
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/retry", "idempotency_header": "X-Idem", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/retry", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", "k1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(); code != http.StatusBadGateway {
		t.Errorf("failed delivery status: %d (want 502)", code)
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	if code := post(); code != http.StatusOK {
		t.Errorf("retry status: %d (want 200)", code)
	}
	if code := post(); code != http.StatusOK {
		t.Errorf("duplicate status: %d (want 200)", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if pushed != 1 {
		t.Errorf("pushed: %d (want exactly 1: retry delivers, duplicate dedups)", pushed)
	}
}

func TestServeWebhook_PermanentRejectionDropsDelivery_AdvancesWatermarkAndReturns200(t *testing.T) {
	var attempts int32
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/drop", "idempotency_header": "X-Idem", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/drop", bytes.NewReader([]byte(`{"event":"x"}`)))
		req.Header.Set("X-Idem", "k1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(); code != http.StatusOK {
		t.Fatalf("rejected delivery status: %d (want 200; a permanently rejected observation is "+
			"consumed, so the webhook source must not be told to retry)", code)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after rejected delivery: %d (want 1; permanent 4xx must not be retried within Send)", got)
	}
	s.mu.Lock()
	w := s.watches["w1"]
	s.mu.Unlock()
	w.mu.Lock()
	lastIdem := w.LastIdempotency
	w.mu.Unlock()
	if lastIdem != "k1" {
		t.Fatalf("LastIdempotency after permanent rejection: %q, want %q — the watermark must "+
			"advance exactly as on success", lastIdem, "k1")
	}

	if code := post(); code != http.StatusOK {
		t.Fatalf("duplicate delivery status: %d (want 200)", code)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after duplicate delivery: %d (want still 1; the dropped observation "+
			"must be deduplicated, not re-posted)", got)
	}
}

func TestIdempotencyHeader_ComposesSubIDWithIncomingHeader(t *testing.T) {
	var gotIdem string
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdem = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/idem-compose", "idempotency_header": "X-Idem", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/wh/idem-compose", bytes.NewReader([]byte(`{"event":"x"}`)))
	req.Header.Set("X-Idem", "incoming-42")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if want := "w1+incoming-42"; gotIdem != want {
		t.Errorf("Idempotency-Key = %q, want %q (sub id + incoming idempotency header)", gotIdem, want)
	}
}

func TestServeWebhook_RejectsOversizedBody(t *testing.T) {
	var readMu sync.Mutex
	upstreamReads := 0
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		readMu.Lock()
		upstreamReads++
		readMu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	router := chi.NewRouter()
	s := NewSensorService(rimsky.URL, router, noopLogger{})
	cfg := map[string]any{"path_prefix": "/wh/size", "auth": map[string]any{"mode": "none"}}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "webhook", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	oversized := bytes.Repeat([]byte("a"), int(maxWebhookBodyBytes)+1)
	resp, err := http.Post(srv.URL+"/wh/size", "application/octet-stream", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized status: %d (want 413)", resp.StatusCode)
	}
	readMu.Lock()
	if upstreamReads != 0 {
		t.Errorf("upstream pushes on oversized body: %d (want 0; must reject before forwarding)", upstreamReads)
	}
	readMu.Unlock()

	normal := []byte(`{"event":"ok"}`)
	okResp, err := http.Post(srv.URL+"/wh/size", "application/json", bytes.NewReader(normal))
	if err != nil {
		t.Fatal(err)
	}
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Errorf("normal-body status: %d (want 200)", okResp.StatusCode)
	}
	readMu.Lock()
	defer readMu.Unlock()
	if upstreamReads != 1 {
		t.Errorf("upstream pushes on normal body: %d (want 1)", upstreamReads)
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	s.mu.Lock()
	s.watches["w1"] = &Watch{SubscriptionID: "w1"}
	s.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := s.Unsubscribe(context.Background(), &genv1.UnsubscribeRequest{PublisherSubscriptionId: "w1"}); err != nil {
			t.Fatalf("unsubscribe[%d]: %v", i, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.watches) != 0 {
		t.Errorf("watches: %+v", s.watches)
	}
}

func TestListSubscriptions(t *testing.T) {
	router := chi.NewRouter()
	s := NewSensorService("", router, noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s.mu.Lock()
	s.watches["w1"] = &Watch{SubscriptionID: "w1", InstanceID: "i1", StartedAt: s.clock()}
	s.watches["w2"] = &Watch{SubscriptionID: "w2", InstanceID: "i2", StartedAt: s.clock()}
	s.mu.Unlock()
	resp, err := s.ListSubscriptions(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Subscriptions) != 2 {
		t.Errorf("subscriptions: %+v", resp.Subscriptions)
	}
}
