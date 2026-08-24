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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
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

func stageAndDeliverTemplateEvent(t *testing.T, f *fanOutFixture, event lifecycle.Event, hash string) map[string]error {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, f.deps.Persist.LifecycleOutbox(), event, hash, twoStoreSpec(), lifecycle.TemplatePayload{}, tx)
	}))
	perService, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(f.deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)
	return perService
}

func stagedTemplateRows(t *testing.T, f *fanOutFixture, hash string) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeTemplate, hash, tx)
		rows = r
		return err
	}))
	return rows
}

func TestTemplateRegistered_ReachesEverySubscriberOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)

	hash := "sha256-" + repeatHex("a", 64)
	perService := stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)
	require.Empty(t, perService)

	require.Len(t, f.alpha.Calls(), 1)
	require.Len(t, f.beta.Calls(), 1)
	require.Equal(t, "on_template_registered", f.alpha.Calls()[0].Verb)
	require.Equal(t, hash, f.alpha.Calls()[0].TemplateHash)
	require.Less(t, f.alpha.Calls()[0].Sequence, f.beta.Calls()[0].Sequence,
		"services are dispatched in the spec's deterministic sort order")
	require.Empty(t, stagedTemplateRows(t, f, hash), "a delivered event leaves no staged row")
}

func TestTemplateRegistered_OneFailingSubscriberLeavesOnlyItsOwnDeliveryOwed(t *testing.T) {
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
	perService := stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)
	require.NotNil(t, perService["beta"])
	require.Contains(t, perService["beta"].Error(), "simulated beta failure")

	staged := stagedTemplateRows(t, f, hash)
	require.Len(t, staged, 1, "only the failed delivery stays owed")
	require.Equal(t, "beta", staged[0].ClaimProducerName)
	require.Equal(t, lifecycle.EventTemplateRegistered.String(), staged[0].Event)

	f.beta.ErrorFunc = nil
	perService, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(f.deps),
		persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)
	require.Empty(t, perService)
	require.Len(t, f.alpha.Calls(), 1, "alpha's row left the outbox, so a second pass does not call it again")
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
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)
	require.Len(t, f.alpha.Calls(), 1)

	f.alpha.ErrorFunc = nil
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateDeployed, hash)

	verbs := make([]string, 0, len(f.alpha.Calls()))
	for _, c := range f.alpha.Calls() {
		verbs = append(verbs, c.Verb)
	}
	require.Equal(t, []string{"on_template_registered", "on_template_registered", "on_template_deployed"}, verbs,
		"the superseded registration is still owed, and it arrives before the deploy")
	require.Empty(t, stagedTemplateRows(t, f, hash))
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
		_, err := f.deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
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

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func TestCloseAndStageRunScopeTerminals_DedupesSharedRootScope(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := seedInstanceForRunScopeFanout(t, f, uuid.NewString())
	seedClosedFramesWithScopes(ctx, t, f, instanceID, 3, true)

	closed := stageRunScopeTerminalsForInstance(t, f, shared.UUID(instanceID))
	require.Len(t, closed, 1,
		"3 frames sharing one root run scope stage on_run_scope_terminal exactly once (seen-map dedupe)")
	require.Len(t, stagedRunScopeRows(t, f, closed[0]), 2, "one row per subscribing service")
}

// @concept: run-scope
// @decision: lifecycle-fanout-after-commit
func TestCloseAndStageRunScopeTerminals_PaginatesAcrossFramePages(t *testing.T) {
	f := newFanOutFixture(t)
	ctx := context.Background()

	instanceID := seedInstanceForRunScopeFanout(t, f, uuid.NewString())
	const n = 257
	scopes := seedClosedFramesWithScopes(ctx, t, f, instanceID, n, false)
	require.Len(t, scopes, n)

	closed := stageRunScopeTerminalsForInstance(t, f, shared.UUID(instanceID))
	require.Len(t, closed, n,
		"all %d distinct root run scopes, spanning more than one internal 256-row page, are staged", n)
}

