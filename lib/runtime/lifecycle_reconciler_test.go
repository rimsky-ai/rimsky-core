// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type lifecycleDrainFixture struct {
	tables persistence.Tables
	alpha  *storetest.Fake
	beta   *storetest.Fake
	clock  *shared.ControllableClock
	drain  *LifecycleReconciler
}

func newLifecycleDrainFixture(t *testing.T, interval, stallAfter time.Duration) *lifecycleDrainFixture {
	t.Helper()
	d := openSQLiteForOutbox(t)
	alpha := storetest.NewFake("alpha", claimproducer.Capabilities{})
	beta := storetest.NewFake("beta", claimproducer.Capabilities{})
	subs := lifecycle.NewRegistry()
	subs.Add("alpha", alpha)
	subs.Add("beta", beta)
	clockStartPastEveryRowStagedByThisFixture := time.Now().UTC().Add(time.Minute)
	clock := shared.NewControllableClock(clockStartPastEveryRowStagedByThisFixture)
	return &lifecycleDrainFixture{
		tables: d.Tables(),
		alpha:  alpha,
		beta:   beta,
		clock:  clock,
		drain: NewLifecycleReconciler(LifecycleReconcilerConfig{
			Persist:        d.Tables(),
			AdvisoryLocker: d.AdvisoryLocker(),
			Subscribers:    subs,
			Clock:          clock,
			Logger:         shared.SilentLogger{},
			Interval:       interval,
			StallAfter:     stallAfter,
		}),
	}
}

func (f *lifecycleDrainFixture) stageTemplateEvent(t *testing.T, event lifecycle.Event, hash string, services ...string) {
	t.Helper()
	ctx := context.Background()
	sp := spec.TemplateSpec{Name: "drain-test", Version: "v1"}
	for _, name := range services {
		sp.Nodes = append(sp.Nodes, spec.TemplateNodeDef{Type: name, Executor: name})
	}
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, f.tables.LifecycleOutbox(), event, hash, sp, lifecycle.TemplatePayload{}, tx)
	}))
}

func (f *lifecycleDrainFixture) pendingRows(t *testing.T, hash string) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.tables.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeTemplate, hash, tx)
		rows = r
		return err
	}))
	return rows
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleDrain_BlockedStreamDoesNotStarveAnother(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLifecycleDrainFixture(t, time.Second, time.Minute)

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}

	deadHash := "sha256-dead"
	for i := 0; i < lifecycleDrainBatch+20; i++ {
		f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, deadHash, "alpha")
	}
	liveHash := "sha256-live"
	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, liveHash, "beta")

	f.drain.DrainOnce(ctx)

	require.Len(t, f.alpha.Calls(), 1,
		"a stream stops at its first failure rather than retrying the whole backlog in one pass")
	require.Len(t, f.beta.Calls(), 1,
		"beta's one staged delivery lands on the same pass, behind alpha's backlog of more rows than a batch holds")
	require.Empty(t, f.pendingRows(t, liveHash), "beta's delivery leaves no staged row")
	require.Len(t, f.pendingRows(t, deadHash), lifecycleDrainBatch+20, "alpha's backlog stays owed in full")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleDrain_ABatchOfBackedOffStreamsDoesNotStarveALiveOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLifecycleDrainFixture(t, time.Second, time.Minute)

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}
	for i := 0; i < lifecycleDrainBatch; i++ {
		f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, fmt.Sprintf("sha256-dead-%03d", i), "alpha")
	}

	f.drain.DrainOnce(ctx)
	require.Len(t, f.alpha.Calls(), lifecycleDrainBatch,
		"the opening pass attempts one delivery on every stream in a full batch")

	liveHash := "sha256-live-behind-the-backlog"
	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, liveHash, "beta")

	f.drain.DrainOnce(ctx)

	require.Len(t, f.beta.Calls(), 1,
		"a full batch of streams waiting on their next attempt still leaves room for the stream that is due")
	require.Empty(t, f.pendingRows(t, liveHash), "beta's delivery leaves no staged row")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleDrain_FailedDeliveryBlocksItsStreamUntilTheDueTimePasses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const interval = time.Second
	f := newLifecycleDrainFixture(t, interval, 4*time.Second)

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}
	hash := "sha256-backoff"
	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, hash, "alpha")

	f.drain.DrainOnce(ctx)
	rows := f.pendingRows(t, hash)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].AttemptCount, "a failed delivery records its attempt on the row")
	require.Contains(t, rows[0].LastError, "alpha is down", "the row carries the error the last attempt returned")
	require.Equal(t, f.clock.Now().Add(interval), rows[0].NextAttemptAt,
		"the first retry is due one reconciler interval after the failure")

	f.alpha.ErrorFunc = nil
	attemptsSoFar := len(f.alpha.Calls())
	f.drain.DrainOnce(ctx)
	require.Len(t, f.alpha.Calls(), attemptsSoFar,
		"a row whose next attempt is still in the future blocks its stream, so the drain does not call the service again")
	require.Len(t, f.pendingRows(t, hash), 1)

	f.clock.Advance(interval)
	f.drain.DrainOnce(ctx)
	require.Len(t, f.alpha.Calls(), attemptsSoFar+1, "the retry lands on the first pass after the due time passes")
	require.Empty(t, f.pendingRows(t, hash), "the acknowledged row leaves the outbox")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleDrain_BackoffWidensAndStopsAtTheStallThreshold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const interval = time.Second
	const stallAfter = 4 * time.Second
	f := newLifecycleDrainFixture(t, interval, stallAfter)

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}
	hash := "sha256-widening"
	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, hash, "alpha")

	for _, want := range []time.Duration{interval, 2 * interval, stallAfter, stallAfter} {
		start := f.clock.Now()
		f.drain.DrainOnce(ctx)
		rows := f.pendingRows(t, hash)
		require.Len(t, rows, 1)
		require.Equal(t, start.Add(want), rows[0].NextAttemptAt,
			"the retry interval widens from the reconciler interval and stops at the stall threshold")
		f.clock.Advance(want)
	}
}

