// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify Publisher: it manages
// subscriptions (Subscribe / Unsubscribe / ListSubscriptions) for one publisher
// "kind" and advertises that kind via Capabilities. Sensors (cron / http /
// object-store / webhook) are publishers.
//
// Emitting a message into a running instance is a separate REST call —
// POST /instances/{id}/messages — not part of this RPC surface, which only
// manages subscriptions. Copy this directory, rename the module in go.mod, and
// replace the in-memory registry with whatever watches your source (a cron
// timer, an HTTP poller, an object-store notifier) and POSTs when it fires.
package main

import (
	"context"
	"sync"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const exampleKind = "example"

// Publisher implements genv1.PublisherServer with an in-memory subscription
// registry. Embedding the generated Unimplemented server keeps it
// forward-compatible.
type Publisher struct {
	genv1.UnimplementedPublisherServer

	mu   sync.Mutex
	subs map[string]*genv1.PublisherSubscriptionDescriptor
}

func newPublisher() *Publisher {
	return &Publisher{subs: map[string]*genv1.PublisherSubscriptionDescriptor{}}
}

// Capabilities is the startup handshake: advertise the publisher kinds this
// service handles. rimsky validates a template's `publishers:` kinds against it.
func (p *Publisher) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: exampleKind}},
	}, nil
}

// Subscribe records a per-instance subscription. A real publisher also starts
// watching the source described by req.GetResolvedConfig() here.
func (p *Publisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs[req.GetPublisherSubscriptionId()] = &genv1.PublisherSubscriptionDescriptor{
		PublisherSubscriptionId: req.GetPublisherSubscriptionId(),
		InstanceId:              req.GetInstanceId(),
		Kind:                    req.GetKind(),
		ResolvedConfig:          req.GetResolvedConfig(),
		TargetNode:              req.GetTargetNode(),
		MessageKind:             req.GetMessageKind(),
	}
	return &genv1.SubscribeResponse{}, nil
}

// Unsubscribe stops and forgets a subscription.
func (p *Publisher) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs, req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

// ListSubscriptions reports the live subscriptions; rimsky reconciles its own
// view against this (e.g. on restart).
func (p *Publisher) ListSubscriptions(_ context.Context, _ *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*genv1.PublisherSubscriptionDescriptor, 0, len(p.subs))
	for _, s := range p.subs {
		out = append(out, s)
	}
	return &genv1.ListSubscriptionsResponse{Subscriptions: out}, nil
}
