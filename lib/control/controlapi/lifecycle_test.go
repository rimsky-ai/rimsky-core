// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type fanOutFixture struct {
	deps      AppDeps
	driver    persistence.Database
	alpha     *storetest.Fake
	beta      *storetest.Fake
	lifecycle *lifecycle.Registry
}

func newFanOutFixture(t *testing.T) *fanOutFixture {
	t.Helper()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	reg := locks.NewRegistry()
	alpha := storetest.NewFake("alpha", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	beta := storetest.NewFake("beta", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("alpha", alpha)
	reg.Add("beta", beta)

	lcReg := lifecycle.NewRegistry()
	lcReg.Add("alpha", alpha)
	lcReg.Add("beta", beta)

	deps := AppDeps{
		Persist:        d.Tables(),
		AdvisoryLocker: d.AdvisoryLocker(),
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

func stageAndDeliverTemplateEvent(t *testing.T, f *fanOutFixture, event LifecycleEvent, hash string) map[string]error {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return StageTemplateLifecycleEvent(ctx, f.deps, event, hash, twoStoreSpec(), TemplatePayload{}, tx)
	}))
	perPeer, err := deliverStagedLifecycleScope(ctx, f.deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)
	return perPeer
}

func templateLedgerRow(t *testing.T, f *fanOutFixture, peer, hash string) *persistence.LifecycleIdempotencyRow {
	t.Helper()
	ctx := context.Background()
	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleIdempotency().Get(ctx, peer,
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		row = r
		return err
	}))
	return row
}

func stagedTemplateRows(t *testing.T, f *fanOutFixture, hash string) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleIdempotencyScopeTemplate, hash, tx)
		rows = r
		return err
	}))
	return rows
}

func TestTemplateRegistered_ReachesEverySubscriberOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("a", 64)
	perPeer := stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.Empty(t, perPeer)

	require.Len(t, f.alpha.Calls(), 1)
	require.Len(t, f.beta.Calls(), 1)
	require.Equal(t, "on_template_registered", f.alpha.Calls()[0].Verb)
	require.Equal(t, hash, f.alpha.Calls()[0].TemplateHash)
	require.Less(t, f.alpha.Calls()[0].Sequence, f.beta.Calls()[0].Sequence,
		"peers are dispatched in the spec's deterministic sort order")
	require.Empty(t, stagedTemplateRows(t, f, hash), "a delivered event leaves no staged row")
}

func TestTemplateRegistered_ReplayOfADeliveredEventCallsNoSubscriber(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("b", 64)
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.Len(t, f.alpha.Calls(), 1)

	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.Len(t, f.alpha.Calls(), 1, "the ledger absorbs a replay of a delivery it already recorded")
	require.Len(t, f.beta.Calls(), 1)
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

func TestTemplateRegistered_OneFailingSubscriberLeavesOnlyItsOwnDeliveryOwed(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("c", 64)
	f.beta.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_template_registered" {
			return errors.New("simulated beta failure")
		}
		return nil
	}
	perPeer := stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.NotNil(t, perPeer["beta"])
	require.Contains(t, perPeer["beta"].Error(), "simulated beta failure")

	alphaRow := templateLedgerRow(t, f, "alpha", hash)
	require.NotNil(t, alphaRow, "alpha's ledger row must persist past beta's failure")
	require.Equal(t, persistence.LifecycleIdempotencyStateRegistered, alphaRow.State)
	require.Nil(t, templateLedgerRow(t, f, "beta", hash))

	staged := stagedTemplateRows(t, f, hash)
	require.Len(t, staged, 1, "only the failed delivery stays owed")
	require.Equal(t, "beta", staged[0].ClaimProducerName)
	require.Equal(t, EventTemplateRegistered.String(), staged[0].Event)

	f.beta.ErrorFunc = nil
	perPeer = stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.Empty(t, perPeer)
	require.Len(t, f.alpha.Calls(), 1, "alpha is not called again for an event it already took")
	require.Len(t, f.beta.Calls(), 2, "beta receives the redelivery")
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

func TestTemplateEvents_AFailedPredecessorIsDeliveredBeforeItsSuccessor(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("e", 64)
	f.alpha.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_template_registered" {
			return errors.New("simulated alpha failure")
		}
		return nil
	}
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	require.Len(t, f.alpha.Calls(), 1)

	f.alpha.ErrorFunc = nil
	stageAndDeliverTemplateEvent(t, f, EventTemplateDeployed, hash)

	verbs := make([]string, 0, len(f.alpha.Calls()))
	for _, c := range f.alpha.Calls() {
		verbs = append(verbs, c.Verb)
	}
	require.Equal(t, []string{"on_template_registered", "on_template_registered", "on_template_deployed"}, verbs,
		"the superseded registration is still owed, and it arrives before the deploy")
	require.Empty(t, stagedTemplateRows(t, f, hash))

	row := templateLedgerRow(t, f, "alpha", hash)
	require.NotNil(t, row)
	require.Equal(t, persistence.LifecycleIdempotencyStateDeployed, row.State)
}

