// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// publishers.go — publisher-subscription lifecycle helpers consumed by
// the control-api.
//
// @concept: publisher
// @concept: publisher-subscription
//
// Publisher-subscription rows are desired state: instance-create
// inserts them in state='mounting' in the same transaction as the
// instance row (atomic: no committed instance without its subscription
// rows) and returns immediately — it never
// performs (or blocks on, or fails because of) the publisher Subscribe
// RPC. `RunPublisherSubscriptionReconciler` drives the Subscribe
// handshake for mounting rows with no attempt cap (the tick interval is
// the backoff) and flips rows to 'active' on success; 'failed' is
// reserved for non-retryable errors (an unregistered publisher name, a
// config blob that cannot resolve), which instance-create stamps
// directly in the insert.
// `ResyncPublisherSubscriptions` at control-api startup remains the
// durable safety net for rows the publisher dropped.
//
// The control-api calls these helpers from the instance-create and
// instance-terminate flows and starts the reconciler + resync at
// startup (the publisher registry lives in the control-api, not the
// supervisor). Each helper is a thin facade over a `PublisherRegistry`
// (operator-supplied) so the wire dependency stays out of the
// controlapi package.

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

// @deliberate: re-exports of wire-shape types from runtime/clientiface/
// (Apache-licensed). Canonical docs live on the alias targets. See
// data_processing.go for the licensing-boundary rationale.
type (
	PublisherClient             = clientiface.PublisherClient
	SubscribeRequest            = clientiface.SubscribeRequest
	ListedPublisherSubscription = clientiface.ListedPublisherSubscription
	PublisherRegistry           = clientiface.PublisherRegistry
)

// subscribeRetryAttempts bounds the bounded retry-with-backoff loops
// used by the startup resync sweep (callSubscribeWithRetry /
// callListSubscriptionsWithRetry). The instance-create path performs no
// Subscribe RPC at all, and the reconciliation worker retries with no
// attempt cap (its tick interval is the backoff) — only resync's
// one-shot sweep uses this bounded budget.
const subscribeRetryAttempts = 3

// subscribeRetryBase is the base of the backoff series. No sleep
// precedes attempt 1; the wait before attempt n+1 is base × 2.8^n —
// ~560ms before attempt 2, ~1.6s before attempt 3 — with ±25% jitter.
const subscribeRetryBase = 200 * time.Millisecond

