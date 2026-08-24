// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: publisher
// @concept: publisher-subscription

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type (
	PublisherClient             = clientiface.PublisherClient
	SubscribeRequest            = clientiface.SubscribeRequest
	ListedPublisherSubscription = clientiface.ListedPublisherSubscription
	PublisherRegistry           = clientiface.PublisherRegistry
)

const subscribeRetryAttempts = 3

const subscribeRetryBase = 200 * time.Millisecond

func StartPublisherSubscriptionsForInstance(
	ctx context.Context, deps PublisherLifecycleDeps, instanceID shared.UUID, params map[string]any, publishers []spec.PublisherSpec, tx persistence.Tx,
) error {
	if len(publishers) == 0 {
		return nil
	}
	now := deps.Clock.Now().UTC()
	var insertErrs []error
	for _, p := range publishers {
		subID := shared.UUID(uuid.New())
		resolvedConfig, resolveErr := resolvePublisherConfig(p.Config, params)
		if resolveErr != nil {
			resolvedConfig = p.Config
		}
		row := persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  p.Name,
			Kind:           p.Kind,
			ResolvedConfig: resolvedConfig,
			MessageType:    p.MessageType,
			// @decision: subscription-mounting-state
			State:     persistence.PublisherSubscriptionStateMounting,
			StartedAt: now,
		}
		_, registered := publisherFromRegistry(deps, p.Name)
		switch {
		case resolveErr != nil:
			deps.Logger.Warn("PUBLISHER.SUBSCRIBE.RESOLVEFAILED",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", resolveErr.Error())
			row.State = persistence.PublisherSubscriptionStateFailed
			row.FailureReason = fmt.Sprintf("publisher config resolution failed: %s", resolveErr.Error())
		case !registered:
			deps.Logger.Warn("PUBLISHER.SUBSCRIBE.UNKNOWNPUBLISHER",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String())
			row.State = persistence.PublisherSubscriptionStateFailed
			row.FailureReason = unknownPublisherReason(p.Name)
		}
		if err := deps.Persist.PublisherSubscriptions().Insert(ctx, row, tx); err != nil {
			deps.Logger.Warn("PUBLISHER.SUBSCRIBE.ROWINSERTFAILED",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
			insertErrs = append(insertErrs,
				fmt.Errorf("publisher %q (subscription %s): insert: %w", p.Name, subID.String(), err))
			continue
		}
		if row.State == persistence.PublisherSubscriptionStateFailed {
			deps.Logger.Warn("PUBLISHER.SUBSCRIBE.FAILED",
				"publisher_subscription_id", subID.String(),
				"reason", row.FailureReason)
		}
	}
	return errors.Join(insertErrs...)
}

func publisherFromRegistry(deps PublisherLifecycleDeps, name string) (PublisherClient, bool) {
	if deps.Publishers == nil {
		return nil, false
	}
	return deps.Publishers.Get(name)
}

func unknownPublisherReason(name string) string {
	return fmt.Sprintf("publisher %q is not registered in this deployment's publisher registry", name)
}

const DefaultPublisherSubscriptionReconcileInterval = 5 * time.Second

const subscribeAttemptTimeout = 10 * time.Second

// @decision: subscription-reconciler
func RunPublisherSubscriptionReconciler(ctx context.Context, deps PublisherLifecycleDeps, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPublisherSubscriptionReconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	runPublisherSubscriptionReconcilerLoop(ctx, deps, ticker.C)
}

func runPublisherSubscriptionReconcilerLoop(ctx context.Context, deps PublisherLifecycleDeps, tick <-chan time.Time) {
	reconcilePublisherSubscriptionsOnce(ctx, deps)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			reconcilePublisherSubscriptionsOnce(ctx, deps)
		}
	}
}

func reconcilePublisherSubscriptionsOnce(ctx context.Context, deps PublisherLifecycleDeps) {
	if err := ResyncPublisherSubscriptions(ctx, deps); err != nil {
		deps.Logger.Warn("PUBLISHER.SUBSCRIBE.RECONCILEFAILED", "error", err.Error())
	}
}

