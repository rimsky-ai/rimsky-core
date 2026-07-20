// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

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

type fanOutFixture struct {
	deps      AppDeps
	driver    persistence.Database
	alpha     *storetest.Fake
	beta      *storetest.Fake
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
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Logger:         shared.SilentLogger{},
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
	}
	return &fanOutFixture{
		deps: deps, driver: d, alpha: alpha, beta: beta, lifecycle: lcReg,
	}
}

func twoStoreSpec() node.TemplateSpec {
	return node.TemplateSpec{
		Name: "fan-out-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []node.NodeClaimProducerRef{{Name: "beta", Selector: "x", Intent: "r"}}},
			{Type: "n2", ClaimProducers: []node.NodeClaimProducerRef{{Name: "alpha", Selector: "y", Intent: "rw"}}},
			{Type: "n3", ClaimProducers: []node.NodeClaimProducerRef{{Name: "alpha", Selector: "z", Intent: "r"}}},
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

func seedInstanceForRunScopeFanout(t *testing.T, f *fanOutFixture, suffix string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	hash := "sha256-" + repeatHex("9", 32) + uuid.NewString()[:32]
	instanceID := uuid.New()
	ck := "ck-" + suffix
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.deps.Persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: twoStoreSpec(), State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		_, err := f.deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: shared.UUID(instanceID), TemplateHash: hash, InstanceKey: &ck,
			Params: map[string]any{},
		}, tx)
		return err
	}))
	return instanceID
}

func seedClosedFramesWithScopes(ctx context.Context, t *testing.T, f *fanOutFixture, instanceID uuid.UUID, n int, shareOneScope bool) []uuid.UUID {
	t.Helper()

	scopeIDs := make([]uuid.UUID, n)
	if shareOneScope {
		sharedScope := uuid.New()
		for i := range scopeIDs {
			scopeIDs[i] = sharedScope
		}
	} else {
		for i := range scopeIDs {
			scopeIDs[i] = uuid.New()
		}
	}

	distinctScopes := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, s := range scopeIDs {
		if !seen[s] {
			seen[s] = true
			distinctScopes = append(distinctScopes, s)
		}
	}

	var scopeSQL strings.Builder
	scopeSQL.WriteString("INSERT INTO rimsky_run_scopes(id, graph_name, instance_id, partition_key, created_at) VALUES ")
	var scopeArgs []any
	for i, id := range distinctScopes {
		if i > 0 {
			scopeSQL.WriteString(",")
		}
		fmt.Fprintf(&scopeSQL, "($%d,'main',$%d,'',now())", len(scopeArgs)+1, len(scopeArgs)+2)
		scopeArgs = append(scopeArgs, id, instanceID)
	}
	pgtest.ExecForTest(ctx, t, f.driver, scopeSQL.String(), scopeArgs...)

	msgIDs := make([]uuid.UUID, n)
	for i := range msgIDs {
		msgIDs[i] = uuid.New()
	}
	var msgSQL strings.Builder
	msgSQL.WriteString("INSERT INTO rimsky_messages(id, instance_id, type, sender_kind, sender, payload, received_at) VALUES ")
	var msgArgs []any
	for i, id := range msgIDs {
		if i > 0 {
			msgSQL.WriteString(",")
		}
		fmt.Fprintf(&msgSQL, "($%d,$%d,'',$%d,$%d,E'{}'::bytea,now())",
			len(msgArgs)+1, len(msgArgs)+2, len(msgArgs)+3, len(msgArgs)+4)
		msgArgs = append(msgArgs, id, instanceID, "operator", "test")
	}
	pgtest.ExecForTest(ctx, t, f.driver, msgSQL.String(), msgArgs...)

	var frameSQL strings.Builder
	frameSQL.WriteString("INSERT INTO rimsky_frames(frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at, ended_at, last_progress_at) VALUES ")
	var frameArgs []any
	for i := 0; i < n; i++ {
		if i > 0 {
			frameSQL.WriteString(",")
		}
		fmt.Fprintf(&frameSQL, "(gen_random_uuid(),$%d,$%d,$%d,now(),now(),now())",
			len(frameArgs)+1, len(frameArgs)+2, len(frameArgs)+3)
		frameArgs = append(frameArgs, instanceID, msgIDs[i], scopeIDs[i])
	}
	pgtest.ExecForTest(ctx, t, f.driver, frameSQL.String(), frameArgs...)

	return distinctScopes
}