// StartPublisherSubscriptionsForInstance walks the template's
// `publishers:` block and, for each entry, generates a publisher-
// subscription id and INSERTs the table:rimsky_publisher_subscriptions
// row with `state = mounting`, on the caller-supplied tx. The control-
// api passes its instance-create transaction so the subscription rows
// commit atomically with the instance row — a post-commit failure in
// the create handler (e.g. the lifecycle fan-out) can never strand a
// live instance with no subscription rows, because either both
// committed or neither did. It performs NO publisher RPC — that is a
// load-bearing property: instance-create must not block on, or fail
// because of, publisher reachability (the inserts are pure DB writes).
// The reconciliation worker (`RunPublisherSubscriptionReconciler`)
// drives the Subscribe handshake asynchronously and flips rows to
// active.
//
// Per-row insert failures are accumulated (errors.Join) and returned —
// never swallowed: the caller's tx rolls back and the create fails
// rather than committing an instance with a partial subscription set.
//
// The `failed` writers in this path are the two non-retryable classes:
// the unknown-publisher check (a publisher name absent from the
// registry — the registry is fixed per process config) and the
// config-resolve check (a malformed publisher config blob — the
// template is fixed once registered). Both insert the row directly in
// `state = failed` with the reason stamped in the SAME insert tx — an
// insert-mounting-then-flip would commit a mounting row the reconciler
// could pick up in the gap and Subscribe with a config we already know
// is bad (or race its mounting→active CAS against the failed flip).
//
// `clock` and `logger` are explicit parameters per cold-read style.
func StartPublisherSubscriptionsForInstance(
	ctx context.Context, deps PublisherLifecycleDeps, tx persistence.Tx,
	instanceID shared.UUID, params map[string]any, publishers []spec.PublisherSpec,
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
			// @constraint: non-retryable, but never silent — the row is
			// still inserted (with the unresolved config blob), directly
			// in state=failed with the resolve error as the operator-
			// readable reason, exactly like the unknown-publisher class
			// below.
			resolvedConfig = p.Config
		}
		// MessageType has no default — the legacy "invalidate" fallback
		// retired with the envelope's kind→type rename. The template
		// validator rejects PublisherSpec entries with an empty
		// message_type at registration, so by the time we mount a
		// publisher-subscription the value is non-empty by construction.
		row := persistence.PublisherSubscriptionRow{
			ID:             subID,
			InstanceID:     instanceID,
			PublisherName:  p.Name,
			Kind:           p.Kind,
			ResolvedConfig: resolvedConfig,
			TargetNode:     p.TargetNode,
			MessageType:    p.MessageType,
			State:          persistence.PublisherSubscriptionStateMounting,
			StartedAt:      now,
		}
		// @constraint: non-retryable classes are stamped failed IN the
		// insert itself — committing a mounting row first and CAS-
		// flipping it after opens a window where a reconciler tick
		// Subscribes a row we already know is broken (worst case: the
		// mounting→active CAS wins over the failed flip and an
		// unresolved config goes permanently active).
		_, registered := publisherFromRegistry(deps, p.Name)
		switch {
		case resolveErr != nil:
			// @constraint: config-resolve failure is non-retryable —
			// the template's config blob is fixed, so no retry can make
			// it resolvable.
			deps.Logger.Warn("publisher.subscribe.resolve_failed",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", resolveErr.Error())
			row.State = persistence.PublisherSubscriptionStateFailed
			row.FailureReason = fmt.Sprintf("publisher config resolution failed: %s", resolveErr.Error())
		case !registered:
			// @constraint: unknown publisher name is non-retryable — no
			// reconciler tick can make an unregistered name resolvable.
			deps.Logger.Warn("publisher.subscribe.unknown_publisher",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String())
			row.State = persistence.PublisherSubscriptionStateFailed
			row.FailureReason = unknownPublisherReason(p.Name)
		}
		if err := deps.Persist.PublisherSubscriptions().Insert(ctx, tx, row); err != nil {
			deps.Logger.Warn("publisher.subscribe.row_insert_failed",
				"publisher_name", p.Name,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
			insertErrs = append(insertErrs,
				fmt.Errorf("publisher %q (subscription %s): insert: %w", p.Name, subID.String(), err))
			continue
		}
		if row.State == persistence.PublisherSubscriptionStateFailed {
			deps.Logger.Warn("publisher.subscribe.failed",
				"publisher_subscription_id", subID.String(),
				"reason", row.FailureReason)
		}
	}
	return errors.Join(insertErrs...)
}

// publisherFromRegistry is the nil-safe registry lookup. The registry
// may be nil in wiring shapes that configure no publishers at all.
func publisherFromRegistry(deps PublisherLifecycleDeps, name string) (PublisherClient, bool) {
	if deps.Publishers == nil {
		return nil, false
	}
	return deps.Publishers.Get(name)
}

// unknownPublisherReason is the operator-readable failure_reason
// stamped on a publisher-subscription row whose publisher name is not
// present in the registry (the non-retryable failure class).
func unknownPublisherReason(name string) string {
	return fmt.Sprintf("publisher %q is not registered in this deployment's publisher registry", name)
}

// DefaultPublisherSubscriptionReconcileInterval is the reconciler's
// default tick. The tick interval IS the retry backoff — each mounting
// row gets one Subscribe attempt per tick, forever.
const DefaultPublisherSubscriptionReconcileInterval = 5 * time.Second

