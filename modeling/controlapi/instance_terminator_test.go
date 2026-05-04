// instance_terminator_test.go — coverage for the control-api
// background worker that fires OnInstanceTerminated against the stores
// recorded in rimsky_lifecycle_idempotency. Drives a real testcontainer-
// backed Postgres + storetest.Fake stores.
package controlapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/internal/pgtest"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

type terminatorFixture struct {
	deps     AppDeps
	driver   persistence.Driver
	persist  persistence.Store
	alpha    *storetest.Fake
	registry *locks.Registry
}

func newTerminatorFixture(t *testing.T) *terminatorFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	reg := locks.NewRegistry()
	alpha := storetest.NewFake("alpha", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg.Add("alpha", alpha)
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("alpha", alpha)
	deps := AppDeps{
		Persist:       d.Store(),
		Queue:         d.Queue(),
		Logger:        shared.SilentLogger{},
		Stores:        reg,
		LifecycleSubs: lcReg,
	}
	return &terminatorFixture{
		deps: deps, driver: d, persist: d.Store(), alpha: alpha,
		registry: reg,
	}
}

// seedTerminatedInstance inserts a rimsky_templates row in 'deployed'
// state, an rimsky_instances row whose terminated_at is non-NULL, and
// a rimsky_lifecycle_idempotency row at scope='instance' so the terminator
// has work to do. When withTemplate is false the template row is
// removed after the instance is created so the lookup misses.
func seedTerminatedInstance(t *testing.T, f *terminatorFixture, storeName string, withTemplate bool) (templateHash string, instanceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	// Use a unique hash per call so parallel tests don't collide on the
	// rimsky_templates PK.
	templateHash = "sha256-" + repeatHex("a", 32) + uuid.NewString()[:32]
	spec := node.TemplateSpec{
		Name: "term-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:   "n1",
			Stores: []node.NodeStoreRef{{Name: storeName, Selector: "x", Intent: "r"}},
		}},
	}
	require.NoError(t, f.persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: templateHash, Spec: spec, State: persistence.TemplateStateDeployed,
	}, nil))

	instanceID = uuid.New()
	ck := "ck-" + uuid.NewString()
	_, err := f.persist.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID: instanceID, TemplateHash: templateHash, InstanceKey: &ck,
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	require.NoError(t, f.persist.Instances().MarkTerminated(ctx, instanceID, nil))
	require.NoError(t, f.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
		StoreRegistrationName: storeName,
		ScopeKind:             persistence.LifecycleIdempotencyScopeInstance,
		ScopeID:               instanceID.String(),
		State:                 persistence.LifecycleIdempotencyStateCreated,
	}, nil))

	if !withTemplate {
		// Drop the FK constraint so we can null/replace the binding,
		// then delete the template row to simulate a force-deleted
		// template.
		pgtest.ExecForTest(ctx, t, f.driver,
			`ALTER TABLE rimsky_instances DROP CONSTRAINT IF EXISTS rimsky_instances_template_hash_fkey`)
		pgtest.ExecForTest(ctx, t, f.driver,
			`DELETE FROM rimsky_templates WHERE id = $1`, templateHash)
	}
	return templateHash, instanceID
}

// TestInstanceTerminator_RowFoundRPCSucceedsRowDeleted: happy path —
// terminated instance with a template + a lifecycle row → tick fires
// OnInstanceTerminated, store records the call, lifecycle row is gone.
func TestInstanceTerminator_RowFoundRPCSucceedsRowDeleted(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	hash, inst := seedTerminatedInstance(t, f, "alpha", true)

	term := NewInstanceTerminator(f.deps, time.Hour) // poll long; we drive tick directly.
	term.tick(context.Background())

	calls := f.alpha.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "on_instance_terminated", calls[0].Verb)
	require.Equal(t, hash, calls[0].TemplateID)
	require.Equal(t, inst.String(), calls[0].InstanceID)

	row, err := f.deps.Persist.LifecycleIdempotency().Get(context.Background(),
		"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), nil)
	require.NoError(t, err)
	require.Nil(t, row, "lifecycle row must be deleted on success")
}

// TestInstanceTerminator_RowFoundRPCFailsRowPreserved: RPC failure
// must leave the lifecycle row in place so the next tick retries.
func TestInstanceTerminator_RowFoundRPCFailsRowPreserved(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", true)
	f.alpha.ErrorFunc = func(verb string, _ locks.ClaimID) error {
		if verb == "on_instance_terminated" {
			return errors.New("simulated alpha failure")
		}
		return nil
	}

	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	row, err := f.deps.Persist.LifecycleIdempotency().Get(context.Background(),
		"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), nil)
	require.NoError(t, err)
	require.NotNil(t, row, "lifecycle row must survive a per-store failure")
}

// TestInstanceTerminator_TemplateMissingFallsBackToLifecycleRows:
// when the template row is gone the terminator falls back to firing
// OnInstanceTerminated against every store named in the lifecycle
// rows, then deletes those rows directly. Issue 7's fix.
func TestInstanceTerminator_TemplateMissingFallsBackToLifecycleRows(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	_, inst := seedTerminatedInstance(t, f, "alpha", false)
	term := NewInstanceTerminator(f.deps, time.Hour)
	term.tick(context.Background())

	calls := f.alpha.Calls()
	require.Len(t, calls, 1, "fallback path must fire OnInstanceTerminated against the lifecycle-row store")
	require.Equal(t, "on_instance_terminated", calls[0].Verb)

	row, err := f.deps.Persist.LifecycleIdempotency().Get(context.Background(),
		"alpha", persistence.LifecycleIdempotencyScopeInstance, inst.String(), nil)
	require.NoError(t, err)
	require.Nil(t, row, "fallback path must delete the lifecycle row on success")
}

// TestInstanceTerminator_RunExitsOnContextCancel verifies the loop
// exits promptly when its context is cancelled.
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
	// Give the goroutine a chance to enter its loop, then cancel.
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

// TestInstanceTerminator_StopBoundedByBudget — Stop must return
// promptly when the goroutine drains cleanly. The stopBudget cap
// prevents wedged-RPC scenarios from blocking shutdown indefinitely.
func TestInstanceTerminator_StopBoundedByBudget(t *testing.T) {
	t.Parallel()
	f := newTerminatorFixture(t)

	// Started=false → Stop is a no-op fast path.
	term := NewInstanceTerminator(f.deps, time.Hour)
	start := time.Now()
	term.Stop()
	require.Less(t, time.Since(start), 100*time.Millisecond)

	// Started=true, goroutine drains cleanly via context-cancel.
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