func TestPeersReferencedBySpec_IncludesPublishers(t *testing.T) {
	t.Parallel()

	spec := node.TemplateSpec{
		Name: "lifecycle-publishers", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []node.NodeClaimProducerRef{{Name: "alpha"}}},
			{Type: "n2", Executor: "beta"},
		},
		Publishers: []node.PublisherSpec{
			{Name: "gamma", Kind: "cron"},
			{Name: "", Kind: "cron"},
			{Name: "alpha", Kind: "cron"},
		},
	}

	got := peersReferencedBySpec(spec)

	require.Equal(t, []string{"alpha", "beta", "gamma"}, got,
		"publisher-only services must be enumerated; empty names skipped; dedup with node refs preserved")
}

func TestFanOutRunScopeEvent_SkipsAlreadyTerminalPeer(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.deps.Persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: "alpha",
			ScopeKind:             persistence.LifecycleIdempotencyScopeRunScope,
			ScopeID:               scopeID.String(),
			State:                 persistence.LifecycleIdempotencyStateRunScopeTerminal,
		}, tx)
	}))

	peers, perPeerErr, err := fanOutRunScopeEventForPeers(ctx, f.deps, LifecyclePeersForSpec(f.deps, twoStoreSpec()), scopeID, instanceID, "instance_terminated", nil)
	require.NoError(t, err)
	require.Empty(t, perPeerErr)
	require.Equal(t, []string{"alpha", "beta"}, peers)

	require.Empty(t, f.alpha.Calls(),
		"a peer already marked run-scope-terminal for this scope must not be re-dispatched")
	require.Len(t, f.beta.Calls(), 1,
		"a peer not yet marked terminal for this scope must still be dispatched")
	require.Equal(t, "on_run_scope_terminal", f.beta.Calls()[0].Verb)
}

func TestFanOutRunScopeEvent_ContinuesPastPeerFailureNilErrorWithPerPeerErr(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())
	f.beta.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_run_scope_terminal" {
			return errors.New("simulated beta failure")
		}
		return nil
	}

	_, perPeerErr, err := fanOutRunScopeEventForPeers(ctx, f.deps, LifecyclePeersForSpec(f.deps, twoStoreSpec()), scopeID, instanceID, "instance_terminated", nil)
	require.NoError(t, err,
		"a per-peer dispatch failure must not fail the overall call; only perPeerErr carries it (continue-on-error contract)")
	require.Len(t, perPeerErr, 1)
	require.Contains(t, perPeerErr["beta"].Error(), "simulated beta failure")

	require.Len(t, f.alpha.Calls(), 1, "alpha must still be dispatched despite beta's failure")
	require.Len(t, f.beta.Calls(), 1, "beta's failing call must still be attempted")

	var alphaRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "alpha",
			persistence.LifecycleIdempotencyScopeRunScope, scopeID.String(), tx)
		alphaRow = r
		return err
	}))
	require.NotNil(t, alphaRow, "alpha's success must be persisted")
	require.Equal(t, persistence.LifecycleIdempotencyStateRunScopeTerminal, alphaRow.State)

	var betaRow *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, "beta",
			persistence.LifecycleIdempotencyScopeRunScope, scopeID.String(), tx)
		betaRow = r
		return err
	}))
	require.Nil(t, betaRow, "beta's failed dispatch must not be marked terminal (so a future re-fanout retries it)")
}

