// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// publishers.go — publisher-subscription lifecycle helpers consumed by
// the control-api.
//
// Spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
// @concept: publisher
// @concept: publisher-subscription
//
// The control-api calls these helpers from the instance-create and
// instance-terminate flows; the supervisor calls
// `ResyncPublisherSubscriptions` at startup. Each helper is a thin
// facade over a `PublisherRegistry` (operator-supplied) so the wire
// dependency stays out of the controlapi package.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/runtime/clientiface"
)

// Re-exports of the wire-shape types from `runtime/clientiface/`
// (Apache-licensed). The canonical docs live on the alias targets.
// See `data_processing.go` for the licensing-boundary rationale.
type (
	PublisherClient             = clientiface.PublisherClient
	SubscribeRequest            = clientiface.SubscribeRequest
	ListedPublisherSubscription = clientiface.ListedPublisherSubscription
	PublisherRegistry           = clientiface.PublisherRegistry
)

// subscribeRetryAttempts bounds the publisher Subscribe RPC's
// retry-with-backoff loop. Per spec §Architecture details §Rimsky-side
// Subscribe retry.
const subscribeRetryAttempts = 3

// subscribeRetryBase is the initial backoff before the second attempt;
// each subsequent attempt waits roughly 2.8× the previous (200ms →
// 560ms → ~1.6s), with ±25% jitter.
const subscribeRetryBase = 200 * time.Millisecond

// StartPublisherSubscriptionsForInstance walks the template's
// `publishers:` block, for each entry: generates a publisher-
// subscription id, INSERTs the table:rimsky_publisher_subscriptions row
// with `state = active`, and calls `PublisherClient.Subscribe` on the
// registered publisher service. Per spec §Per-instance parameterization,
// Subscribe failures leave the row at `state = failed` and log; they
// do NOT block instance creation (operator-recoverable via resync).
//
// `clock` and `logger` are explicit parameters per cold-read style.
func StartPublisherSubscriptionsForInstance(
	ctx context.Context, deps PublisherLifecycleDeps,
	instanceID shared.UUID, params map[string]any, publishers []spec.PublisherSpec,
) error {
	if len(publishers) == 0 {
		return nil
	}
	now := deps.Clock.Now().UTC()
	for _, p := range publishers {
		subID := shared.UUID(uuid.New())
		resolvedConfig, err := resolvePublisherConfig(p.Config, params)
		if err != nil {
			deps.Logger.Warn("publisher.subscribe.resolve_failed",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"error", err.Error())
			continue
		}
		messageKind := p.MessageKind
		if messageKind == "" {
			messageKind = "invalidate"
		}
		row := persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  p.Name,
			Kind:           p.Kind,
			ResolvedConfig: resolvedConfig,
			TargetNode:     p.TargetNode,
			MessageKind:    messageKind,
			State:          persistence.PublisherSubscriptionStateActive,
			StartedAt:      now,
		}
		// INSERT first so a Subscribe failure still leaves an
		// auditable row. State flips to failed on RPC error below.
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.PublisherSubscriptions().Insert(ctx, tx, row)
		}); err != nil {
			deps.Logger.Warn("publisher.subscribe.row_insert_failed",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
			continue
		}
		client, ok := deps.Publishers.Get(p.Name)
		if !ok {
			deps.Logger.Warn("publisher.subscribe.unknown_publisher",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String())
			markSubscriptionFailed(ctx, deps, subID)
			continue
		}
		req := SubscribeRequest{
			PublisherSubscriptionID: subID,
			InstanceID:              instanceID,
			Kind:                    p.Kind,
			ResolvedConfig:          resolvedConfig,
			TargetNode:              p.TargetNode,
			MessageKind:             messageKind,
		}
		if err := callSubscribeWithRetry(ctx, client, req, deps.Logger); err != nil {
			deps.Logger.Warn("publisher.subscribe.rpc_failed",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
			markSubscriptionFailed(ctx, deps, subID)
			continue
		}
	}
	return nil
}