func retryRPCWithBackoff(
	ctx context.Context, log shared.Logger, logEvent string,
	logFields func(attempt int, err error) []any,
	attempt func(attemptCtx context.Context) error,
) error {
	var lastErr error
	for i := 0; i < subscribeRetryAttempts; i++ {
		if i > 0 {
			d := time.Duration(float64(subscribeRetryBase) * pow28(i))
			j := time.Duration(rand.Float64()*0.5*float64(d)) - d/4
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d + j):
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		err := attempt(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if log != nil {
			log.Warn(logEvent, logFields(i+1, err)...)
		}
	}
	return lastErr
}

func callListSubscriptionsWithRetry(ctx context.Context, client PublisherClient, log shared.Logger) ([]ListedPublisherSubscription, error) {
	var live []ListedPublisherSubscription
	err := retryRPCWithBackoff(ctx, log, "PUBLISHER.RESYNCLIST.RETRIED",
		func(attempt int, err error) []any {
			return []any{"publisher_name", client.Name(), "attempt", attempt, "error", err.Error()}
		},
		func(attemptCtx context.Context) error {
			l, err := client.ListSubscriptions(attemptCtx)
			if err != nil {
				return err
			}
			live = l
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return live, nil
}

func callSubscribeWithRetry(ctx context.Context, client PublisherClient, req SubscribeRequest, log shared.Logger) error {
	return retryRPCWithBackoff(ctx, log, "PUBLISHER.SUBSCRIBE.RETRIED",
		func(attempt int, err error) []any {
			return []any{"publisher_subscription_id", req.PublisherSubscriptionID.String(), "attempt", attempt, "error", err.Error()}
		},
		func(attemptCtx context.Context) error {
			return client.Subscribe(attemptCtx, req)
		},
	)
}

func pow28(n int) float64 {
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 2.8
	}
	return v
}

func StopPublisherSubscriptionsForInstance(
	ctx context.Context, deps PublisherLifecycleDeps,
	instanceID shared.UUID,
) error {
	subs, err := deps.Persist.PublisherSubscriptions().ListByInstance(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("StopPublisherSubscriptionsForInstance: list: %w", err)
	}
	for _, s := range subs {
		mounting := s.State == persistence.PublisherSubscriptionStateMounting
		if s.State != persistence.PublisherSubscriptionStateActive && !mounting {
			continue
		}
		client, ok := publisherFromRegistry(deps, s.PublisherName)
		if !ok {
			deps.Logger.Warn("PUBLISHER.UNSUBSCRIBE.UNKNOWNPUBLISHER",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String())
			markSubscriptionStopped(ctx, deps, s.ID, s.State)
			continue
		}
		rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		err := client.Unsubscribe(rpcCtx, s.ID)
		cancel()
		if err != nil {
			deps.Logger.Warn("PUBLISHER.UNSUBSCRIBE.RPCFAILED",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			if mounting {
				markSubscriptionStopped(ctx, deps, s.ID, persistence.PublisherSubscriptionStateMounting)
			}
			continue
		}
		markSubscriptionStopped(ctx, deps, s.ID, s.State)
	}
	return nil
}

func ResyncPublisherSubscriptions(ctx context.Context, deps PublisherLifecycleDeps) error {
	if deps.Publishers == nil {
		return nil
	}
	expected, err := deps.Persist.PublisherSubscriptions().ListByState(ctx, persistence.PublisherSubscriptionStateActive)
	if err != nil {
		return fmt.Errorf("ResyncPublisherSubscriptions: list active: %w", err)
	}
	mounting, err := deps.Persist.PublisherSubscriptions().ListByState(ctx, persistence.PublisherSubscriptionStateMounting)
	if err != nil {
		return fmt.Errorf("ResyncPublisherSubscriptions: list mounting: %w", err)
	}
	expected = append(expected, mounting...)
	recovered, err := recoverUnknownPublisherFailures(ctx, deps)
	if err != nil {
		return fmt.Errorf("ResyncPublisherSubscriptions: recover failed: %w", err)
	}
	expected = append(expected, recovered...)
	expectedByPublisher := map[string][]persistence.PublisherSubscriptionRow{}
	for _, s := range expected {
		expectedByPublisher[s.PublisherName] = append(expectedByPublisher[s.PublisherName], s)
	}
	goneMemo := map[shared.UUID]bool{}
	for _, client := range deps.Publishers.All() {
		live, err := callListSubscriptionsWithRetry(ctx, client, deps.Logger)
		if err != nil {
			deps.Logger.Warn("PUBLISHER.RESYNC.LISTFAILED",
				"publisher_name", client.Name(),
				"error", err.Error())
			continue
		}
		liveSet := map[shared.UUID]struct{}{}
		for _, l := range live {
			liveSet[l.PublisherSubscriptionID] = struct{}{}
		}
		for _, s := range expectedByPublisher[client.Name()] {
			gone, goneErr := instanceTerminatedOrMissingMemo(ctx, deps, s.InstanceID, goneMemo)
			if goneErr != nil {
				deps.Logger.Warn("PUBLISHER.RESYNC.INSTANCEREADFAILED",
					"publisher_name", client.Name(),
					"instance_id", s.InstanceID.String(),
					"publisher_subscription_id", s.ID.String(),
					"error", goneErr.Error())
				continue
			}
			if gone {
				deps.Logger.Info("PUBLISHER.RESYNC.INSTANCETERMINATEDSKIPPED",
					"publisher_name", client.Name(),
					"instance_id", s.InstanceID.String(),
					"publisher_subscription_id", s.ID.String())
				markSubscriptionStopped(ctx, deps, s.ID, s.State)
				if _, live := liveSet[s.ID]; live {
					unsubscribeIfRowStopped(ctx, deps, client, s.ID)
				}
				continue
			}
			if _, ok := liveSet[s.ID]; ok {
				if s.State == persistence.PublisherSubscriptionStateMounting {
					if !markSubscriptionActive(ctx, deps, s.ID) {
						unsubscribeIfRowStopped(ctx, deps, client, s.ID)
					}
				}
				continue
			}
			fresh, err := getSubscriptionRow(ctx, deps, s.ID)
			if err != nil {
				deps.Logger.Warn("PUBLISHER.RESYNC.ROWREADFAILED",
					"publisher_name", client.Name(),
					"publisher_subscription_id", s.ID.String(),
					"error", err.Error())
				continue
			}
			if fresh == nil ||
				(fresh.State != persistence.PublisherSubscriptionStateMounting &&
					fresh.State != persistence.PublisherSubscriptionStateActive) {
				continue
			}
			isMounting := fresh.State == persistence.PublisherSubscriptionStateMounting
			req := SubscribeRequest{
				PublisherSubscriptionID: fresh.ID,
				InstanceID:              fresh.InstanceID,
				Kind:                    fresh.Kind,
				ResolvedConfig:          fresh.ResolvedConfig,
				MessageType:             fresh.MessageType,
			}
			if err := callSubscribeWithRetry(ctx, client, req, deps.Logger); err != nil {
				deps.Logger.Warn("PUBLISHER.RESYNC.SUBSCRIBEFAILED",
					"publisher_name", client.Name(),
					"publisher_subscription_id", s.ID.String(),
					"error", err.Error())
				continue
			}
			if isMounting {
				if !markSubscriptionActive(ctx, deps, s.ID) {
					unsubscribeIfRowStopped(ctx, deps, client, s.ID)
				}
			} else {
				unsubscribeIfRowStopped(ctx, deps, client, s.ID)
			}
		}
		expectedSet := map[shared.UUID]struct{}{}
		for _, s := range expectedByPublisher[client.Name()] {
			expectedSet[s.ID] = struct{}{}
		}
		for _, l := range live {
			if _, ok := expectedSet[l.PublisherSubscriptionID]; ok {
				continue
			}
			fresh, err := getSubscriptionRow(ctx, deps, l.PublisherSubscriptionID)
			if err != nil {
				deps.Logger.Warn("PUBLISHER.RESYNC.ORPHANREADFAILED",
					"publisher_name", client.Name(),
					"publisher_subscription_id", l.PublisherSubscriptionID.String(),
					"error", err.Error())
				continue
			}
			if fresh != nil &&
				(fresh.State == persistence.PublisherSubscriptionStateMounting ||
					fresh.State == persistence.PublisherSubscriptionStateActive) {
				continue
			}
			deps.Logger.Warn("PUBLISHER.RESYNC.ORPHANFOUND",
				"publisher_name", client.Name(),
				"publisher_subscription_id", l.PublisherSubscriptionID.String(),
				"instance_id", l.InstanceID.String(),
				"kind", l.Kind)
			rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
			err = client.Unsubscribe(rpcCtx, l.PublisherSubscriptionID)
			cancel()
			if err != nil {
				deps.Logger.Warn("PUBLISHER.RESYNC.ORPHANUNSUBSCRIBEFAILED",
					"publisher_name", client.Name(),
					"publisher_subscription_id", l.PublisherSubscriptionID.String(),
					"error", err.Error())
			}
		}
	}
	return nil
}

func recoverUnknownPublisherFailures(ctx context.Context, deps PublisherLifecycleDeps) ([]persistence.PublisherSubscriptionRow, error) {
	failed, err := deps.Persist.PublisherSubscriptions().ListByState(ctx, persistence.PublisherSubscriptionStateFailed)
	if err != nil {
		return nil, err
	}
	var recovered []persistence.PublisherSubscriptionRow
	for _, s := range failed {
		if s.FailureReason != unknownPublisherReason(s.PublisherName) {
			continue
		}
		if _, ok := publisherFromRegistry(deps, s.PublisherName); !ok {
			continue
		}
		flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, s.ID,
			persistence.PublisherSubscriptionStateFailed,
			persistence.PublisherSubscriptionStateMounting, "")
		if err != nil {
			deps.Logger.Warn("PUBLISHER.RESYNC.ROWRECOVERFAILED",
				"publisher_name", s.PublisherName,
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			continue
		}
		if !flipped {
			continue
		}
		deps.Logger.Info("PUBLISHER.RESYNC.ROWRECOVERED",
			"publisher_name", s.PublisherName,
			"publisher_subscription_id", s.ID.String())
		s.State = persistence.PublisherSubscriptionStateMounting
		s.FailureReason = ""
		recovered = append(recovered, s)
	}
	return recovered, nil
}

type PublisherLifecycleDeps struct {
	Persist    persistence.Tables
	Publishers PublisherRegistry
	Clock      shared.Clock
	Logger     shared.Logger
}

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

func markSubscriptionActive(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) bool {
	flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, subID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateActive, "")
	if err != nil && deps.Logger != nil {
		deps.Logger.Warn("PUBLISHER.SUBSCRIPTIONACTIVE.UPDATEFAILED",
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
		return false
	}
	if flipped && deps.Logger != nil {
		deps.Logger.Info("PUBLISHER.SUBSCRIPTION.ACTIVE",
			"publisher_subscription_id", subID.String())
	}
	return flipped
}

func getSubscriptionRow(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) (*persistence.PublisherSubscriptionRow, error) {
	var row *persistence.PublisherSubscriptionRow
	err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.PublisherSubscriptions().Get(ctx, subID, tx)
		row = r
		return err
	})
	return row, err
}

func instanceTerminatedOrMissing(ctx context.Context, deps PublisherLifecycleDeps, instanceID shared.UUID) (bool, error) {
	var inst *persistence.InstanceRow
	err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, err := deps.Persist.Instances().Get(ctx, instanceID, tx)
		inst = i
		return err
	})
	if err != nil {
		return false, err
	}
	return inst == nil || inst.TerminatedAt != nil, nil
}