func TestFanOutRunScopeEvent_ConcurrentCallsDeliverExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, _, _ = fanOutRunScopeEventForPeers(ctx, f.deps, LifecyclePeersForSpec(f.deps, twoStoreSpec()), scopeID, instanceID, "instance_terminated", nil)
		}()
	}
	wg.Wait()

	require.Len(t, f.alpha.Calls(), 1,
		"two racing fan-outs for the same run-scope must converge to exactly one on_run_scope_terminal "+
			"delivery per peer, not one per racing caller")
	require.Len(t, f.beta.Calls(), 1,
		"two racing fan-outs for the same run-scope must converge to exactly one on_run_scope_terminal "+
			"delivery per peer, not one per racing caller")
}

func TestCloseAndFanOutRunScopesForInstance_DedupesSharedRootScope(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := seedInstanceForRunScopeFanout(t, f, uuid.NewString())
	seedClosedFramesWithScopes(ctx, t, f, instanceID, 3, true)

	err := CloseAndFanOutRunScopesForInstance(ctx, f.deps, twoStoreSpec(), shared.UUID(instanceID), "instance_terminated")
	require.NoError(t, err)

	require.Len(t, f.alpha.Calls(), 1,
		"3 frames sharing one root run scope must fan out on_run_scope_terminal exactly once (seen-map dedupe)")
	require.Len(t, f.beta.Calls(), 1)
}

func TestCloseAndFanOutRunScopesForInstance_PaginatesAcrossFramePages(t *testing.T) {
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := seedInstanceForRunScopeFanout(t, f, uuid.NewString())
	const n = 257
	scopes := seedClosedFramesWithScopes(ctx, t, f, instanceID, n, false)
	require.Len(t, scopes, n)

	err := CloseAndFanOutRunScopesForInstance(ctx, f.deps, twoStoreSpec(), shared.UUID(instanceID), "instance_terminated")
	require.NoError(t, err)

	require.Len(t, f.alpha.Calls(), n,
		"all %d distinct root run scopes, spanning more than one internal 256-row page, must be fanned out", n)
	require.Len(t, f.beta.Calls(), n)
}

func TestLifecyclePeersForSpec_AppendsLateBindProxiesDedupedSkippingEmpty(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		LateBindServiceProxies: map[string]string{
			"svc-a": "gamma-proxy",
			"svc-b": "alpha",
			"svc-c": "",
		},
	}
	spec := node.TemplateSpec{
		Name: "late-bind-peers", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []node.NodeClaimProducerRef{{Name: "alpha"}}},
			{Type: "n2", Executor: "beta"},
		},
		LateBindServices: []string{"svc-a"},
	}

	got := LifecyclePeersForSpec(deps, spec)

	require.Equal(t, []string{"alpha", "beta", "gamma-proxy"}, got,
		"a late-bind-declaring spec must append configured proxy peers, deduped against spec-referenced peers, skipping empty proxy names")
}

func TestLifecyclePeersForSpec_NoProxiesWhenSpecHasNoLateBindServices(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		LateBindServiceProxies: map[string]string{"svc-a": "gamma-proxy"},
	}
	spec := node.TemplateSpec{
		Name: "no-late-bind", Version: "v1",
		Nodes: []node.TemplateNodeDef{{Type: "n1", Executor: "beta"}},
	}

	got := LifecyclePeersForSpec(deps, spec)

	require.Equal(t, []string{"beta"}, got,
		"a spec with no LateBindServices must not pull in any configured proxy peers")
}

func TestLifecyclePeersForSpec_ProxyOrderDeterministicAcrossMultipleProxies(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		LateBindServiceProxies: map[string]string{
			"svc-z": "proxy-z",
			"svc-a": "proxy-a",
			"svc-m": "proxy-m",
		},
	}
	spec := node.TemplateSpec{
		Name: "late-bind-order", Version: "v1",
		LateBindServices: []string{"svc-a"},
	}

	want := LifecyclePeersForSpec(deps, spec)
	require.Equal(t, []string{"proxy-a", "proxy-m", "proxy-z"}, want,
		"proxies must be appended in sorted service-name order, not random map-iteration order")

	for i := 0; i < 20; i++ {
		got := LifecyclePeersForSpec(deps, spec)
		require.Equal(t, want, got,
			"proxy peer order must be deterministic across repeated calls")
	}
}