// @concept: run-scope
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleDrain_DeliversAStagedRunScopeTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLifecycleDrainFixture(t, time.Second, time.Minute)

	templateHash := "sha256-scope-drain"
	sp := spec.TemplateSpec{
		Name: "scope-drain", Version: "v1",
		Nodes: []spec.TemplateNodeDef{{Type: "n1", Executor: "alpha"}},
	}
	instanceID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())
	instanceKey := "ck-" + uuid.NewString()
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: sp, State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		if err := f.tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: runScopeID, GraphName: "main", InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := f.tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, InstanceKey: &instanceKey,
			TargetRoutingIdentity: "test-daemon", Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		return lifecycle.StageRunScopeTerminal(ctx, f.tables, instanceID, runScopeID, "frame_end", nil, tx)
	}))

	f.drain.DrainOnce(ctx)

	calls := f.alpha.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "on_run_scope_terminal", calls[0].Verb,
		"a staged run-scope terminal reaches the subscribing service through the same drain as every other event")

	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.tables.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeRunScope, runScopeID.String(), tx)
		rows = r
		return err
	}))
	require.Empty(t, rows, "the acknowledged run-scope row leaves the outbox")
}

// @decision: lifecycle-drain-per-role
func TestLifecycleDrain_KickDeliversWithoutWaitingForTheInterval(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newLifecycleDrainFixture(t, time.Hour, time.Minute)

	go f.drain.Run(ctx)
	t.Cleanup(f.drain.Stop)

	awaited.Until(t, "the drain's first pass to finish, so only a kick or the interval can wake it again",
		func() bool { return f.drain.PassesCompleted() >= 1 })

	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, "sha256-kicked", "alpha")
	f.drain.Kick()

	awaited.Until(t, "the kicked drain to deliver the row staged after its last pass",
		func() bool { return len(f.alpha.Calls()) == 1 })
}

// @decision: lifecycle-drain-per-role
func TestLifecycleDrain_KickOnAFullChannelDrops(t *testing.T) {
	t.Parallel()
	f := newLifecycleDrainFixture(t, time.Hour, time.Minute)

	for i := 0; i < 100; i++ {
		f.drain.Kick()
	}

	require.Len(t, f.drain.kick, 1, "the kick channel holds one pending wake, and every further kick drops")
}
