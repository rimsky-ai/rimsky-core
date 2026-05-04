// lifecycle_test.go — coverage for the FanOutTemplateEvent /
// FanOutInstanceEvent helpers. Uses storetest.Fake stores as targets
// and a real testcontainer-backed Postgres for the rimsky_lifecycle_idempotency
// bookkeeping table so the SQL behaviour is exercised end-to-end.
package controlapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/internal/pgtest"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// fanOutFixture wires a real persistence.Driver-backed Postgres, two
// storetest.Fake stores ("alpha", "beta") accessible via the registry,
// and the AppDeps the helpers consume.
type fanOutFixture struct {
	deps      AppDeps
	alpha     *storetest.Fake
	beta      *storetest.Fake
	registry  *locks.Registry
	lifecycle *locks.LifecycleRegistry
}

func newFanOutFixture(t *testing.T) *fanOutFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	reg := locks.NewRegistry()
	alpha := storetest.NewFake("alpha", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	beta := storetest.NewFake("beta", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg.Add("alpha", alpha)
	reg.Add("beta", beta)

	// Fake satisfies both ClaimProducer and LifecycleSubscriber; register
	// both alpha and beta as lifecycle subscribers.
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("alpha", alpha)
	lcReg.Add("beta", beta)

	deps := AppDeps{
		Persist:       d.Store(),
		Queue:         d.Queue(),
		Logger:        shared.SilentLogger{},
		Stores:        reg,
		LifecycleSubs: lcReg,
	}
	return &fanOutFixture{
		deps: deps, alpha: alpha, beta: beta, registry: reg, lifecycle: lcReg,
	}
}

// twoStoreSpec returns a TemplateSpec whose nodes reference store "alpha"
// twice (one node per ref) and "beta" once. The dedupe-and-sort path
// must yield ["alpha", "beta"] regardless of the input order.
func twoStoreSpec() node.TemplateSpec {
	return node.TemplateSpec{
		Name: "fan-out-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", Stores: []node.NodeStoreRef{{Name: "beta", Selector: "x", Intent: "r"}}},
			{Type: "n2", Stores: []node.NodeStoreRef{{Name: "alpha", Selector: "y", Intent: "rw"}}},
			{Type: "n3", Stores: []node.NodeStoreRef{{Name: "alpha", Selector: "z", Intent: "r"}}},
		},
	}
}

// TestFanOutTemplateEvent_DedupAndSortedOrder confirms (a) duplicate
// store names collapse to a single per-store call and (b) the surviving
// per-store iteration runs in lexicographic order.
func TestFanOutTemplateEvent_DedupAndSortedOrder(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("a", 64)
	storeNames, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec())
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, storeNames, "must dedupe + sort")

	// Each store saw exactly one OnTemplateRegistered call.
	require.Len(t, f.alpha.Calls(), 1)
	require.Len(t, f.beta.Calls(), 1)
	require.Equal(t, "on_template_registered", f.alpha.Calls()[0].Verb)
	require.Equal(t, hash, f.alpha.Calls()[0].TemplateID)
}

// TestFanOutTemplateEvent_SkipsAlreadyTargetState confirms idempotency:
// re-firing OnTemplateRegistered on a row already at state='registered'
// is a no-op (no second store call, no row churn).
func TestFanOutTemplateEvent_SkipsAlreadyTargetState(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("b", 64)
	_, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec())
	require.NoError(t, err)
	require.Len(t, f.alpha.Calls(), 1)

	// Second fire — both rows are already at target state.
	_, _, err = FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec())
	require.NoError(t, err)
	require.Len(t, f.alpha.Calls(), 1, "second fire must skip when already at target")
	require.Len(t, f.beta.Calls(), 1)
}

