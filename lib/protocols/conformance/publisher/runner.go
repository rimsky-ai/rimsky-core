// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type CheckResult struct {
	Name string
	Err  error
}

type RunOpts struct {
	Kind            string
	ResolvedConfig  []byte
	MessageReceiver *MessageReceiver
	SubscriptionID  string
	InstanceID      string
	MessageType     string
	MessageTimeout  time.Duration
}

func Run(ctx context.Context, c genv1.PublisherClient, opts RunOpts) []CheckResult {
	if opts.SubscriptionID == "" {
		opts.SubscriptionID = "conformance-subscription"
	}
	if opts.InstanceID == "" {
		opts.InstanceID = "conformance-instance"
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

func checkCapabilities(ctx context.Context, c genv1.PublisherClient, kind string) CheckResult {
	caps, err := c.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return CheckResult{Name: "Capabilities", Err: err}
	}
	if len(caps.GetSupportedKinds()) == 0 {
		return CheckResult{Name: "Capabilities", Err: fmt.Errorf("supported_kinds is empty")}
	}
	var matched *genv1.PublisherKindCapability
	for _, k := range caps.GetSupportedKinds() {
		if k.GetKind() == kind {
			matched = k
			break
		}
	}
	if matched == nil {
		return CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("kind %q not advertised in supported_kinds", kind),
		}
	}
	protocolsHasPublisher := false
	for _, p := range caps.GetProtocols() {
		if p == "publisher" {
			protocolsHasPublisher = true
			break
		}
	}
	if !protocolsHasPublisher {
		return CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("protocols %v does not advertise the mix-in service protocol \"publisher\"", caps.GetProtocols()),
		}
	}
	if schema := matched.GetConfigSchema(); len(schema) > 0 {
		var probe any
		if err := json.Unmarshal(schema, &probe); err != nil {
			return CheckResult{
				Name: "Capabilities",
				Err:  fmt.Errorf("kind %q config_schema does not parse as JSON: %w", kind, err),
			}
		}
	}
	return CheckResult{Name: "Capabilities"}
}

func checkSubscribe(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: opts.SubscriptionID,
		InstanceId:              opts.InstanceID,
		Kind:                    opts.Kind,
		ResolvedConfig:          opts.ResolvedConfig,
		MessageType:             opts.MessageType,
	}); err != nil {
		return CheckResult{Name: "Subscribe", Err: err}
	}
	return CheckResult{Name: "Subscribe"}
}

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
			if w.GetMessageType() != opts.MessageType {
				return CheckResult{
					Name: "ListSubscriptions",
					Err:  fmt.Errorf("subscription %q message_type %q != expected %q", opts.SubscriptionID, w.GetMessageType(), opts.MessageType),
				}
			}
			if !bytes.Equal(w.GetResolvedConfig(), opts.ResolvedConfig) {
				return CheckResult{
					Name: "ListSubscriptions",
					Err: fmt.Errorf("subscription %q resolved_config %q != expected %q",
						opts.SubscriptionID, string(w.GetResolvedConfig()), string(opts.ResolvedConfig)),
				}
			}
			if w.GetStartedAt() == nil || w.GetStartedAt().AsTime().IsZero() {
				return CheckResult{
					Name: "ListSubscriptions",
					Err:  fmt.Errorf("subscription %q started_at is unset; ListSubscriptions is the restart-reconciliation surface and must report when the subscription started", opts.SubscriptionID),
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

func checkSubscribeIdempotent(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: opts.SubscriptionID,
		InstanceId:              opts.InstanceID,
		Kind:                    opts.Kind,
		ResolvedConfig:          opts.ResolvedConfig,
		MessageType:             opts.MessageType,
	}); err != nil {
		return CheckResult{Name: "SubscribeIdempotent", Err: err}
	}
	return CheckResult{Name: "SubscribeIdempotent"}
}

func checkMessagePush(c genv1.PublisherClient, opts RunOpts) CheckResult {
	ok := opts.MessageReceiver.WaitForMessage(opts.InstanceID, opts.MessageTimeout)
	if !ok {
		return CheckResult{
			Name: "MessagePush",
			Err: fmt.Errorf("no message arrived at the receiver within %s for instance_id=%q",
				opts.MessageTimeout, opts.InstanceID),
		}
	}
	_ = c
	return CheckResult{Name: "MessagePush"}
}

func checkUnsubscribe(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: opts.SubscriptionID}); err != nil {
		return CheckResult{Name: "Unsubscribe", Err: err}
	}
	return CheckResult{Name: "Unsubscribe"}
}

func checkUnsubscribeIdempotent(ctx context.Context, c genv1.PublisherClient, opts RunOpts) CheckResult {
	if _, err := c.Unsubscribe(ctx, &genv1.UnsubscribeRequest{PublisherSubscriptionId: opts.SubscriptionID}); err != nil {
		return CheckResult{Name: "UnsubscribeIdempotent", Err: err}
	}
	return CheckResult{Name: "UnsubscribeIdempotent"}
}

type MessageReceiver struct {
	mu          sync.Mutex
	cond        *sync.Cond
	observedSet map[string]bool
}

func NewMessageReceiver() *MessageReceiver {
	r := &MessageReceiver{observedSet: make(map[string]bool)}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *MessageReceiver) NoteMessage(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observedSet[instanceID] = true
	r.cond.Broadcast()
}

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