// subscribeAttemptTimeout bounds ONE publisher RPC attempt — every
// individual Subscribe / ListSubscriptions / Unsubscribe issued by the
// reconciler, the startup resync sweep (including its bounded-retry
// helpers and the orphan sweep), the compensating teardown, and the
// instance stop path. The property protected: a black-holed publisher
// (process frozen, network partition — the connection is up but no
// response ever comes) must not wedge the reconciler pass, the resync
// sweep, or an instance termination request — every OTHER row stops
// progressing behind the hung RPC and the "one broken publisher cannot
// wedge the rest" promise is false. A timed-out attempt is the
// retryable class: the row keeps its state and the next tick / retry /
// sweep remains its driver.
const subscribeAttemptTimeout = 10 * time.Second

// RunPublisherSubscriptionReconciler drives the publisher Subscribe
// handshake for `state = mounting` rows. Started by the control-api at
// startup (beside ResyncPublisherSubscriptions — the publisher registry
// is control-api-side) and runs until ctx is canceled.
//
// Each tick lists the mounting rows and, for each, issues one Subscribe
// RPC: on success the row flips to active; on RPC failure it stays
// mounting and is retried next tick; an unknown publisher name (the
// non-retryable class) flips it to failed with a reason; a row whose
// instance is terminated or deleted is flipped to stopped without
// subscribing (a terminated instance rejects every publisher emit).
//
// Load-bearing property: there is NO attempt budget. A slow,
// overloaded, or briefly-down publisher keeps its subscriptions in
// observable `mounting` until it recovers — bounded budgets convert
// contention spikes into silent failures, which is exactly the behavior
// this worker replaces. `failed` is reserved for errors no retry can
// fix.
//
// State flips use the guarded CompareAndSetState so a concurrent
// lifecycle transition (e.g. instance terminate marking the row
// stopped while a Subscribe RPC is in flight) is never overwritten by
// a late flip.
func RunPublisherSubscriptionReconciler(ctx context.Context, deps PublisherLifecycleDeps, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPublisherSubscriptionReconcileInterval
	}
	// @deliberate: one immediate pass so rows pending at startup don't
	// wait a full tick, then the ticker cadence.
	reconcileMountingSubscriptionsOnce(ctx, deps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcileMountingSubscriptionsOnce(ctx, deps)
		}
	}
}

