// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

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
	alpha := storetest.NewFake("alpha", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	beta := storetest.NewFake("beta", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("alpha", alpha)
	reg.Add("beta", beta)

	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("alpha", alpha)
	lcReg.Add("beta", beta)

	deps := AppDeps{
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Logger:        shared.SilentLogger{},
		Stores:        reg,
		LifecycleSubs: lcReg,
	}
	return &fanOutFixture{
		deps: deps, alpha: alpha, beta: beta, registry: reg, lifecycle: lcReg,
	}
}

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

func TestFanOutTemplateEvent_DedupAndSortedOrder(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("a", 64)
	storeNames, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, storeNames, "must dedupe + sort")

	require.Len(t, f.alpha.Calls(), 1)
	require.Len(t, f.beta.Calls(), 1)
	require.Equal(t, "on_template_registered", f.alpha.Calls()[0].Verb)
	require.Equal(t, hash, f.alpha.Calls()[0].TemplateID)
}

func TestFanOutTemplateEvent_SkipsAlreadyTargetState(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("b", 64)
	_, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.NoError(t, err)
	require.Len(t, f.alpha.Calls(), 1)

	_, _, err = FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.NoError(t, err)
	require.Len(t, f.alpha.Calls(), 1, "second fire must skip when already at target")
	require.Len(t, f.beta.Calls(), 1)
}

func TestFanOutTemplateEvent_PartialFailurePreservesProgress(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("c", 64)
	f.beta.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_template_registered" {
			return errors.New("simulated beta failure")
		}
		return nil
	}
	_, perStore, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.Error(t, err)
	require.NotNil(t, perStore["beta"])
	require.Contains(t, perStore["beta"].Error(), "simulated beta failure")

	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "alpha",
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "alpha row must persist past beta's failure")
	require.Equal(t, persistence.LifecycleIdempotencyStateRegistered, row.State)

	var betaRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "beta",
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		betaRow = r
		return err
	}))
	require.Nil(t, betaRow)

	alphaCalls := f.alpha.Calls()
	betaCalls := f.beta.Calls()
	require.Len(t, alphaCalls, 1, "alpha should have been called once before beta failed")
	require.Len(t, betaCalls, 1, "beta should have been called once (the failing call)")
	require.Less(t, alphaCalls[0].Sequence, betaCalls[0].Sequence,
		"alpha must be dispatched before beta under deterministic sort order")
}

func TestFanOutTemplateEvent_DeregisterDeletesRow(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("d", 64)
	_, _, err := FanOutTemplateEvent(ctx, f.deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.NoError(t, err)

	_, _, err = FanOutTemplateEvent(ctx, f.deps, EventTemplateDeregistered, hash, twoStoreSpec(), TemplatePayload{}, nil)
	require.NoError(t, err)

	for _, name := range []string{"alpha", "beta"} {
		var row *persistence.LifecycleIdempotencyRow
		require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, name,
				persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
			row = r
			return err
		}))
		require.Nil(t, row, "deregister must delete %q row", name)
	}
}

func TestFanOutInstanceEvent_TerminatedDeletesRow(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	ctx := context.Background()
	hash := "sha256-" + repeatHex("e", 64)
	instanceID := "instance-" + repeatHex("f", 16)

	_, _, err := FanOutInstanceEvent(ctx, f.deps, EventInstanceCreated, hash, instanceID, twoStoreSpec(), InstancePayload{}, nil)
	require.NoError(t, err)
	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "alpha",
			persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)

	_, _, err = FanOutInstanceEvent(ctx, f.deps, EventInstanceTerminated, hash, instanceID, twoStoreSpec(), InstancePayload{}, nil)
	require.NoError(t, err)
	for _, name := range []string{"alpha", "beta"} {
		var row *persistence.LifecycleIdempotencyRow
		require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, name,
				persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
			row = r
			return err
		}))
		require.Nil(t, row, "terminate must delete %q row", name)
	}
}

func repeatHex(c string, n int) string {
	return strings.Repeat(c, n)
}