func stageRunScopeTerminalsForInstance(t *testing.T, f *fanOutFixture, instanceID shared.UUID) []shared.UUID {
	t.Helper()
	ctx := context.Background()
	var closed []shared.UUID
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		scopes, err := closeAndStageRunScopeTerminalsInTx(ctx, f.deps, instanceID, instanceTerminatedReason, tx)
		closed = scopes
		return err
	}))
	return closed
}

func stagedRunScopeRows(t *testing.T, f *fanOutFixture, scopeID shared.UUID) []persistence.LifecycleOutboxRow {
	t.Helper()
	ctx := context.Background()
	var rows []persistence.LifecycleOutboxRow
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := f.deps.Persist.LifecycleOutbox().ListPendingForScope(ctx,
			persistence.LifecycleScopeRunScope, scopeID.String(), tx)
		rows = r
		return err
	}))
	return rows
}

// @concept: advisory-lock
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestStagedDelivery_TwoReplicasSharingOneDBDeliverOneStagedRowOnce(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	replicaAlpha := storetest.NewFake("alpha", claimproducer.Capabilities{})
	replicaReg := lifecycle.NewRegistry()
	replicaReg.Add("alpha", replicaAlpha)
	replicaDeps := f.deps
	replicaDeps.LifecycleSubs = replicaReg

	hash := "sha256-" + repeatHex("3", 64)
	stageTemplateEventForSpec(t, f, lifecycle.EventTemplateRegistered, hash, oneStoreSpec("alpha"))

	rows := stagedTemplateRows(t, f, hash)
	require.Len(t, rows, 1)
	row := rows[0]

	const racersPerReplica = 4
	replicas := []AppDeps{f.deps, replicaDeps}
	var wg sync.WaitGroup
	for i := 0; i < racersPerReplica; i++ {
		for _, deps := range replicas {
			deps := deps
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = runtime.DeliverStagedLifecycleRow(ctx, lifecycleDeliveryDeps(deps), row)
			}()
		}
	}
	wg.Wait()

	require.Equal(t, 1, len(f.alpha.Calls())+len(replicaAlpha.Calls()),
		"racing drains over one staged row (separate subscriber registries, shared database, no shared process "+
			"memory) converge on one delivery through the per-scope lock, not one per racer")
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

func oneStoreSpec(service string) node.TemplateSpec {
	return node.TemplateSpec{
		Name: "one-store-test", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []node.NodeClaimProducerRef{{Name: service, Selector: "x", Intent: "r"}}},
		},
	}
}

func stageTemplateEventForSpec(t *testing.T, f *fanOutFixture, event lifecycle.Event, hash string, spec node.TemplateSpec) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, f.deps.Persist.LifecycleOutbox(), event, hash, spec, lifecycle.TemplatePayload{}, tx)
	}))
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateEvents_ARestagedEventArrivesAfterTheEventsStagedBetweenIt(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	hash := "sha256-" + repeatHex("f", 64)
	spec := oneStoreSpec("alpha")
	stageTemplateEventForSpec(t, f, lifecycle.EventTemplateDeployed, hash, spec)
	stageTemplateEventForSpec(t, f, lifecycle.EventTemplateUndeployed, hash, spec)
	stageTemplateEventForSpec(t, f, lifecycle.EventTemplateDeployed, hash, spec)

	perService, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(f.deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)
	require.Empty(t, perService)

	verbs := make([]string, 0, len(f.alpha.Calls()))
	for _, c := range f.alpha.Calls() {
		verbs = append(verbs, c.Verb)
	}
	require.Equal(t,
		[]string{"on_template_deployed", "on_template_undeployed", "on_template_deployed"}, verbs,
		"a re-staged event arrives after the events staged between the two stagings, "+
			"so the subscriber's last-heard state matches the template's")
	require.Empty(t, stagedTemplateRows(t, f, hash))
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
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateDeployed, hash)
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateUndeployed, hash)
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateDeregistered, hash)

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
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateUndeployed, hash)

	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.deps.Persist.Templates().DeleteByHash(ctx, hash, tx); err != nil {
			return err
		}
		return lifecycle.StageTemplateEvent(ctx, f.deps.Persist.LifecycleOutbox(), lifecycle.EventTemplateDeregistered, hash, twoStoreSpec(), lifecycle.TemplatePayload{}, tx)
	}))
	_, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(f.deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)

	for _, name := range []string{"alpha", "beta"} {
		require.Equalf(t, 1, lifecycleVerbCount(f, name, "on_template_deregistered", hash),
			"%s must hear the deregister. The handler removes the template row and stages the event in one "+
				"transaction, so the staged row carries everything the delivery needs after the row is gone", name)
	}
	require.Empty(t, stagedTemplateRows(t, f, hash))
}