// reconcileMountingSubscriptionsOnce is one reconciler pass: list the
// mounting rows and drive each toward active. Errors are logged and the
// pass continues — one broken publisher cannot wedge the rest.
func reconcileMountingSubscriptionsOnce(ctx context.Context, deps PublisherLifecycleDeps) {
	rows, err := deps.Persist.PublisherSubscriptions().ListByState(ctx, persistence.PublisherSubscriptionStateMounting)
	if err != nil {
		deps.Logger.Warn("publisher.subscribe.reconcile_list_failed", "error", err.Error())
		return
	}
	// @deliberate: per-pass memo — many rows can share an instance, and
	// one Get per row per tick is a needless N+1; each instance is
	// fetched at most once per pass.
	goneMemo := map[shared.UUID]bool{}
	for _, s := range rows {
		if ctx.Err() != nil {
			return
		}
		// @constraint: never mount a subscription for an instance that
		// is terminated or gone — a terminated instance rejects every
		// publisher emit (errInstanceTerminated on the message
		// endpoint), so an active subscription for it is a permanent
		// dead-letter generator. Such rows are flipped to stopped
		// instead. On a failed read, skip the row (retry next tick)
		// rather than Subscribing blind.
		gone, err := instanceTerminatedOrMissingMemo(ctx, deps, s.InstanceID, goneMemo)
		if err != nil {
			deps.Logger.Warn("publisher.subscribe.instance_read_failed",
				"publisher_name", s.PublisherName,
				"instance_id", s.InstanceID.String(),
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			continue
		}
		if gone {
			deps.Logger.Info("publisher.subscribe.instance_terminated_skip",
				"publisher_name", s.PublisherName,
				"instance_id", s.InstanceID.String(),
				"publisher_subscription_id", s.ID.String())
			markSubscriptionStopped(ctx, deps, s.ID, persistence.PublisherSubscriptionStateMounting)
			continue
		}
		client, ok := publisherFromRegistry(deps, s.PublisherName)
		if !ok {
			deps.Logger.Warn("publisher.subscribe.unknown_publisher",
				"publisher_name", s.PublisherName,
				"instance_id", s.InstanceID.String(),
				"publisher_subscription_id", s.ID.String())
			markSubscriptionFailed(ctx, deps, s.ID, unknownPublisherReason(s.PublisherName))
			continue
		}
		req := SubscribeRequest{
			PublisherSubscriptionID: s.ID,
			InstanceID:              s.InstanceID,
			Kind:                    s.Kind,
			ResolvedConfig:          s.ResolvedConfig,
			TargetNode:              s.TargetNode,
			MessageType:             s.MessageType,
		}
		// @constraint: one attempt per tick — the tick interval is the
		// backoff; the row stays in observable `mounting` between
		// attempts. The attempt is deadline-bounded
		// (subscribeAttemptTimeout) so a black-holed publisher cannot
		// wedge the pass for the other rows; a timeout is just the
		// retryable failure class.
		attemptCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		err = client.Subscribe(attemptCtx, req)
		cancel()
		if err != nil {
			deps.Logger.Warn("publisher.subscribe.rpc_failed",
				"publisher_name", s.PublisherName,
				"instance_id", s.InstanceID.String(),
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			continue
		}
		if !markSubscriptionActive(ctx, deps, s.ID) {
			// @constraint: a concurrent transition settled the row
			// mid-Subscribe (instance terminate is the common case).
			// If the row is stopped/gone, reap the publisher-side
			// subscription now.
			unsubscribeIfRowStopped(ctx, deps, client, s.ID)
		}
	}
}

// callListSubscriptionsWithRetry wraps PublisherClient.ListSubscriptions
// with the same bounded retry-with-backoff loop as Subscribe. Reconcile
// runs in a startup goroutine that races with whatever stack glue makes
// the publisher reachable (host-port tunnels, sidecars, DNS warm-up); a
// single one-shot ListSubscriptions silently skips the publisher's
// reconcile leg when the first attempt loses the race, so the retry is
// not optional. 3 attempts with waits of ~560ms then ~1.6s between
// them, jittered ±25%
// (identical schedule to callSubscribeWithRetry so the two helpers share
// observable failure semantics). Each individual attempt carries a
// subscribeAttemptTimeout deadline, same as the reconciler's per-RPC
// bound.
func callListSubscriptionsWithRetry(ctx context.Context, client PublisherClient, log shared.Logger) ([]ListedPublisherSubscription, error) {
	var lastErr error
	for attempt := 0; attempt < subscribeRetryAttempts; attempt++ {
		if attempt > 0 {
			d := time.Duration(float64(subscribeRetryBase) * pow28(attempt))
			j := time.Duration(rand.Float64()*0.5*float64(d)) - d/4
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d + j):
			}
		}
		// @constraint: each attempt is deadline-bounded
		// (subscribeAttemptTimeout) so a black-holed publisher cannot
		// wedge the resync sweep.
		attemptCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		live, err := client.ListSubscriptions(attemptCtx)
		cancel()
		if err == nil {
			return live, nil
		}
		lastErr = err
		if log != nil {
			log.Warn("publisher.resync.list_retry",
				"publisher_name", client.Name(),
				"attempt", attempt+1,
				"error", err.Error())
		}
	}
	return nil, lastErr
}

// callSubscribeWithRetry wraps PublisherClient.Subscribe with a bounded
// retry-with-backoff loop: 3 attempts with waits of ~560ms then ~1.6s
// between them, jittered ±25%. Used only by the startup resync sweep
// (a one-shot pass); the
// reconciliation worker retries forever on its own tick and the
// instance-create path performs no Subscribe RPC at all. Each
// individual attempt carries a subscribeAttemptTimeout deadline, same
// as the reconciler's per-RPC bound. Exhaustion
// here is log-only — the row keeps its state and the reconciler /
// next resync remain its drivers.
func callSubscribeWithRetry(ctx context.Context, client PublisherClient, req SubscribeRequest, log shared.Logger) error {
	var lastErr error
	for attempt := 0; attempt < subscribeRetryAttempts; attempt++ {
		if attempt > 0 {
			// @deliberate: backoff is ~560ms before attempt 2, ~1.6s
			// before attempt 3 (base 200ms × 2.8^attempt; no sleep
			// before attempt 1), each ±25% jitter.
			d := time.Duration(float64(subscribeRetryBase) * pow28(attempt))
			j := time.Duration(rand.Float64()*0.5*float64(d)) - d/4
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d + j):
			}
		}
		// @constraint: each attempt is deadline-bounded
		// (subscribeAttemptTimeout) so a black-holed publisher cannot
		// wedge the resync sweep.
		attemptCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		err := client.Subscribe(attemptCtx, req)
		cancel()
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

