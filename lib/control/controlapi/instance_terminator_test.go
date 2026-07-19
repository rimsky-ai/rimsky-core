// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type terminatorFixture struct {
	deps     AppDeps
	driver   persistence.Database
	persist  persistence.Tables
	alpha    *storetest.Fake
	registry *locks.Registry
}

func newTerminatorFixture(t *testing.T) *terminatorFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	reg := locks.NewRegistry()
	alpha := storetest.NewFake("alpha", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("alpha", alpha)
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("alpha", alpha)
	deps := AppDeps{
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Logger:         shared.SilentLogger{},
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
	}
	return &terminatorFixture{
		deps: deps, driver: d, persist: d.Tables(), alpha: alpha,
		registry: reg,
	}
}

func seedTerminatedInstance(t *testing.T, f *terminatorFixture, storeName string, withTemplate bool) (templateHash string, instanceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	templateHash = "sha256-" + repeatHex("a", 32) + uuid.NewString()[:32]
	spec := node.TemplateSpec{
		Name: "term-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:           "n1",
			ClaimProducers: []node.NodeClaimProducerRef{{Name: storeName, Selector: "x", Intent: "r"}},
		}},
	}
	instanceID = uuid.New()
	mainScopeID := uuid.New()
	ck := "ck-" + uuid.NewString()
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: spec, State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		if err := f.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := f.persist.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, InstanceKey: &ck,
			Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		msgID := uuid.New()
		if err := f.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		if _, err := f.persist.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx); err != nil {
			return err
		}
		if err := f.persist.Instances().MarkTerminated(ctx, instanceID, tx); err != nil {
			return err
		}
		return f.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: storeName,
			ScopeKind:             persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:               instanceID.String(),
			State:                 persistence.LifecycleIdempotencyStateCreated,
		}, tx)
	}))

	if !withTemplate {
		pgtest.ExecForTest(ctx, t, f.driver,
			`ALTER TABLE rimsky_instances DROP CONSTRAINT IF EXISTS rimsky_instances_template_hash_fkey`)
		pgtest.ExecForTest(ctx, t, f.driver,
			`DELETE FROM rimsky_templates WHERE id = $1`, templateHash)
	}
	return templateHash, instanceID
}

func TestInstanceTerminator_RowFoundRPCSucceedsRowDeleted(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	hash, inst := seedTerminatedInstance(t, f, "alpha", true)

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	calls := f.alpha.Calls()
	require.Len(t, calls, 2)
	var runScopeCall, terminatedCall *storetest.FakeCall
	for i := range calls {
		switch calls[i].Verb {
		case "on_run_scope_terminal":
			runScopeCall = &calls[i]
		case "on_instance_terminated":
			terminatedCall = &calls[i]
		}
	}
	require.NotNil(t, runScopeCall, "OnRunScopeTerminal must fire for the main run-scope")
	require.NotEmpty(t, runScopeCall.RunScopeID)
	require.NotNil(t, terminatedCall, "OnInstanceTerminated must fire")
	require.Equal(t, hash, terminatedCall.TemplateID)
	require.Equal(t, inst.String(), terminatedCall.InstanceID)
	require.Less(t, runScopeCall.Sequence, terminatedCall.Sequence,
		"OnRunScopeTerminal must fire before OnInstanceTerminated")

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.Nil(t, row, "lifecycle row must be deleted on success")
}

func TestInstanceTerminator_RowFoundRPCFailsRowPreserved(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", true)
	f.alpha.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_instance_terminated" {
			return errors.New("simulated alpha failure")
		}
		return nil
	}

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "lifecycle row must survive a per-store failure")
}

func TestInstanceTerminator_FailedTickRetriedToSuccessOnLaterTick(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", true)
	f.alpha.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_instance_terminated" {
			return errors.New("simulated alpha failure")
		}
		return nil
	}

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "row must survive the first, failing tick")

	f.alpha.ErrorFunc = nil
	term.tick(context.Background())

	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.Nil(t, row, "a second tick after the error clears must deliver and delete the row")

	var terminatedCalls int
	for _, c := range f.alpha.Calls() {
		if c.Verb == "on_instance_terminated" {
			terminatedCalls++
		}
	}
	require.Equal(t, 2, terminatedCalls, "on_instance_terminated must be re-attempted on the following tick until it succeeds")
}

func TestInstanceTerminator_RunScopeFailureBlocksInstanceTerminatedRowDeletion(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", true)
	f.alpha.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_run_scope_terminal" {
			return errors.New("simulated run-scope failure")
		}
		return nil
	}

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	var sawRunScope, sawTerminated bool
	for _, c := range f.alpha.Calls() {
		switch c.Verb {
		case "on_run_scope_terminal":
			sawRunScope = true
		case "on_instance_terminated":
			sawTerminated = true
		}
	}
	require.True(t, sawRunScope, "on_run_scope_terminal must be attempted")
	require.False(t, sawTerminated,
		"on_instance_terminated must not fire in the same tick when on_run_scope_terminal failed for this peer; "+
			"firing it anyway would delete the instance-scope row and drop the instance from the terminator's "+
			"retry candidate set forever, losing the run-scope-terminal delivery")

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "the instance-scope row must survive so the instance remains in the terminator's retry set")

	f.alpha.ErrorFunc = nil
	term.tick(context.Background())

	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.Nil(t, row, "once on_run_scope_terminal succeeds on retry, on_instance_terminated must fire and delete the row")
}

