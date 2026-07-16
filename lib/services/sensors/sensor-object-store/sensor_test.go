// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

func TestCapabilities_AdvertisesObjectStore(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 || caps.SupportedKinds[0].Kind != "object-store" {
		t.Errorf("kinds: %+v", caps.SupportedKinds)
	}
	if len(caps.Protocols) != 1 || caps.Protocols[0] != "publisher" {
		t.Errorf("protocols: %+v", caps.Protocols)
	}
}

func TestSubscribe_RegistersInMemory(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.SetBackend("memory", NewMemoryLister())
	cfg := map[string]any{
		"backend":       "memory",
		"bucket":        "test-bucket",
		"prefix":        "events/",
		"poll_interval": "10s",
	}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
		MessageType: "invalidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w, ok := s.watches["w1"]
	s.mu.Unlock()
	if !ok || w.Bucket != "test-bucket" || w.WatermarkField != "name" {
		t.Errorf("subscription: %+v", w)
	}
	if w.MessageType != "invalidate" {
		t.Errorf("routing: %+v", w)
	}
}

func TestSubscribe_RejectsBadBackend(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	cfg := map[string]any{"backend": "ftp", "bucket": "b"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "object-store", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestObjectStoreRejectsUnregisteredBackend(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.SetBackend("memory", NewMemoryLister())

	s3cfg, _ := json.Marshal(map[string]any{"backend": "s3", "bucket": "b"})
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w-s3", Kind: "object-store", ResolvedConfig: s3cfg,
	})
	if err == nil {
		t.Fatal("expected Subscribe to reject unregistered backend s3")
	}
	if !strings.Contains(err.Error(), "s3") {
		t.Errorf("rejection error must name the unserviceable backend s3: %v", err)
	}

	caps, err := s.Capabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.SupportedKinds) != 1 {
		t.Fatalf("kinds: %+v", caps.SupportedKinds)
	}
	var schema struct {
		Properties struct {
			Backend struct {
				Enum []string `json:"enum"`
			} `json:"backend"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(caps.SupportedKinds[0].ConfigSchema, &schema); err != nil {
		t.Fatalf("decode config schema: %v", err)
	}
	got := append([]string(nil), schema.Properties.Backend.Enum...)
	if len(got) != 1 || got[0] != "memory" {
		t.Errorf("backend enum: %v (want exactly [memory])", got)
	}

	memcfg, _ := json.Marshal(map[string]any{"backend": "memory", "bucket": "b"})
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w-mem", Kind: "object-store", ResolvedConfig: memcfg,
	}); err != nil {
		t.Fatalf("memory Subscribe must succeed: %v", err)
	}
}

func TestSubscribe_RejectsBadWatermark(t *testing.T) {
	s := NewSensorService("", noopLogger{})
	s.SetBackend("memory", NewMemoryLister())
	cfg := map[string]any{"backend": "memory", "bucket": "b", "watermark_field": "lol"}
	raw, _ := json.Marshal(cfg)
	_, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", Kind: "object-store", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected error for unknown watermark_field")
	}
}

func TestTick_EmitsOneMessagePerNewObject(t *testing.T) {
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

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	lister := NewMemoryLister()
	s.SetBackend("memory", lister)
	lister.Put("test-bucket", ObjectMeta{Name: "events/a.json", LastModified: pin.Add(-1 * time.Hour), Size: 10, ETag: "etag-a"})
	lister.Put("test-bucket", ObjectMeta{Name: "events/b.json", LastModified: pin.Add(-30 * time.Minute), Size: 20, ETag: "etag-b"})

	cfg := map[string]any{"backend": "memory", "bucket": "test-bucket", "prefix": "events/", "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("first tick messages: %d (want 2)", len(obsBody))
	}
	if sub, _ := obsBody[0]["publisher_subscription_id"].(string); sub == "" {
		t.Errorf("publisher_subscription_id: missing or empty (auth path discriminator)")
	}
	obsMu.Unlock()

	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 2 {
		t.Errorf("steady state messages: %d (want 2)", len(obsBody))
	}
	obsMu.Unlock()

	lister.Put("test-bucket", ObjectMeta{Name: "events/c.json", LastModified: pin, Size: 30, ETag: "etag-c"})
	s.clock = func() time.Time { return pin.Add(30 * time.Second) }
	s.Tick(context.Background())
	obsMu.Lock()
	if len(obsBody) != 3 {
		t.Errorf("post-add messages: %d (want 3)", len(obsBody))
	}
	obsMu.Unlock()
}

func TestTick_LastModifiedWatermark(t *testing.T) {
	var pushed int
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed++
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	lister := NewMemoryLister()
	s.SetBackend("memory", lister)
	lister.Put("test-bucket", ObjectMeta{Name: "z.json", LastModified: pin.Add(-2 * time.Hour)})
	lister.Put("test-bucket", ObjectMeta{Name: "a.json", LastModified: pin.Add(-1 * time.Hour)})

	cfg := map[string]any{
		"backend":         "memory",
		"bucket":          "test-bucket",
		"poll_interval":   "10s",
		"watermark_field": "last_modified",
	}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
	}); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if pushed != 2 {
		t.Errorf("pushed: %d (want 2)", pushed)
	}
	lister.Put("test-bucket", ObjectMeta{Name: "b.json", LastModified: pin.Add(-30 * time.Minute)})
	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	if pushed != 3 {
		t.Errorf("pushed: %d (want 3)", pushed)
	}
}

func TestTick_FailedPostDoesNotAdvanceWatermark(t *testing.T) {
	var (
		mu        sync.Mutex
		failing   = true
		delivered []string
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fail := failing
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		payload, _ := body["payload"].(map[string]any)
		name, _ := payload["object_name"].(string)
		mu.Lock()
		delivered = append(delivered, name)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	pin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return pin }
	lister := NewMemoryLister()
	s.SetBackend("memory", lister)
	lister.Put("test-bucket", ObjectMeta{Name: "events/a.json", LastModified: pin.Add(-1 * time.Hour), Size: 10, ETag: "etag-a"})
	lister.Put("test-bucket", ObjectMeta{Name: "events/b.json", LastModified: pin.Add(-30 * time.Minute), Size: 20, ETag: "etag-b"})

	cfg := map[string]any{"backend": "memory", "bucket": "test-bucket", "prefix": "events/", "poll_interval": "10s"}
	raw, _ := json.Marshal(cfg)
	if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "object-store", ResolvedConfig: raw,
		MessageType: "invalidate",
	}); err != nil {
		t.Fatal(err)
	}

	s.Tick(context.Background())
	s.mu.Lock()
	watermark := s.watches["w1"].WatermarkName
	s.mu.Unlock()
	if watermark != "" {
		t.Errorf("watermark advanced past failed delivery: %q", watermark)
	}
	mu.Lock()
	if len(delivered) != 0 {
		t.Errorf("delivered during failure: %v", delivered)
	}
	failing = false
	mu.Unlock()

	s.clock = func() time.Time { return pin.Add(15 * time.Second) }
	s.Tick(context.Background())
	mu.Lock()
	got := append([]string(nil), delivered...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "events/a.json" || got[1] != "events/b.json" {
		t.Errorf("delivered after recovery: %v (want [events/a.json events/b.json])", got)
	}
	s.mu.Lock()
	watermark = s.watches["w1"].WatermarkName
	s.mu.Unlock()
	if watermark != "events/b.json" {
		t.Errorf("watermark after recovery: %q (want events/b.json)", watermark)
	}
}

func TestPostMessage_EscapesInstanceIDPathSegment(t *testing.T) {
	const hostileInstanceID = "../../../v1/auth/whoami?x=/..%2e"
	gotSegment := make(chan string, 1)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := r.URL.EscapedPath()
		prefix, suffix := "/v1/instances/", "/messages"
		if !strings.HasPrefix(escaped, prefix) || !strings.HasSuffix(escaped, suffix) {
			t.Errorf("escaped path escaped its route shape: %q", escaped)
		}
		segment := strings.TrimSuffix(strings.TrimPrefix(escaped, prefix), suffix)
		gotSegment <- segment
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, noopLogger{})
	w := &Watch{SubscriptionID: "w1", InstanceID: hostileInstanceID, MessageType: "invalidate"}
	if err := s.postMessage(context.Background(), w, map[string]any{"k": "v"}, "idem-1"); err != nil {
		t.Fatalf("postMessage: %v", err)
	}

	segment := <-gotSegment
	if strings.Contains(segment, "/") {
		t.Errorf("instance-id segment broke out of its path position: %q", segment)
	}
	if decoded, err := url.PathUnescape(segment); err != nil || decoded != hostileInstanceID {
		t.Errorf("segment %q did not round-trip to the raw instance id (decoded=%q err=%v)", segment, decoded, err)
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	s := NewSensorService("", noopLogger{})
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