// StopPublisherSubscriptionsForInstance walks the instance's mounting
// and active publisher-subscriptions, calls `PublisherClient.Unsubscribe`
// on each, and sets `state = stopped`. On any per-subscription failure,
// the loop continues so a single broken publisher cannot block instance
// termination.
//
// Mounting rows are always flipped to stopped — even when Unsubscribe
// fails — so the reconciliation worker never keeps re-driving Subscribe
// for a terminated instance (the property protected here is reconciler
// termination; the resync orphan sweep is the safety net for a
// publisher-side leftover). An active row whose Unsubscribe fails stays
// active; its teardown is retried by the eventual DELETE's call here
// and by the startup resync, which checks the row's instance, finds it
// terminated, flips the row to stopped, and retries the publisher-side
// Unsubscribe (it never re-Subscribes a terminated instance's rows).
// On instance DELETE the subscription rows are cascade-deleted with the
// instance row, and a publisher-side leftover whose Unsubscribe failed
// is reaped by the startup resync's orphan sweep (which unsubscribes
// publisher-reported subscriptions with no backing row).
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
			deps.Logger.Warn("publisher.unsubscribe.unknown_publisher",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String())
			markSubscriptionStopped(ctx, deps, s.ID, s.State)
			continue
		}
		// @constraint: deadline-bounded — Stop runs in the DELETE /
		// terminate request path, and a black-holed publisher must
		// not hang instance termination indefinitely.
		rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
		err := client.Unsubscribe(rpcCtx, s.ID)
		cancel()
		if err != nil {
			deps.Logger.Warn("publisher.unsubscribe.rpc_failed",
				"publisher_name", s.PublisherName,
				"instance_id", instanceID.String(),
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			if mounting {
				// @constraint: stop the reconciler from re-driving a
				// terminated instance. A publisher-side leftover (a
				// Subscribe that landed despite this Unsubscribe
				// failing) is reaped by the reconciler's compensating
				// Unsubscribe when its activation CAS finds the row
				// stopped, or — last resort — by the orphan sweep at
				// the next control-api startup resync.
				markSubscriptionStopped(ctx, deps, s.ID, persistence.PublisherSubscriptionStateMounting)
			}
			// @deliberate: an active row whose Unsubscribe failed stays
			// active here; the startup resync detects that its instance
			// is terminated or deleted, flips the row to stopped, and
			// retries the publisher-side teardown (on DELETE the row is
			// cascade-deleted instead and the orphan sweep reaps the
			// leftover).
			continue
		}
		markSubscriptionStopped(ctx, deps, s.ID, s.State)
	}
	return nil
}

