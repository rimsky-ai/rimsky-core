// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package publisher

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakePublisherClient struct {
	caps *genv1.PublisherCapabilities

	mu               sync.Mutex
	subs             map[string]*genv1.PublisherSubscriptionDescriptor
	omitResolvedConf bool
	omitStartedAt    bool
	wrongMessageType bool
	noOpUnsubscribe  bool
}

func newFakePublisherClient(caps *genv1.PublisherCapabilities) *fakePublisherClient {
	return &fakePublisherClient{caps: caps, subs: map[string]*genv1.PublisherSubscriptionDescriptor{}}
}

func (f *fakePublisherClient) Capabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*genv1.PublisherCapabilities, error) {
	return f.caps, nil
}

func (f *fakePublisherClient) Subscribe(_ context.Context, req *genv1.SubscribeRequest, _ ...grpc.CallOption) (*genv1.SubscribeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	desc := &genv1.PublisherSubscriptionDescriptor{
		PublisherSubscriptionId: req.GetPublisherSubscriptionId(),
		InstanceId:              req.GetInstanceId(),
		Kind:                    req.GetKind(),
		MessageType:             req.GetMessageType(),
		ResolvedConfig:          req.GetResolvedConfig(),
		StartedAt:               timestamppb.New(time.Now()),
	}
	if f.wrongMessageType {
		desc.MessageType = "wrong/message-type"
	}
	if f.omitResolvedConf {
		desc.ResolvedConfig = nil
	}
	if f.omitStartedAt {
		desc.StartedAt = nil
	}
	f.subs[req.GetPublisherSubscriptionId()] = desc
	return &genv1.SubscribeResponse{}, nil
}

func (f *fakePublisherClient) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest, _ ...grpc.CallOption) (*genv1.UnsubscribeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noOpUnsubscribe {
		return &genv1.UnsubscribeResponse{}, nil
	}
	delete(f.subs, req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

func (f *fakePublisherClient) ListSubscriptions(context.Context, *emptypb.Empty, ...grpc.CallOption) (*genv1.ListSubscriptionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(f.subs))
	for _, d := range f.subs {
		out = append(out, d)
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func honestCaps() *genv1.PublisherCapabilities {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{
			{Kind: "cron", ConfigSchema: []byte(`{"type":"object"}`)},
		},
		Protocols: []string{"publisher"},
	}
}

func baseRunOpts() RunOpts {
	return RunOpts{
		Kind:           "cron",
		ResolvedConfig: []byte(`{"cron":"* * * * *"}`),
		SubscriptionID: "sub-1",
		InstanceID:     "instance-1",
		MessageType:    "system/conformance",
	}
}

func findResult(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Name)
	}
	t.Fatalf("result set missing %q (have: %v)", name, names)
	return CheckResult{}
}

func TestRun_Honest_Passes(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	results := Run(context.Background(), c, baseRunOpts())
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("row %q expected PASS, got Err: %v", r.Name, r.Err)
		}
	}
}

func TestCheckCapabilities_MissingPublisherProtocol_Fails(t *testing.T) {
	caps := honestCaps()
	caps.Protocols = []string{"validation"}
	c := newFakePublisherClient(caps)
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "Capabilities")
	if r.Err == nil {
		t.Error("Capabilities expected non-nil Err when protocols does not advertise \"publisher\", got PASS")
	}
}

func TestCheckCapabilities_MalformedConfigSchema_Fails(t *testing.T) {
	caps := honestCaps()
	caps.SupportedKinds[0].ConfigSchema = []byte(`not-json`)
	c := newFakePublisherClient(caps)
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "Capabilities")
	if r.Err == nil {
		t.Error("Capabilities expected non-nil Err for a non-JSON config_schema, got PASS")
	}
}

func TestCheckCapabilities_EmptyConfigSchema_Passes(t *testing.T) {
	caps := honestCaps()
	caps.SupportedKinds[0].ConfigSchema = nil
	c := newFakePublisherClient(caps)
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "Capabilities")
	if r.Err != nil {
		t.Errorf("Capabilities expected PASS for empty (opaque-optional) config_schema, got Err: %v", r.Err)
	}
}

func TestCheckListSubscriptions_WrongMessageType_Fails(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	c.wrongMessageType = true
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "ListSubscriptions")
	if r.Err == nil {
		t.Error("ListSubscriptions expected non-nil Err when message_type does not match the Subscribe request, got PASS")
	}
}

func TestCheckListSubscriptions_MissingResolvedConfig_Fails(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	c.omitResolvedConf = true
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "ListSubscriptions")
	if r.Err == nil {
		t.Error("ListSubscriptions expected non-nil Err when resolved_config is not persisted, got PASS")
	}
}

func TestCheckListSubscriptions_MissingStartedAt_Fails(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	c.omitStartedAt = true
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "ListSubscriptions")
	if r.Err == nil {
		t.Error("ListSubscriptions expected non-nil Err when started_at is unset, got PASS")
	}
}

func TestCheckUnsubscribe_NoOpLeaksSubscription_Fails(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	c.noOpUnsubscribe = true
	results := Run(context.Background(), c, baseRunOpts())
	r := findResult(t, results, "Unsubscribe")
	if r.Err == nil {
		t.Error("Unsubscribe expected non-nil Err when the subscription is still present in ListSubscriptions afterward, got PASS")
	}
}

func TestCheckUnsubscribeIdempotent_NoOpLeaksSubscription_Fails(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	results := Run(context.Background(), c, baseRunOpts())
	if r := findResult(t, results, "Unsubscribe"); r.Err != nil {
		t.Fatalf("Unsubscribe expected PASS as a precondition, got Err: %v", r.Err)
	}

	c.mu.Lock()
	c.noOpUnsubscribe = true
	c.subs[baseRunOpts().SubscriptionID] = &genv1.PublisherSubscriptionDescriptor{
		PublisherSubscriptionId: baseRunOpts().SubscriptionID,
	}
	c.mu.Unlock()

	r := checkUnsubscribeIdempotent(context.Background(), c, baseRunOpts())
	if r.Err == nil {
		t.Error("UnsubscribeIdempotent expected non-nil Err when the subscription is still present in ListSubscriptions afterward, got PASS")
	}
}

func TestCheckUnsubscribe_Honest_RemovesFromListSubscriptions(t *testing.T) {
	c := newFakePublisherClient(honestCaps())
	results := Run(context.Background(), c, baseRunOpts())
	for _, name := range []string{"Unsubscribe", "UnsubscribeIdempotent"} {
		r := findResult(t, results, name)
		if r.Err != nil {
			t.Errorf("%s expected PASS for an honest Unsubscribe, got Err: %v", name, r.Err)
		}
	}
	c.mu.Lock()
	_, stillPresent := c.subs[baseRunOpts().SubscriptionID]
	c.mu.Unlock()
	if stillPresent {
		t.Fatalf("fakePublisherClient bug: subscription still present after honest Unsubscribe")
	}
}
