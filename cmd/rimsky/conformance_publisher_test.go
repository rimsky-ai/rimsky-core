// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pubconformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/publisher"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestPublisherConformance_FixtureCron(t *testing.T) {
	receiver := pubconformance.NewMessageReceiver()
	receiverEndpoint, stopReceiver := startReceiver(t, receiver)
	t.Cleanup(stopReceiver)

	fixture := newFixturePublisher(receiverEndpoint)
	pubEndpoint, stopPub := startPublisherServer(t, fixture)
	t.Cleanup(stopPub)

	conn, err := grpc.NewClient(pubEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewPublisherClient(conn)

	opts := pubconformance.RunOpts{
		Kind:            "cron",
		ResolvedConfig:  []byte(`{"cron":"* * * * *"}`),
		MessageReceiver: receiver,
		SubscriptionID:  "self-test-subscription",
		InstanceID:      "self-test-instance",
		MessageType:     "system/conformance",
	}
	results := pubconformance.Run(context.Background(), client, opts)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	wantNames := []string{
		"Capabilities", "Subscribe", "ListSubscriptions", "SubscribeIdempotent",
		"MessagePush", "Unsubscribe", "UnsubscribeIdempotent",
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Name] = true
	}
	for _, name := range wantNames {
		if !seen[name] {
			t.Errorf("expected check %q to run, did not see it", name)
		}
	}
}

func startReceiver(t *testing.T, r *pubconformance.MessageReceiver) (endpoint string, teardown func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances/", func(w http.ResponseWriter, req *http.Request) {
		u, _ := url.Parse(req.URL.Path)
		parts := splitNonEmpty(u.Path, '/')
		var instanceID string
		if len(parts) >= 4 && parts[0] == "v1" && parts[1] == "instances" && parts[3] == "messages" {
			instanceID = parts[2]
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		if instanceID != "" {
			r.NoteMessage(instanceID)
		}
		w.WriteHeader(http.StatusCreated)
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("receiver listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(lis) }()
	endpoint = "http://" + lis.Addr().String()
	return endpoint, func() {
		_ = srv.Close()
	}
}

func splitNonEmpty(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

type fixturePublisher struct {
	genv1.UnimplementedPublisherServer
	mu             sync.Mutex
	rimskyEndpoint string
	subs           map[string]*fixtureSub
	httpClient     *http.Client
}

type fixtureSub struct {
	subscriptionID string
	instanceID     string
	kind           string
	messageType    string
	resolvedConfig []byte
	startedAt      time.Time
}

func newFixturePublisher(rimskyEndpoint string) *fixturePublisher {
	return &fixturePublisher{
		rimskyEndpoint: rimskyEndpoint,
		subs:           map[string]*fixtureSub{},
		httpClient:     &http.Client{},
	}
}

func (s *fixturePublisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: "cron"}},
		Protocols:      []string{"publisher"},
	}, nil
}

func (s *fixturePublisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	if req.GetKind() != "cron" {
		return nil, fmt.Errorf("fixture publisher: unsupported kind %q", req.GetKind())
	}
	sub, fresh := s.recordSubscription(req)
	if !fresh {
		return &genv1.SubscribeResponse{}, nil
	}
	if err := s.push(sub); err != nil {
		return nil, fmt.Errorf("fixture publisher: push for subscription %q: %w", sub.subscriptionID, err)
	}
	return &genv1.SubscribeResponse{}, nil
}

func (s *fixturePublisher) recordSubscription(req *genv1.SubscribeRequest) (*fixtureSub, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.subs[req.GetPublisherSubscriptionId()]; ok {
		return existing, false
	}
	sub := &fixtureSub{
		subscriptionID: req.GetPublisherSubscriptionId(),
		instanceID:     req.GetInstanceId(),
		kind:           req.GetKind(),
		messageType:    req.GetMessageType(),
		resolvedConfig: append([]byte(nil), req.GetResolvedConfig()...),
		startedAt:      time.Now(),
	}
	s.subs[sub.subscriptionID] = sub
	return sub, true
}

func (s *fixturePublisher) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

func (s *fixturePublisher) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(s.subs))
	for _, sub := range s.subs {
		out = append(out, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: sub.subscriptionID,
			InstanceId:              sub.instanceID,
			Kind:                    sub.kind,
			MessageType:             sub.messageType,
			ResolvedConfig:          sub.resolvedConfig,
			StartedAt:               timestamppb.New(sub.startedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func (s *fixturePublisher) push(sub *fixtureSub) error {
	payload, err := json.Marshal(map[string]any{"observed_at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{
		"type":                      sub.messageType,
		"payload":                   json.RawMessage(payload),
		"sender":                    "fixture-publisher",
		"publisher_subscription_id": sub.subscriptionID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.rimskyEndpoint+"/v1/instances/"+sub.instanceID+"/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", sub.subscriptionID+"+1")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("message push returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func startPublisherServer(t *testing.T, srv *fixturePublisher) (endpoint string, teardown func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("publisher listen: %v", err)
	}
	g := grpc.NewServer()
	genv1.RegisterPublisherServer(g, srv)
	done := make(chan struct{})
	go func() {
		_ = g.Serve(lis)
		close(done)
	}()
	return lis.Addr().String(), func() {
		g.GracefulStop()
		<-done
	}
}