// ResyncPublisherSubscriptions is invoked at control-api startup (the
// publisher registry lives in the control-api process, not the
// supervisor). For each configured publisher it:
//
//  1. Calls `Publisher.ListSubscriptions()` to enumerate what the
//     publisher sees.
//  2. Lists `table:rimsky_publisher_subscriptions` rows in
//     `state = active` and `state = mounting` for that publisher name
//     (mounting rows are desired state too — resync re-drives them, and
//     a mounting row the publisher already reports is flipped straight
//     to active: the Subscribe landed but the flip was lost, e.g. to a
//     control-api crash between the RPC and the row update).
//  3. For subscriptions rimsky expects but the publisher doesn't
//     report, re-issues `Subscribe` to restore; a mounting row flips to
//     active on success. A row whose instance is terminated or deleted
//     is never re-Subscribed or activated — it is flipped to stopped
//     (mirroring the reconciler) and a publisher-side leftover is
//     unsubscribed, which is the retry path for an active row whose
//     terminate-time Unsubscribe failed.
//  4. For orphan subscriptions the publisher reports but rimsky doesn't
//     know about, issues `Unsubscribe` and logs at WARN.
//  5. Recovers `failed` rows whose failure was an unregistered
//     publisher name once that name IS registered: the row is CAS'd
//     back to `mounting` and the reconciler drives it from there. Other
//     `failed` classes (e.g. config-resolve failures) stay failed —
//     they are non-retryable regardless of registry contents.
//
// The resync pass runs concurrently with the reconciler, so its
// snapshot of expected rows can go stale mid-pass. Every action that
// could mis-drive a row created or settled during the window (orphan
// Unsubscribe, re-Subscribe) is therefore preceded by a fresh
// single-row read.
//
// Errors from individual publishers are logged and the sweep continues
// across the remaining set — one broken publisher cannot wedge the rest.
// ListSubscriptions is retried with the same bounded backoff as Subscribe
// so a transient network race at startup (e.g. the publisher is reachable
// at registration but not at the moment resync first fires) does not
// silently skip the publisher's reconcile leg.
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
	// @deliberate: per-pass memo for the terminated-instance checks
	// below — at most one instance Get per distinct instance per
	// sweep.
	goneMemo := map[shared.UUID]bool{}
	for _, client := range deps.Publishers.All() {
		live, err := callListSubscriptionsWithRetry(ctx, client, deps.Logger)
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
		// @constraint: rimsky-expected, publisher-missing → re-
		// Subscribe. A mounting row the publisher already reports
		// flips straight to active. Before any Subscribe, re-read the
		// row — the snapshot was taken once at pass start, and a row
		// stopped mid-pass (instance terminate) must not be re-mounted
		// on the publisher.
		for _, s := range expectedByPublisher[client.Name()] {
			// @constraint: terminated/deleted instance check is first,
			// gating BOTH legs (the fast-path activation and the
			// re-Subscribe). Resync must never mount — or keep mounted
			// — a subscription whose every emit would be rejected with
			// errInstanceTerminated (a permanent dead-letter
			// generator). Mirroring the reconciler, such rows are
			// flipped to stopped; when the publisher still reports the
			// subscription live (the active row whose terminate-time
			// Unsubscribe failed), the teardown is retried with a
			// best-effort Unsubscribe. On a failed instance read, skip
			// the row — never Subscribe blind.
			gone, goneErr := instanceTerminatedOrMissingMemo(ctx, deps, s.InstanceID, goneMemo)
			if goneErr != nil {
				deps.Logger.Warn("publisher.resync.instance_read_failed",
					"publisher_name", client.Name(),
					"instance_id", s.InstanceID.String(),
					"publisher_subscription_id", s.ID.String(),
					"error", goneErr.Error())
				continue
			}
			if gone {
				deps.Logger.Info("publisher.resync.instance_terminated_skip",
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
					markSubscriptionActive(ctx, deps, s.ID)
				}
				continue
			}
			fresh, err := getSubscriptionRow(ctx, deps, s.ID)
			if err != nil {
				deps.Logger.Warn("publisher.resync.row_read_failed",
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
				TargetNode:              fresh.TargetNode,
				MessageType:             fresh.MessageType,
			}
			if err := callSubscribeWithRetry(ctx, client, req, deps.Logger); err != nil {
				deps.Logger.Warn("publisher.resync.subscribe_failed",
					"publisher_name", client.Name(),
					"publisher_subscription_id", s.ID.String(),
					"error", err.Error())
				continue
			}
			// @constraint: post-Subscribe compensation is unconditional
			// across both legs — a concurrent stop/DELETE completing
			// between the fresh read above and the Subscribe leaves a
			// publisher-live subscription with a stopped — or cascade-
			// deleted, hence missing — row. unsubscribeIfRowStopped
			// re-reads and unsubscribes for both the stopped and the
			// missing-row case. The mounting leg keeps its CAS and
			// compensates only when the CAS loses (a successful flip
			// proves the row was still mounting after the Subscribe
			// landed).
			if isMounting {
				if !markSubscriptionActive(ctx, deps, s.ID) {
					unsubscribeIfRowStopped(ctx, deps, client, s.ID)
				}
			} else {
				unsubscribeIfRowStopped(ctx, deps, client, s.ID)
			}
		}
		// @constraint: publisher-reported, rimsky-unknown → Unsubscribe
		// + log. The expected set is a stale snapshot and the
		// reconciler runs concurrently — a subscription created and
		// mounted during the pass is publisher-live but snapshot-
		// absent. Re-read the row before declaring it orphan; a live
		// mounting/active row is legitimate, and Unsubscribing it
		// would leave a dead subscription the instance surface reports
		// healthy.
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
				// @constraint: fail safe — never tear down a possibly-
				// legitimate subscription on a failed read.
				deps.Logger.Warn("publisher.resync.orphan_read_failed",
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
			deps.Logger.Warn("publisher.resync.orphan_subscription",
				"publisher_name", client.Name(),
				"publisher_subscription_id", l.PublisherSubscriptionID.String(),
				"instance_id", l.InstanceID.String(),
				"kind", l.Kind)
			// @constraint: deadline-bounded like every other publisher
			// RPC in the sweep — one black-holed Unsubscribe must not
			// wedge the rest of the orphan sweep.
			rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
			err = client.Unsubscribe(rpcCtx, l.PublisherSubscriptionID)
			cancel()
			if err != nil {
				deps.Logger.Warn("publisher.resync.unsubscribe_orphan_failed",
					"publisher_name", client.Name(),
					"publisher_subscription_id", l.PublisherSubscriptionID.String(),
					"error", err.Error())
			}
		}
	}
	return nil
}