// callSubscribeWithRetry wraps PublisherClient.Subscribe with a bounded
// retry-with-backoff loop. Per spec §Rimsky-side Subscribe retry:
// 3 attempts with exp 200ms → ~1.6s, jittered ±25%. After exhaustion
// the caller marks the publisher-subscription row state='failed'.
func callSubscribeWithRetry(ctx context.Context, client PublisherClient, req SubscribeRequest, log shared.Logger) error {
	var lastErr error
	for attempt := 0; attempt < subscribeRetryAttempts; attempt++ {
		if attempt > 0 {
			// Backoff: 200ms, ~560ms, ~1.6s, each ±25% jitter.
			d := time.Duration(float64(subscribeRetryBase) * pow28(attempt))
			j := time.Duration(rand.Float64()*0.5*float64(d)) - d/4
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d + j):
			}
		}
		err := client.Subscribe(ctx, req)
		if err == nil {
			return nil
		}
		lastErr = err
		if log != nil {
			log.Warn("publisher.subscribe.retry",
				"publisher_subscription_id", req.PublisherSubscriptionID.String(),
				"attempt", attempt+1,
				"error", err.Error())
		}
	}
	return lastErr
}

// pow28 returns 2.8^n for small n (avoids importing math.Pow).
func pow28(n int) float64 {
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 2.8
	}
	return v
}

// StopPublisherSubscriptionsForInstance walks active publisher-
// subscriptions for the instance and calls `PublisherClient.Unsubscribe`
// on each; sets `state = stopped`. On any per-subscription failure, the
// loop continues so a single broken publisher cannot block instance
// termination.
func StopPublisherSubscriptionsForInstance(
	ctx context.Context, deps PublisherLifecycleDeps,
	instanceID shared.UUID,
) error {
	subs, err := deps.Persist.PublisherSubscriptions().ListByInstance(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("StopPublisherSubscriptionsForInstance: list: %w", err)
	}
	for _, s := range subs {
		if s.State != persistence.PublisherSubscriptionStateActive {
			continue
		}
		client, ok := deps.Publishers.Get(s.PublisherName)
		if !ok {
			deps.Logger.Warn("publisher.unsubscribe.unknown_publisher",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String())
			markSubscriptionStopped(ctx, deps, s.ID)
			continue
		}
		if err := client.Unsubscribe(ctx, s.ID); err != nil {
			deps.Logger.Warn("publisher.unsubscribe.rpc_failed",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			// Leave at active so resync can retry; do not flip to stopped.
			continue
		}
		markSubscriptionStopped(ctx, deps, s.ID)
	}
	return nil
}

// ResyncPublisherSubscriptions is invoked at supervisor startup. For
// each configured publisher it:
//
//  1. Calls `Publisher.ListSubscriptions()` to enumerate what the
//     publisher sees.
//  2. Lists `table:rimsky_publisher_subscriptions` rows in
//     `state = active` for that publisher name.
//  3. For subscriptions rimsky expects but the publisher doesn't
//     report, re-issues `Subscribe` to restore.
//  4. For orphan subscriptions the publisher reports but rimsky doesn't
//     know about, issues `Unsubscribe` and logs at WARN.
//
// Errors from individual publishers are logged and the sweep continues
// across the remaining set — one broken publisher cannot wedge the rest.
func ResyncPublisherSubscriptions(ctx context.Context, deps PublisherLifecycleDeps) error {
	if deps.Publishers == nil {
		return nil
	}
	expected, err := deps.Persist.PublisherSubscriptions().ListByState(ctx, persistence.PublisherSubscriptionStateActive)
	if err != nil {
		return fmt.Errorf("ResyncPublisherSubscriptions: list active: %w", err)
	}
	expectedByPublisher := map[string][]persistence.PublisherSubscriptionRow{}
	for _, s := range expected {
		expectedByPublisher[s.PublisherName] = append(expectedByPublisher[s.PublisherName], s)
	}
	for _, client := range deps.Publishers.All() {
		live, err := client.ListSubscriptions(ctx)
		if err != nil {
			deps.Logger.Warn("publisher.resync.list_failed",
				"publisher_name", client.Name(),
				"error", err.Error())
			continue
		}
		liveSet := map[shared.UUID]struct{}{}
		for _, l := range live {
			liveSet[l.PublisherSubscriptionID] = struct{}{}
		}
		// Rimsky-expected, publisher-missing → re-Subscribe.
		for _, s := range expectedByPublisher[client.Name()] {
			if _, ok := liveSet[s.ID]; ok {
				continue
			}
			req := SubscribeRequest{
				PublisherSubscriptionID: s.ID,
				InstanceID:              s.InstanceID,
				Kind:                    s.Kind,
				ResolvedConfig:          s.ResolvedConfig,
				TargetNode:              s.TargetNode,
				MessageKind:             s.MessageKind,
			}
			if err := callSubscribeWithRetry(ctx, client, req, deps.Logger); err != nil {
				deps.Logger.Warn("publisher.resync.subscribe_failed",
					"publisher_name", client.Name(),
					"publisher_subscription_id", s.ID.String(),
					"error", err.Error())
			}
		}
		// Publisher-reported, rimsky-unknown → Unsubscribe + log.
		expectedSet := map[shared.UUID]struct{}{}
		for _, s := range expectedByPublisher[client.Name()] {
			expectedSet[s.ID] = struct{}{}
		}
		for _, l := range live {
			if _, ok := expectedSet[l.PublisherSubscriptionID]; ok {
				continue
			}
			deps.Logger.Warn("publisher.resync.orphan_subscription",
				"publisher_name", client.Name(),
				"publisher_subscription_id", l.PublisherSubscriptionID.String(),
				"instance_id", l.InstanceID.String(),
				"kind", l.Kind)
			if err := client.Unsubscribe(ctx, l.PublisherSubscriptionID); err != nil {
				deps.Logger.Warn("publisher.resync.unsubscribe_orphan_failed",
					"publisher_name", client.Name(),
					"publisher_subscription_id", l.PublisherSubscriptionID.String(),
					"error", err.Error())
			}
		}
	}
	return nil
}

// PublisherLifecycleDeps is the dependency capsule for the
// publisher-subscription lifecycle helpers. Struct-shaped so the call
// sites stay compact and the deps surface is easy to grep.
type PublisherLifecycleDeps struct {
	Persist    persistence.Tables
	Publishers PublisherRegistry
	Clock      shared.Clock
	Logger     shared.Logger
}

// resolvePublisherConfig applies `{{params.X}}` substitution to the
// publisher config blob. The implementation is intentionally minimal —
// the graph-side substitution layer (graph/attribute/substitution.go)
// is the canonical site for richer template grammars. Here we walk
// JSON leaves and resolve `{{params.<path>}}` references against the
// instance params map.
func resolvePublisherConfig(raw []byte, params map[string]any) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("publisher config not valid JSON: %w", err)
	}
	resolved := walkPublisherConfig(doc, params)
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func walkPublisherConfig(v any, params map[string]any) any {
	switch val := v.(type) {
	case string:
		return resolveParamsLeaf(val, params)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			out[k] = walkPublisherConfig(inner, params)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			out[i] = walkPublisherConfig(inner, params)
		}
		return out
	default:
		return val
	}
}

