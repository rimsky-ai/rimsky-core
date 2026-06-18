// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type gateFixture struct {
	d       persistence.Database
	tables  persistence.Tables
	args    RunArgs
	inst    *persistence.InstanceRow
	frameID shared.UUID
	scopeID shared.UUID
	nodes   map[string]persistence.NodeRow
	runs    map[string]shared.UUID
}

func makeGateFixture(t *testing.T, nodes []tmplspec.TemplateNodeDef) gateFixture {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:           "upstream-gate-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes:          nodes,
	}

	fx := gateFixture{
		d:       d,
		tables:  tables,
		scopeID: mainScopeID,
		nodes:   map[string]persistence.NodeRow{},
		runs:    map[string]shared.UUID{},
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		inst, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		fx.inst = &inst
		for _, def := range nodes {
			row, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: instanceID,
				NodeType: def.Type, Executor: def.Executor,
			}, tx)
			if err != nil {
				return err
			}
			fx.nodes[def.Type] = row
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertFrame(ctx, instanceID, msgID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := tables.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		fx.frameID = frameID
		for _, def := range nodes {
			runID := shared.UUID(uuid.New())
			if err := tables.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
				RunID: runID, NodeID: fx.nodes[def.Type].ID, FrameID: frameID,
				RunScopeID: mainScopeID, ExecutorName: def.Executor,
			}); err != nil {
				return err
			}
			fx.runs[def.Type] = runID
		}
		return nil
	}); err != nil {
		t.Fatalf("seed gate fixture: %v", err)
	}

	fx.args = RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-upstream-gate",
	}
	return fx
}

func (fx gateFixture) gated(t *testing.T, nodeType string) bool {
	t.Helper()
	nd := fx.nodes[nodeType]
	cand := persistence.Candidate{
		DispatchID: fx.runs[nodeType],
		NodeID:     nd.ID,
		NodeType:   nodeType,
		FrameID:    fx.frameID,
	}
	var out bool
	if err := fx.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		g, err := candidateGatedByInFlightUpstream(ctx, fx.args, tx, &nd, fx.inst, cand, fx.scopeID)
		if err != nil {
			return err
		}
		out = g
		return nil
	}); err != nil {
		t.Fatalf("candidateGatedByInFlightUpstream(%s): %v", nodeType, err)
	}
	return out
}

func (fx gateFixture) setRunPhase(t *testing.T, nodeType, phase string) {
	t.Helper()
	rawDB := sqlitedrv.DBFromDatabase(fx.d)
	if _, err := rawDB.Exec(
		`UPDATE rimsky_node_runs SET phase = ? WHERE id = ?`,
		phase, fx.runs[nodeType].String(),
	); err != nil {
		t.Fatalf("set phase %s on %s: %v", phase, nodeType, err)
	}
}

func (fx gateFixture) lowestNodeType(members ...string) string {
	lowest := members[0]
	for _, m := range members[1:] {
		a := fx.nodes[m].ID
		b := fx.nodes[lowest].ID
		if bytes.Compare(a[:], b[:]) < 0 {
			lowest = m
		}
	}
	return lowest
}

func threeCycleNodes() []tmplspec.TemplateNodeDef {
	return []tmplspec.TemplateNodeDef{
		{Type: "alpha", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "beta", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
		{Type: "beta", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "gamma", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
		{Type: "gamma", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "alpha", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
	}
}

func TestUpstreamGate_PendingThreeCycle_LowestNodeIDDispatches(t *testing.T) {
	t.Parallel()
	fx := makeGateFixture(t, threeCycleNodes())

	members := []string{"alpha", "beta", "gamma"}
	lowest := fx.lowestNodeType(members...)
	passed := 0
	for _, m := range members {
		g := fx.gated(t, m)
		if m == lowest && g {
			t.Errorf("byte-wise-lowest cycle member %q must pass the gate (liveness), got gated", m)
		}
		if m != lowest && !g {
			t.Errorf("non-lowest cycle member %q must stay gated until the winner resolves", m)
		}
		if !g {
			passed++
		}
	}
	if passed != 1 {
		t.Errorf("exactly one member of a pending-only 3-cycle must dispatch, got %d", passed)
	}
}

func TestUpstreamGate_ThreeCycle_RunningSenderGatesEveryone(t *testing.T) {
	t.Parallel()
	fx := makeGateFixture(t, threeCycleNodes())

	fx.setRunPhase(t, "gamma", "active")

	for _, m := range []string{"alpha", "beta"} {
		if !fx.gated(t, m) {
			t.Errorf("%q must stay gated while a cycle member is genuinely running", m)
		}
	}
	if !fx.gated(t, "gamma") {
		t.Errorf("gamma's own rows are not pending-only; the tie-breaker must not apply and gamma must stay gated")
	}
}

type phaseInjectQueue struct {
	persistence.Queue
	nodeID shared.UUID
	phase  string
}

func (q phaseInjectQueue) ListInFlightRunPhases(
	ctx context.Context, tx persistence.Tx, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID,
) (map[shared.UUID][]string, error) {
	m, err := q.Queue.ListInFlightRunPhases(ctx, tx, nodeIDs, frameID, runScopeID)
	if err != nil {
		return nil, err
	}
	for _, id := range nodeIDs {
		if id == q.nodeID {
			m[id] = append(m[id], q.phase)
		}
	}
	return m, nil
}

func TestUpstreamGate_MixedPhaseCycleMember_NoDualPass(t *testing.T) {
	t.Parallel()
	fx := makeGateFixture(t, threeCycleNodes())

	members := []string{"alpha", "beta", "gamma"}
	lowest := fx.lowestNodeType(members...)
	fx.args.Queue = phaseInjectQueue{
		Queue:  fx.args.Queue,
		nodeID: fx.nodes[lowest].ID,
		phase:  "parked",
	}

	passed := 0
	for _, m := range members {
		g := fx.gated(t, m)
		if m == lowest && !g {
			t.Errorf("mixed-phase member %q (pending+parked) must be gated even as the byte-wise-lowest cycle member", m)
		}
		if !g {
			passed++
		}
	}
	if passed != 0 {
		t.Errorf("a cycle holding a mixed-phase member admits no dispatch until it resolves; got %d members passing", passed)
	}
}

func TestUpstreamGate_PendingSenderOutsideCycleStillGates(t *testing.T) {
	t.Parallel()
	nodes := threeCycleNodes()
	nodes[0].Subscribes = append(nodes[0].Subscribes,
		tmplspec.SubscriptionEntry{Node: "delta", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)})
	nodes = append(nodes, tmplspec.TemplateNodeDef{Type: "delta", Executor: "stub"})
	fx := makeGateFixture(t, nodes)

	if !fx.gated(t, "alpha") {
		t.Errorf("alpha must gate on the merely-pending out-of-cycle sender delta, regardless of the cycle tie-break")
	}
	if fx.gated(t, "delta") {
		t.Errorf("delta has no subscribed upstreams and must not be gated")
	}
}