// recoverUnknownPublisherFailures is resync's failed-row recovery leg:
// rows that failed as unknown-publisher recover once the name IS
// registered (an operator adding the publisher to config and
// restarting is the expected path). The reason string is matched
// against the exact unknown-publisher form so other failed classes
// (config-resolve failures — non-retryable regardless of registry
// contents) are never re-driven. Recovered rows are CAS'd
// failed→mounting and returned so the caller's pass drives them like
// any other mounting row.
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
			deps.Logger.Warn("publisher.resync.recover_failed_row",
				"publisher_name", s.PublisherName,
				"publisher_subscription_id", s.ID.String(),
				"error", err.Error())
			continue
		}
		if !flipped {
			continue
		}
		deps.Logger.Info("publisher.resync.recovered_failed_row",
			"publisher_name", s.PublisherName,
			"publisher_subscription_id", s.ID.String())
		s.State = persistence.PublisherSubscriptionStateMounting
		s.FailureReason = ""
		recovered = append(recovered, s)
	}
	return recovered, nil
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

// markSubscriptionFailed flips a mounting row to failed with an
// operator-readable reason (surfaced on the instance-detail API).
// `failed` is reserved for non-retryable errors. Guarded CAS: a row a
// concurrent transition already settled (active / stopped) is left
// alone.
func markSubscriptionFailed(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID, reason string) {
	flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, subID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateFailed, reason)
	if err != nil && deps.Logger != nil {
		deps.Logger.Warn("publisher.markSubscriptionFailed.update_failed",
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
		return
	}
	if flipped && deps.Logger != nil {
		deps.Logger.Warn("publisher.subscribe.failed",
			"publisher_subscription_id", subID.String(),
			"reason", reason)
	}
}

// markSubscriptionActive flips a mounting row to active once the
// publisher Subscribe handshake succeeded. Guarded CAS: a concurrent
// stop/terminate is never overwritten by this late flip. Returns
// whether the flip landed — a false return after a successful
// Subscribe means a concurrent transition settled the row first, and
// the caller decides whether the publisher-side subscription needs a
// compensating Unsubscribe (see unsubscribeIfRowStopped).
func markSubscriptionActive(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) bool {
	flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, subID,
		persistence.PublisherSubscriptionStateMounting,
		persistence.PublisherSubscriptionStateActive, "")
	if err != nil && deps.Logger != nil {
		deps.Logger.Warn("publisher.markSubscriptionActive.update_failed",
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
		return false
	}
	if flipped && deps.Logger != nil {
		deps.Logger.Info("publisher.subscribe.active",
			"publisher_subscription_id", subID.String())
	}
	return flipped
}