func resolveParamsLeaf(s string, params map[string]any) any {
	if len(s) < 4 {
		return s
	}
	if s[:2] != "{{" || s[len(s)-2:] != "}}" {
		return s
	}
	inner := s[2 : len(s)-2]
	const prefix = "params."
	if len(inner) <= len(prefix) || inner[:len(prefix)] != prefix {
		return s
	}
	path := inner[len(prefix):]
	if v, ok := lookupParam(params, path); ok {
		return v
	}
	return s
}

func lookupParam(params map[string]any, path string) (any, bool) {
	if params == nil {
		return nil, false
	}
	cur := any(params)
	for _, seg := range splitDots(path) {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func splitDots(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func markSubscriptionFailed(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) {
	state := persistence.PublisherSubscriptionStateFailed
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deps.Persist.PublisherSubscriptions().Update(ctx, tx, subID, persistence.PublisherSubscriptionUpdate{State: &state})
	}); err != nil && deps.Logger != nil {
		deps.Logger.Warn("publisher.markSubscriptionFailed.update_failed",
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
	}
}

func markSubscriptionStopped(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) {
	state := persistence.PublisherSubscriptionStateStopped
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deps.Persist.PublisherSubscriptions().Update(ctx, tx, subID, persistence.PublisherSubscriptionUpdate{State: &state})
	}); err != nil && deps.Logger != nil {
		deps.Logger.Warn("publisher.markSubscriptionStopped.update_failed",
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
	}
}

// keep time / errors alive when the file compiles with paths excluded.
var _ = time.Time{}
var _ = errors.New