func instanceTerminatedOrMissingMemo(
	ctx context.Context, deps PublisherLifecycleDeps,
	instanceID shared.UUID, memo map[shared.UUID]bool,
) (bool, error) {
	if gone, ok := memo[instanceID]; ok {
		return gone, nil
	}
	gone, err := instanceTerminatedOrMissing(ctx, deps, instanceID)
	if err != nil {
		return false, err
	}
	memo[instanceID] = gone
	return gone, nil
}

func unsubscribeIfRowStopped(ctx context.Context, deps PublisherLifecycleDeps, client PublisherClient, subID shared.UUID) {
	row, err := getSubscriptionRow(ctx, deps, subID)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("PUBLISHER.UNSUBSCRIBECOMPENSATION.READFAILED",
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
		}
		return
	}
	if row != nil && row.State != persistence.PublisherSubscriptionStateStopped {
		return
	}
	rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
	defer cancel()
	if err := client.Unsubscribe(rpcCtx, subID); err != nil && deps.Logger != nil {
		deps.Logger.Warn("PUBLISHER.UNSUBSCRIBECOMPENSATION.FAILED",
			"publisher_name", client.Name(),
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
	}
}

func markSubscriptionStopped(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID, fromState string) {
	flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, subID,
		fromState, persistence.PublisherSubscriptionStateStopped, "")
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("PUBLISHER.SUBSCRIPTIONSTOPPED.UPDATEFAILED",
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
		}
		return
	}
	if !flipped && deps.Logger != nil {
		deps.Logger.Info("PUBLISHER.SUBSCRIPTIONSTOPPED.ALREADYSETTLED",
			"publisher_subscription_id", subID.String(),
			"expected_state", fromState)
	}
}