// @concept: lifecycle-subscriber
// @decision: lifecycle-subscriber-at-least-once-delivery
func TestTemplateLifecycle_DeregisterDropsTheStagedRowOfAnUnregisteredService(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	hash := "sha256-" + repeatHex("e", 64)
	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.deps.Persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: twoStoreSpec(), State: persistence.TemplateStateUndeployed, Source: "direct",
		}, tx)
	}))
	stageAndDeliverTemplateEvent(t, f, lifecycle.EventTemplateRegistered, hash)

	onlyAlpha := lifecycle.NewRegistry()
	onlyAlpha.Add("alpha", f.alpha)
	deps := f.deps
	deps.LifecycleSubs = onlyAlpha

	require.NoError(t, f.deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.deps.Persist.Templates().DeleteByHash(ctx, hash, tx); err != nil {
			return err
		}
		return lifecycle.StageTemplateEvent(ctx, f.deps.Persist.LifecycleOutbox(), lifecycle.EventTemplateDeregistered, hash, twoStoreSpec(), lifecycle.TemplatePayload{}, tx)
	}))
	_, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)

	require.Equal(t, 1, lifecycleVerbCount(f, "alpha", "on_template_deregistered", hash),
		"the service that is still registered must hear the deregister")
	require.Empty(t, stagedTemplateRows(t, f, hash),
		"beta is no longer a registered subscriber, so nothing will ever deliver its staged event; the delivery "+
			"attempt drops the row rather than blocking beta's stream for ever")
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
func TestStagedDelivery_ReportsEveryDeliveryItMakes(t *testing.T) {
	t.Parallel()
	f := newFanOutFixture(t)
	ctx := context.Background()

	recorder := &recordingLifecycleLogger{}
	deps := f.deps
	deps.Logger = recorder

	hash := "sha256-" + repeatHex("7", 64)
	require.NoError(t, deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, deps.Persist.LifecycleOutbox(), lifecycle.EventTemplateRegistered, hash, twoStoreSpec(), lifecycle.TemplatePayload{}, tx)
	}))
	_, err := runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)

	require.NoError(t, deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return lifecycle.StageTemplateEvent(ctx, deps.Persist.LifecycleOutbox(), lifecycle.EventTemplateDeregistered, hash, twoStoreSpec(), lifecycle.TemplatePayload{}, tx)
	}))
	_, err = runtime.DeliverStagedLifecycleScope(ctx, lifecycleDeliveryDeps(deps), persistence.LifecycleScopeTemplate, hash)
	require.NoError(t, err)

	delivered := recorder.linesOfKind("LIFECYCLE.STAGEDDELIVERY.DELIVERED")
	byEvent := map[string]recordedLine{}
	for _, line := range delivered {
		if line.fields["service"] == "alpha" {
			byEvent[line.fields["event"]] = line
		}
	}

	_, sawOpening := byEvent[lifecycle.EventTemplateRegistered.String()]
	require.True(t, sawOpening, "the opening event reports its delivery")

	_, sawClosing := byEvent[lifecycle.EventTemplateDeregistered.String()]
	require.True(t, sawClosing,
		"the closing event reports its delivery too. An operator reading the feed sees a line when a service "+
			"is told and a line when a delivery is skipped, so silence means rimsky did neither")
}
