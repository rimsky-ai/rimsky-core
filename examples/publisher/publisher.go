// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"sync"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const exampleKind = "example"

type Publisher struct {
	genv1.UnimplementedPublisherServer

	mu                     sync.Mutex
	subs                   map[string]*genv1.PublisherSubscriptionDescriptor
	subscribeCalls         int
	unsubscribeCalls       int
	listSubscriptionsCalls int
}

func newPublisher() *Publisher {
	return &Publisher{subs: map[string]*genv1.PublisherSubscriptionDescriptor{}}
}

type CallCounts struct {
	Subscribe         int
	Unsubscribe       int
	ListSubscriptions int
}

func (p *Publisher) Calls() CallCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return CallCounts{
		Subscribe:         p.subscribeCalls,
		Unsubscribe:       p.unsubscribeCalls,
		ListSubscriptions: p.listSubscriptionsCalls,
	}
}

func (p *Publisher) SubscriptionIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.subs))
	for id := range p.subs {
		out = append(out, id)
	}
	return out
}

func (p *Publisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: exampleKind}},
	}, nil
}

func (p *Publisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribeCalls++
	p.subs[req.GetPublisherSubscriptionId()] = &genv1.PublisherSubscriptionDescriptor{
		PublisherSubscriptionId: req.GetPublisherSubscriptionId(),
		InstanceId:              req.GetInstanceId(),
		Kind:                    req.GetKind(),
		ResolvedConfig:          req.GetResolvedConfig(),
		TargetNode:              req.GetTargetNode(),
		MessageType:             req.GetMessageType(),
	}
	return &genv1.SubscribeResponse{}, nil
}

func (p *Publisher) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unsubscribeCalls++
	delete(p.subs, req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

func (p *Publisher) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listSubscriptionsCalls++
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(p.subs))
	for _, s := range p.subs {
		out = append(out, s)
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}