// TestFanOutTemplateEvent_PartialFailurePreservesProgress: when the
// second store fails, the first store's bookkeeping row remains
// upserted (so a retry re-fires only the failing store). The error
// must be surfaced and per-store-error map populated. Also asserts
// the deterministic-sort semantics under fan-out: alpha's call must
// be recorded before beta's regardless of input order, verified via
// the FakeCall.Sequence counter.
func TestFanOutTemplateEvent_PartialFailurePreservesProgress(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("c", 64)
	// "beta" sorts after "alpha" → first call goes to alpha, second to beta.
	f.beta.ErrorFunc = func(verb string, _ locks.ClaimID) error {
		if verb == "on_template_registered" {
			return errors.New("simulated beta failure")
		}
		return nil
	}
	_, perStore, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec())
	require.Error(t, err)
	require.NotNil(t, perStore["beta"])
	require.Contains(t, perStore["beta"].Error(), "simulated beta failure")

	// alpha row was committed before beta failed.
	row, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "alpha",
		persistence.LifecycleIdempotencyScopeTemplate, hash, nil)
	require.NoError(t, err)
	require.NotNil(t, row, "alpha row must persist past beta's failure")
	require.Equal(t, persistence.LifecycleIdempotencyStateRegistered, row.State)

	// beta row was never written.
	betaRow, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "beta",
		persistence.LifecycleIdempotencyScopeTemplate, hash, nil)
	require.NoError(t, err)
	require.Nil(t, betaRow)

	// Deterministic-sort assertion: alpha's recorded call must precede
	// beta's, regardless of the spec's input ordering. The
	// twoStoreSpec function deliberately lists beta first to make this
	// test meaningful; the fan-out helper sorts before iterating.
	alphaCalls := f.alpha.Calls()
	betaCalls := f.beta.Calls()
	require.Len(t, alphaCalls, 1, "alpha should have been called once before beta failed")
	require.Len(t, betaCalls, 1, "beta should have been called once (the failing call)")
	require.Less(t, alphaCalls[0].Sequence, betaCalls[0].Sequence,
		"alpha must be dispatched before beta under deterministic sort order")
}

// TestFanOutTemplateEvent_DeregisterDeletesRow exercises the
// deletes-row branch: OnTemplateDeregistered fires on a previously-
// upserted row, the store records the call, the lifecycle row is
// gone afterward.
func TestFanOutTemplateEvent_DeregisterDeletesRow(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("d", 64)
	_, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec())
	require.NoError(t, err)

	_, _, err = FanOutTemplateEvent(ctx, f.deps, EventTemplateDeregistered, hash, twoStoreSpec())
	require.NoError(t, err)

	for _, name := range []string{"alpha", "beta"} {
		row, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, name,
			persistence.LifecycleIdempotencyScopeTemplate, hash, nil)
		require.NoError(t, err)
		require.Nil(t, row, "deregister must delete %q row", name)
	}
}

// TestFanOutInstanceEvent_TerminatedDeletesRow mirrors the template-
// scope deregister test for the instance-scope termination event.
func TestFanOutInstanceEvent_TerminatedDeletesRow(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("e", 64)
	instanceID := "instance-" + repeatHex("f", 16)

	_, _, err := FanOutInstanceEvent(ctx, f.deps, EventInstanceCreated, hash, instanceID, twoStoreSpec())
	require.NoError(t, err)
	row, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "alpha",
		persistence.LifecycleIdempotencyScopeInstance, instanceID, nil)
	require.NoError(t, err)
	require.NotNil(t, row)

	_, _, err = FanOutInstanceEvent(ctx, f.deps, EventInstanceTerminated, hash, instanceID, twoStoreSpec())
	require.NoError(t, err)
	for _, name := range []string{"alpha", "beta"} {
		row, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, name,
			persistence.LifecycleIdempotencyScopeInstance, instanceID, nil)
		require.NoError(t, err)
		require.Nil(t, row, "terminate must delete %q row", name)
	}
}

// repeatHex is a tiny helper for synthesising fake content hashes in tests.
func repeatHex(c string, n int) string {
	return strings.Repeat(c, n)
}
