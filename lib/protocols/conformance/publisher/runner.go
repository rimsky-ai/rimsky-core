// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package publisher is the importable form of the Publisher protocol
// conformance suite. The `rimsky conformance publisher` subcommand is a
// thin wrapper that dials the endpoint and invokes Run; tests can
// invoke Run directly against an in-process or testcontainers-hosted
// publisher.

package publisher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// CheckResult is one row of conformance output.
type CheckResult struct {
	Name string
	Err  error
}

// RunOpts bundles the per-run inputs.
type RunOpts struct {
	// Kind names the publisher kind (cron, http, ...). Required.
	Kind string
	// ResolvedConfig is the per-subscription config bytes the publisher
	// parses in Subscribe. Publisher-kind-specific.
	ResolvedConfig []byte
	// MessageReceiver, when non-nil, is consulted by the message-push
	// check. The receiver's WaitForMessage method blocks up to the
	// supplied timeout for the publisher to POST to
	// `/instances/{instance_id}/messages`. Out-of-process conformance
	// runs don't set this (the publisher's HTTP target is baked at
	// startup, outside the conformance binary's control); the
	// in-process self-test sets it.
	MessageReceiver *MessageReceiver
	// SubscriptionID is the publisher_subscription_id passed to
	// Subscribe / Unsubscribe. Defaults to a stable conformance value
	// when empty.
	SubscriptionID string
	// InstanceID echoed on Subscribe. Required when the publisher
	// pushes to `/instances/{instance_id}/messages`.
	InstanceID string
	// TargetNode is the routing target_node passed inline on Subscribe.
	// Defaults to a stable conformance value when empty.
	TargetNode string
	// MessageType is the routing message_type passed inline on
	// Subscribe. Defaults to "system/conformance" when empty (the legacy
	// "invalidate" default retired in the 2026-06-14 message-schema-
	// layer reshape; a real publisher's message_type is now validated
	// against the target instance's template registry).
	MessageType string
	// MessageTimeout bounds the wait for the message-push check.
	// Defaults to 5s.
	MessageTimeout time.Duration
}

// Run drives the lifecycle + (optional) message push checks against
// the supplied Publisher client.
func Run(ctx context.Context, c genv1.PublisherClient, opts RunOpts) []CheckResult {
	if opts.SubscriptionID == "" {
		opts.SubscriptionID = "conformance-subscription"
	}
	if opts.InstanceID == "" {
		opts.InstanceID = "conformance-instance"
	}
	if opts.TargetNode == "" {
		opts.TargetNode = "tick"
	}
	if opts.MessageType == "" {
		opts.MessageType = "system/conformance"
	}
	if opts.MessageTimeout == 0 {
		opts.MessageTimeout = 5 * time.Second
	}
	results := make([]CheckResult, 0, 6)
	results = append(results, checkCapabilities(ctx, c, opts.Kind))
	results = append(results, checkSubscribe(ctx, c, opts))
	results = append(results, checkListSubscriptions(ctx, c, opts))
	results = append(results, checkSubscribeIdempotent(ctx, c, opts))
	if opts.MessageReceiver != nil {
		results = append(results, checkMessagePush(c, opts))
	}
	results = append(results, checkUnsubscribe(ctx, c, opts))
	results = append(results, checkUnsubscribeIdempotent(ctx, c, opts))
	return results
}

// checkCapabilities probes the Capabilities RPC and asserts the
// requested kind is in the supported set.
func checkCapabilities(ctx context.Context, c genv1.PublisherClient, kind string) CheckResult {
	caps, err := c.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return CheckResult{Name: "Capabilities", Err: err}
	}
	if len(caps.GetSupportedKinds()) == 0 {
		return CheckResult{Name: "Capabilities", Err: fmt.Errorf("supported_kinds is empty")}
	}
	found := false
	for _, k := range caps.GetSupportedKinds() {
		if k.GetKind() == kind {
			found = true
			break
		}
	}
	if !found {
		return CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("kind %q not advertised in supported_kinds", kind),
		}
	}
	return CheckResult{Name: "Capabilities"}
}

// checkSubscribe fires Subscribe with the supplied opts.
func checkSubscribe(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: opts.SubscriptionID,
		InstanceId:              opts.InstanceID,
		Kind:                    opts.Kind,
		ResolvedConfig:          opts.ResolvedConfig,
		TargetNode:              opts.TargetNode,
		MessageType:             opts.MessageType,
	}); err != nil {
		return CheckResult{Name: "Subscribe", Err: err}
	}
	return CheckResult{Name: "Subscribe"}
}