// getSubscriptionRow is a fresh single-row read (wrapped in the
// no-nil-tx Transaction contract). Used by the resync sweep and the
// reconciler wherever a stale snapshot could mis-drive a row that a
// concurrent lifecycle transition just settled.
func getSubscriptionRow(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID) (*persistence.PublisherSubscriptionRow, error) {
	var row *persistence.PublisherSubscriptionRow
	err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.PublisherSubscriptions().Get(ctx, tx, subID)
		row = r
		return err
	})
	return row, err
}

// instanceTerminatedOrMissing reports whether a subscription's owning
// instance is terminal (terminated_at set — the same discriminator the
// message-emit path checks before errInstanceTerminated) or deleted.
// The reconciler uses it to flip such rows to stopped instead of
// mounting subscriptions whose every emit would be rejected.
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

// instanceTerminatedOrMissingMemo is instanceTerminatedOrMissing with a
// caller-owned per-pass memo, so a reconciler/resync pass issues at
// most one instance Get per distinct instance rather than one per
// subscription row (the N+1 shape). Failed reads are NOT memoized — the
// caller's per-row skip-and-retry semantics stay unchanged. The memo is
// scoped to one pass; an instance terminating mid-pass is observed by
// the next pass, same as the unmemoized snapshot semantics.
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

// unsubscribeIfRowStopped is the compensating teardown for the
// Subscribe-vs-stop race: a Subscribe RPC in flight when the instance
// terminates can land at the publisher after Stop's Unsubscribe, in
// which case the activation CAS finds the row already stopped (correct)
// but the publisher holds a live subscription. When the fresh row is
// missing or stopped, issue a best-effort second Unsubscribe so the
// leak is reaped at the moment it is created rather than waiting for
// the next control-api startup resync.
func unsubscribeIfRowStopped(ctx context.Context, deps PublisherLifecycleDeps, client PublisherClient, subID shared.UUID) {
	row, err := getSubscriptionRow(ctx, deps, subID)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("publisher.unsubscribe.compensate_read_failed",
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
		}
		return
	}
	if row != nil && row.State != persistence.PublisherSubscriptionStateStopped {
		return
	}
	// @constraint: deadline-bounded like every other publisher RPC on
	// the lifecycle paths — the compensation runs inside reconciler/
	// resync passes and must not wedge them.
	rpcCtx, cancel := context.WithTimeout(ctx, subscribeAttemptTimeout)
	defer cancel()
	if err := client.Unsubscribe(rpcCtx, subID); err != nil && deps.Logger != nil {
		deps.Logger.Warn("publisher.unsubscribe.compensate_failed",
			"publisher_name", client.Name(),
			"publisher_subscription_id", subID.String(),
			"error", err.Error())
	}
}

// markSubscriptionStopped flips a row to stopped using the same guarded
// CAS discipline as every other lifecycle transition, from the state the
// caller observed on the row. A blind Update here could clobber a
// concurrent transition (e.g. overwrite a failed row's state + reason);
// a CAS loss instead means a concurrent transition settled the row
// first, which is non-fatal and logged at Info.
func markSubscriptionStopped(ctx context.Context, deps PublisherLifecycleDeps, subID shared.UUID, fromState string) {
	flipped, err := deps.Persist.PublisherSubscriptions().CompareAndSetState(ctx, subID,
		fromState, persistence.PublisherSubscriptionStateStopped, "")
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("publisher.markSubscriptionStopped.update_failed",
				"publisher_subscription_id", subID.String(),
				"error", err.Error())
		}
		return
	}
	if !flipped && deps.Logger != nil {
		deps.Logger.Info("publisher.markSubscriptionStopped.already_settled",
			"publisher_subscription_id", subID.String(),
			"expected_state", fromState)
	}
}