func TestTemplateDeregistered_DeletesEveryLedgerRow(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("d", 64)
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	stageAndDeliverTemplateEvent(t, f, EventTemplateDeregistered, hash)

	for _, name := range []string{"alpha", "beta"} {
		require.Nil(t, templateLedgerRow(t, f, name, hash), "deregister must delete %q row", name)
	}
	require.Empty(t, stagedTemplateRows(t, f, hash))
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
		_, err := f.deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
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
	pgdbtest.ExecForTest(ctx, t, f.driver, scopeSQL.String(), scopeArgs...)

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
	pgdbtest.ExecForTest(ctx, t, f.driver, msgSQL.String(), msgArgs...)

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
	pgdbtest.ExecForTest(ctx, t, f.driver, frameSQL.String(), frameArgs...)

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
			ClaimProducerName: "alpha",
			ScopeKind:         persistence.LifecycleIdempotencyScopeRunScope,
			ScopeID:           scopeID.String(),
			State:             persistence.LifecycleIdempotencyStateRunScopeTerminal,
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

// @concept: lifecycle-subscriber
func TestFanOutRunScopeEvent_TwoReplicasSharingOneDBDeliverExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	replicaAlpha := storetest.NewFake("alpha", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	replicaReg := lifecycle.NewRegistry()
	replicaReg.Add("alpha", replicaAlpha)
	replicaDeps := AppDeps{
		Persist:        f.driver.Tables(),
		AdvisoryLocker: f.driver.AdvisoryLocker(),
		Queue:          f.driver.Queue(),
		Logger:         shared.SilentLogger{},
		ClaimProducers: f.deps.ClaimProducers,
		LifecycleSubs:  replicaReg,
	}

	instanceID := shared.UUID(uuid.New())
	scopeID := shared.UUID(uuid.New())
	peers := []string{"alpha"}

	const racersPerReplica = 4
	replicas := []AppDeps{f.deps, replicaDeps}
	racerErrs := make([]error, racersPerReplica*len(replicas))
	racerPeerErrs := make([]map[string]error, racersPerReplica*len(replicas))
	var wg sync.WaitGroup
	for i := 0; i < racersPerReplica; i++ {
		for j, deps := range replicas {
			slot := i*len(replicas) + j
			deps := deps
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, perPeerErr, err := fanOutRunScopeEventForPeers(ctx, deps, peers, scopeID, instanceID, "instance_terminated", nil)
				racerErrs[slot] = err
				racerPeerErrs[slot] = perPeerErr
			}()
		}
	}
	wg.Wait()

	for slot := range racerErrs {
		require.NoError(t, racerErrs[slot])
		require.Empty(t, racerPeerErrs[slot])
	}

	total := len(f.alpha.Calls()) + len(replicaAlpha.Calls())
	require.Equal(t, 1, total,
		"racing fan-outs from two replicas (separate subscriber registries, shared database, no shared "+
			"process memory) must converge to exactly one on_run_scope_terminal delivery via the "+
			"DB-level lifecycle scope lock, not one per replica")
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

func oneStoreSpec(peer string) node.TemplateSpec {
	return node.TemplateSpec{
		Name: "one-store-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []node.NodeClaimProducerRef{{Name: peer, Selector: "x", Intent: "r"}}},
		},
	}
}