// checkListSubscriptions pins that the just-started subscription
// appears in ListSubscriptions.
func checkListSubscriptions(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	resp, err := c.ListSubscriptions(ctx, &emptypb.Empty{})
	if err != nil {
		return CheckResult{Name: "ListSubscriptions", Err: err}
	}
	for _, w := range resp.GetSubscriptions() {
		if w.GetPublisherSubscriptionId() == opts.SubscriptionID {
			if w.GetInstanceId() != opts.InstanceID {
				return CheckResult{
					Name: "ListSubscriptions",
					Err:  fmt.Errorf("subscription %q instance_id %q != expected %q", opts.SubscriptionID, w.GetInstanceId(), opts.InstanceID),
				}
			}
			if w.GetKind() != opts.Kind {
				return CheckResult{
					Name: "ListSubscriptions",
					Err:  fmt.Errorf("subscription %q kind %q != expected %q", opts.SubscriptionID, w.GetKind(), opts.Kind),
				}
			}
			return CheckResult{Name: "ListSubscriptions"}
		}
	}
	return CheckResult{
		Name: "ListSubscriptions",
		Err:  fmt.Errorf("subscription %q not present in ListSubscriptions after Subscribe", opts.SubscriptionID),
	}
}

// checkSubscribeIdempotent re-fires Subscribe and asserts no error.
// Publishers are expected to treat duplicate Subscribe on a live
// subscription_id as a no-op.
func checkSubscribeIdempotent(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: opts.SubscriptionID,
		InstanceId:              opts.InstanceID,
		Kind:                    opts.Kind,
		ResolvedConfig:          opts.ResolvedConfig,
		TargetNode:              opts.TargetNode,
		MessageType:             opts.MessageType,
	}); err != nil {
		return CheckResult{Name: "SubscribeIdempotent", Err: err}
	}
	return CheckResult{Name: "SubscribeIdempotent"}
}

// checkMessagePush blocks for a message envelope arriving at the
// in-process receiver. Skipped when no receiver is configured.
func checkMessagePush(c genv1.PublisherClient, opts RunOpts) CheckResult {
	ok := opts.MessageReceiver.WaitForMessage(opts.InstanceID, opts.MessageTimeout)
	if !ok {
		return CheckResult{
			Name: "MessagePush",
			Err: fmt.Errorf("no message arrived at the receiver within %s for instance_id=%q",
				opts.MessageTimeout, opts.InstanceID),
		}
	}
	_ = c // @constraint: arrival is enough; client unused beyond lifetime tracking
	return CheckResult{Name: "MessagePush"}
}

// checkUnsubscribe fires Unsubscribe.
func checkUnsubscribe(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: opts.SubscriptionID}); err != nil {
		return CheckResult{Name: "Unsubscribe", Err: err}
	}
	return CheckResult{Name: "Unsubscribe"}
}

// checkUnsubscribeIdempotent re-fires Unsubscribe and asserts no error.
func checkUnsubscribeIdempotent(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: opts.SubscriptionID}); err != nil {
		return CheckResult{Name: "UnsubscribeIdempotent", Err: err}
	}
	return CheckResult{Name: "UnsubscribeIdempotent"}
}

// MessageReceiver is the small in-process HTTP receiver the
// conformance harness uses to assert messages land. Publishers POST
// to ${endpoint}/instances/{instance_id}/messages; the receiver
// records the arrival keyed by instance_id and unblocks waiters.
type MessageReceiver struct {
	mu          sync.Mutex
	cond        *sync.Cond
	observedSet map[string]bool
}

// NewMessageReceiver constructs an empty receiver.
func NewMessageReceiver() *MessageReceiver {
	r := &MessageReceiver{observedSet: make(map[string]bool)}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// NoteMessage records that instance_id received a message and wakes
// any waiter.
func (r *MessageReceiver) NoteMessage(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observedSet[instanceID] = true
	r.cond.Broadcast()
}

// WaitForMessage blocks up to timeout for a message arrival for
// instanceID. Returns true on arrival, false on timeout.
//
// Implementation: sync.Cond doesn't support a native deadline, so a
// single time.AfterFunc broadcasts the cond at the deadline. The
// timer is stopped on normal completion so the watchdog goroutine
// does not outlive WaitForMessage's stack frame.
func (r *MessageReceiver) WaitForMessage(instanceID string, timeout time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.observedSet[instanceID] {
		return true
	}
	if timeout <= 0 {
		return false
	}
	timer := time.AfterFunc(timeout, func() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	defer timer.Stop()
	deadline := time.Now().Add(timeout)
	for !r.observedSet[instanceID] {
		if !time.Now().Before(deadline) {
			return false
		}
		r.cond.Wait()
	}
	return true
}
