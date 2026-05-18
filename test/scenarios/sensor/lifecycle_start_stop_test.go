// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lifecycle_start_stop_test pins the Publisher service-protocol
// lifecycle: Subscribe creates an in-memory publisher-subscription,
// ListSubscriptions enumerates active subscriptions, Unsubscribe
// removes one. Publishers are required to be idempotent on retries.
// The scenario pins the contract using a minimal in-process publisher.
package sensor

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// fixturePublisher is a minimal Publisher impl used to exercise the
// lifecycle contract. Mirrors the bundled sensor binaries' shape at
// helper level (per-kind firing is out of scope).
type fixturePublisher struct {
	genv1.UnimplementedPublisherServer
	mu   sync.Mutex
	subs map[string]subscription
}

type subscription struct {
	SubscriptionID string
	InstanceID     string
	Kind           string
	TargetNode     string
	MessageKind    string
	StartedAt      time.Time
}

func newFixturePublisher() *fixturePublisher {
	return &fixturePublisher{subs: make(map[string]subscription)}
}

func (s *fixturePublisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: "cron"}, {Kind: "http"}},
		Protocols:      []string{"publisher"},
	}, nil
}

func (s *fixturePublisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[req.GetPublisherSubscriptionId()]; ok {
		return &genv1.SubscribeResponse{}, nil
	}
	s.subs[req.GetPublisherSubscriptionId()] = subscription{
		SubscriptionID: req.GetPublisherSubscriptionId(),
		InstanceID:     req.GetInstanceId(),
		Kind:           req.GetKind(),
		TargetNode:     req.GetTargetNode(),
		MessageKind:    req.GetMessageKind(),
		StartedAt:      time.Now(),
	}
	return &genv1.SubscribeResponse{}, nil
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
			PublisherSubscriptionId: sub.SubscriptionID,
			InstanceId:              sub.InstanceID,
			Kind:                    sub.Kind,
			TargetNode:              sub.TargetNode,
			MessageKind:             sub.MessageKind,
			StartedAt:               timestamppb.New(sub.StartedAt),
		})
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func TestLifecycleStartStop_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newFixturePublisher()
	ctx := context.Background()
	if _, err := s.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron",
		TargetNode: "tick", MessageKind: "invalidate",
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	resp, err := s.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(resp.GetSubscriptions()) != 1 {
		t.Errorf("ListSubscriptions: expected 1 subscription, got %d", len(resp.GetSubscriptions()))
	}
	if _, err := s.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: "w1"}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	resp, err = s.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(resp.GetSubscriptions()) != 0 {
		t.Errorf("ListSubscriptions after unsubscribe: expected 0, got %d", len(resp.GetSubscriptions()))
	}
}

func TestLifecycleStartStop_Idempotent(t *testing.T) {
	t.Parallel()
	s := newFixturePublisher()
	ctx := context.Background()
	if _, err := s.Subscribe(ctx, &genv1.SubscribeRequest{PublisherSubscriptionId: "w1", Kind: "cron"}); err != nil {
		t.Fatalf("Subscribe #1: %v", err)
	}
	if _, err := s.Subscribe(ctx, &genv1.SubscribeRequest{PublisherSubscriptionId: "w1", Kind: "cron"}); err != nil {
		t.Errorf("Subscribe #2 (idempotent): %v", err)
	}
	if _, err := s.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: "w1"}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, err := s.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: "w1"}); err != nil {
		t.Errorf("Unsubscribe idempotent: %v", err)
	}
}
