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
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Logger:        shared.SilentLogger{},
		Stores:        reg,
		LifecycleSubs: lcReg,
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
			Type:   "n1",
			Stores: []node.NodeStoreRef{{Name: storeName, Selector: "x", Intent: "r"}},
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
			Params:         map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx); err != nil {
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