func stageTemplateEventForSpec(t *testing.T, f *fanOutFixture, event LifecycleEvent, hash string, spec node.TemplateSpec) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return StageTemplateLifecycleEvent(ctx, f.deps, event, hash, spec, TemplatePayload{}, tx)
	}))
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateEvents_ARestagedEventArrivesAfterTheEventsStagedBetweenIt(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	hash := "sha256-" + repeatHex("f", 64)
	spec := oneStoreSpec("alpha")
	stageTemplateEventForSpec(t, f, EventTemplateDeployed, hash, spec)
	stageTemplateEventForSpec(t, f, EventTemplateUndeployed, hash, spec)
	stageTemplateEventForSpec(t, f, EventTemplateDeployed, hash, spec)

	perPeer, err := deliverStagedLifecycleScope(ctx, f.deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)
	require.Empty(t, perPeer)

	verbs := make([]string, 0, len(f.alpha.Calls()))
	for _, c := range f.alpha.Calls() {
		verbs = append(verbs, c.Verb)
	}
	require.Equal(t,
		[]string{"on_template_deployed", "on_template_undeployed", "on_template_deployed"}, verbs,
		"a re-staged event arrives after the events staged between the two stagings, "+
			"so the subscriber's last-heard state matches the template's")

	row := templateLedgerRow(t, f, "alpha", hash)
	require.NotNil(t, row)
	require.Equal(t, persistence.LifecycleIdempotencyStateDeployed, row.State,
		"the subscriber ends on the state the last staged event names")
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestLifecycleReconciler_ABlockedStreamDoesNotStarveAnother(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}

	deadHash := "sha256-" + repeatHex("1", 64)
	for i := 0; i < reconcileBatch+20; i++ {
		stageTemplateEventForSpec(t, f, EventTemplateDeployed, deadHash, oneStoreSpec("alpha"))
	}
	liveHash := "sha256-" + repeatHex("2", 64)
	stageTemplateEventForSpec(t, f, EventTemplateRegistered, liveHash, oneStoreSpec("beta"))

	NewLifecycleReconciler(f.deps, time.Hour).tick(ctx)

	require.Len(t, f.alpha.Calls(), 1,
		"a stream stops at its first failure rather than retrying the whole backlog in one tick")
	require.Len(t, f.beta.Calls(), 1,
		"beta's one staged delivery lands on the same tick, behind alpha's backlog of "+
			"more rows than a batch holds")
	require.Equal(t, "on_template_registered", f.beta.Calls()[0].Verb)
	require.Empty(t, stagedTemplateRows(t, f, liveHash), "beta's delivery leaves no staged row")
	require.Len(t, stagedTemplateRows(t, f, deadHash), reconcileBatch+20,
		"alpha's backlog stays owed in full")
}

func lifecycleVerbCount(f *fanOutFixture, name, verb, hash string) int {
	fake := f.alpha
	if name == "beta" {
		fake = f.beta
	}
	n := 0
	for _, call := range fake.Calls() {
		if call.Verb == verb && call.TemplateHash == hash {
			n++
		}
	}
	return n
}

// @concept: lifecycle-subscriber
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateLifecycle_DeregisterReachesEverySubscriberAfterAnUndeploy(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("a", 64)
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	stageAndDeliverTemplateEvent(t, f, EventTemplateDeployed, hash)
	stageAndDeliverTemplateEvent(t, f, EventTemplateUndeployed, hash)
	stageAndDeliverTemplateEvent(t, f, EventTemplateDeregistered, hash)

	for _, name := range []string{"alpha", "beta"} {
		require.Equalf(t, 1, lifecycleVerbCount(f, name, "on_template_undeployed", hash),
			"%s must hear the undeploy exactly once", name)
		require.Equalf(t, 1, lifecycleVerbCount(f, name, "on_template_deregistered", hash),
			"%s must hear the deregister; a template walked through its whole life owes every subscriber "+
				"the closing event, and dropping it leaves the subscriber holding substrate for a template "+
				"that no longer exists", name)
	}
}

// @concept: lifecycle-subscriber
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateLifecycle_DeregisterSurvivesTheTemplateRowItRemoves(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	hash := "sha256-" + repeatHex("b", 64)
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.deps.Persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: twoStoreSpec(), State: persistence.TemplateStateUndeployed, Source: "direct",
		}, tx)
	}))
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	stageAndDeliverTemplateEvent(t, f, EventTemplateUndeployed, hash)

	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.deps.Persist.Templates().DeleteByHash(ctx, hash, tx); err != nil {
			return err
		}
		return StageTemplateLifecycleEvent(ctx, f.deps, EventTemplateDeregistered, hash, twoStoreSpec(), TemplatePayload{}, tx)
	}))
	_, err := deliverStagedLifecycleScope(ctx, f.deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)

	for _, name := range []string{"alpha", "beta"} {
		require.Equalf(t, 1, lifecycleVerbCount(f, name, "on_template_deregistered", hash),
			"%s must hear the deregister. The handler removes the template row and stages the event in one "+
				"transaction, so removing the row must leave the ledger row the delivery reads to decide "+
				"whether the peer is owed the closing event", name)
		require.Nil(t, templateLedgerRow(t, f, name, hash),
			"the delivery deletes %q ledger row once the peer has heard the deregister", name)
	}
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