func TestInstanceTerminator_MultiStorePartialFailureInSameTick(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	beta := storetest.NewFake("beta", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	f.registry.Add("beta", beta)
	f.deps.LifecycleSubs.Add("beta", beta)
	beta.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_instance_terminated" {
			return errors.New("simulated beta failure")
		}
		return nil
	}

	ctx := context.Background()
	templateHash := "sha256-" + repeatHex("f", 32) + uuid.NewString()[:32]
	spec := node.TemplateSpec{
		Name: "term-multi-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type: "n1",
			ClaimProducers: []node.NodeClaimProducerRef{
				{Name: "alpha", Selector: "x", Intent: "r"},
				{Name: "beta", Selector: "y", Intent: "r"},
			},
		}},
	}
	instanceID := uuid.New()
	mainScopeID := uuid.New()
	ck := "ck-" + uuid.NewString()
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: spec, State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		if err := f.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := f.persist.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash, InstanceKey: &ck,
			Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		msgID := uuid.New()
		if err := f.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		if _, err := f.persist.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx); err != nil {
			return err
		}
		if err := f.persist.Instances().MarkTerminated(ctx, instanceID, tx); err != nil {
			return err
		}
		for _, name := range []string{"alpha", "beta"} {
			if err := f.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             persistence.LifecycleIdempotencyScopeInstance,
				ScopeID:               instanceID.String(),
				State:                 persistence.LifecycleIdempotencyStateCreated,
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(ctx)

	var alphaRow, betaRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, instanceID.String(), tx)
		alphaRow = r
		return err
	}))
	require.NoError(t, f.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.LifecycleIdempotency().Get(ctx,
			"beta", persistence.LifecycleIdempotencyScopeInstance, instanceID.String(), tx)
		betaRow = r
		return err
	}))

	require.Nil(t, alphaRow, "the succeeding peer's row must be deleted even though a later peer in the same fan-out failed")
	require.NotNil(t, betaRow, "the failing peer's row must be preserved for retry")
}

func TestInstanceTerminator_TickRacesConcurrentDirectFanOut(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	hash, inst := seedTerminatedInstance(t, f, "alpha", true)

	var tpl *persistence.TemplateRow
	require.NoError(t, f.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.persist.Templates().GetByHash(ctx, hash, tx)
		tpl = r
		return err
	}))
	require.NotNil(t, tpl)

	term := NewInstanceTerminator(f.deps, time.Hour)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		term.tick(context.Background())
	}()
	go func() {
		defer wg.Done()
		_, _, _ = FanOutInstanceEvent(context.Background(), f.deps,
			EventInstanceTerminated, hash, inst.String(), tpl.Spec,
			InstancePayload{}, nil)
	}()
	wg.Wait()

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.Nil(t, row,
		"a background tick racing with a handler-triggered direct fan-out on the same instance must converge "+
			"to the row being deleted, not left dangling")

	var terminatedCalls int
	for _, c := range f.alpha.Calls() {
		if c.Verb == "on_instance_terminated" {
			terminatedCalls++
		}
	}
	require.GreaterOrEqual(t, terminatedCalls, 1, "at least one of the racing paths must have delivered on_instance_terminated")
	require.LessOrEqual(t, terminatedCalls, 2, "at most one dispatch per racing caller")
}

func TestInstanceTerminator_TemplateMissingFallsBackToLifecycleRows(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", false)
	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	calls := f.alpha.Calls()
	require.Len(t, calls, 1, "fallback path must fire OnInstanceTerminated against the lifecycle-row store")
	require.Equal(t, "on_instance_terminated", calls[0].Verb)

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx,
			"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), tx)
		row = r
		return err
	}))
	require.Nil(t, row, "fallback path must delete the lifecycle row on success")
}

func TestInstanceTerminator_RunExitsOnContextCancel(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	term := NewInstanceTerminator(f.deps, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		term.Run(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit promptly after context cancel")
	}
}

func TestInstanceTerminator_StopBoundedByBudget(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	term := NewInstanceTerminator(f.deps, time.Hour)
	start := time.Now()
	term.Stop()
	require.Less(t, time.Since(start), 100*time.Millisecond)

	term2 := NewInstanceTerminator(f.deps, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go term2.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	start = time.Now()
	term2.Stop()
	require.Less(t, time.Since(start), 1*time.Second,
		"Stop must complete promptly when the goroutine is alive and exits cleanly")
}