// @concept: lifecycle-subscriber
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateLifecycle_DeregisterReclaimsTheLedgerRowOfAnUnregisteredPeer(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	hash := "sha256-" + repeatHex("e", 64)
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.deps.Persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: twoStoreSpec(), State: persistence.TemplateStateUndeployed, Source: "direct",
		}, tx)
	}))
	stageAndDeliverTemplateEvent(t, f, EventTemplateRegistered, hash)
	for _, name := range []string{"alpha", "beta"} {
		require.NotNil(t, templateLedgerRow(t, f, name, hash), "%s must hold a ledger row before the peer leaves", name)
	}

	onlyAlpha := lifecycle.NewRegistry()
	onlyAlpha.Add("alpha", f.alpha)
	deps := f.deps
	deps.LifecycleSubs = onlyAlpha

	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.deps.Persist.Templates().DeleteByHash(ctx, hash, tx); err != nil {
			return err
		}
		return StageTemplateLifecycleEvent(ctx, f.deps, EventTemplateDeregistered, hash, twoStoreSpec(), TemplatePayload{}, tx)
	}))
	_, err := deliverStagedLifecycleScope(ctx, deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)

	require.Equal(t, 1, lifecycleVerbCount(f, "alpha", "on_template_deregistered", hash),
		"the peer that is still registered must hear the deregister")
	require.Nil(t, templateLedgerRow(t, f, "beta", hash),
		"beta is no longer a registered subscriber, so nothing will ever deliver its staged event. Its ledger "+
			"row names a template that no longer exists. No sweep covers this table, so the delivery attempt "+
			"is the only thing that can reclaim the row")
	require.Nil(t, templateLedgerRow(t, f, "alpha", hash))
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

type recordingLifecycleLogger struct {
	mu    sync.Mutex
	lines []recordedLine
}

type recordedLine struct {
	kind   string
	fields map[string]string
}

func (r *recordingLifecycleLogger) record(msg string, fields ...any) {
	line := recordedLine{kind: msg, fields: map[string]string{}}
	for i := 0; i+1 < len(fields); i += 2 {
		line.fields[fmt.Sprint(fields[i])] = fmt.Sprint(fields[i+1])
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func (r *recordingLifecycleLogger) Debug(msg string, fields ...any) { r.record(msg, fields...) }
func (r *recordingLifecycleLogger) Info(msg string, fields ...any)  { r.record(msg, fields...) }
func (r *recordingLifecycleLogger) Warn(msg string, fields ...any)  { r.record(msg, fields...) }
func (r *recordingLifecycleLogger) Error(msg string, fields ...any) { r.record(msg, fields...) }
func (r *recordingLifecycleLogger) With(...any) shared.Logger       { return r }

func (r *recordingLifecycleLogger) linesOfKind(kind string) []recordedLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedLine
	for _, line := range r.lines {
		if line.kind == kind {
			out = append(out, line)
		}
	}
	return out
}

// @concept: lifecycle-subscriber
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestStagedDelivery_ReportsEverySuccessIncludingTheOnesThatCloseTheLedger(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	recorder := &recordingLifecycleLogger{}
	deps := f.deps
	deps.Logger = recorder

	hash := "sha256-" + repeatHex("7", 64)
	require.NoError(t, deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return StageTemplateLifecycleEvent(ctx, deps, EventTemplateRegistered, hash, twoStoreSpec(), TemplatePayload{}, tx)
	}))
	_, err := deliverStagedLifecycleScope(ctx, deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)

	require.NoError(t, deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return StageTemplateLifecycleEvent(ctx, deps, EventTemplateDeregistered, hash, twoStoreSpec(), TemplatePayload{}, tx)
	}))
	_, err = deliverStagedLifecycleScope(ctx, deps, persistence.LifecycleIdempotencyScopeTemplate, hash)
	require.NoError(t, err)

	delivered := recorder.linesOfKind("lifecycle.staged_delivered")
	byEvent := map[string]recordedLine{}
	for _, line := range delivered {
		if line.fields["peer"] == "alpha" {
			byEvent[line.fields["event"]] = line
		}
	}

	opening, sawOpening := byEvent[EventTemplateRegistered.String()]
	require.True(t, sawOpening, "an event that advances the ledger reports its delivery")
	require.Equal(t, string(persistence.LifecycleIdempotencyStateRegistered), opening.fields["ledger_disposition"])

	closing, sawClosing := byEvent[EventTemplateDeregistered.String()]
	require.True(t, sawClosing,
		"an event that closes the ledger reports its delivery too. An operator reading the feed sees a line "+
			"when a peer is told and a line when a delivery is skipped, so silence means rimsky did neither")
	require.Equal(t, ledgerDispositionDeleted, closing.fields["ledger_disposition"])
}
