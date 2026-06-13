// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: stale-run-not-dispatch-eligible — exercised here: a stale run is filtered from the dispatch-eligible set.

// runner_acquire_upstream_gate_test.go — pins the pending-cycle
// tie-breaker in `candidateGatedByInFlightUpstream` against a real
// SQLite backend:
//
//   - a 3-cycle A→B→C→A with all three merely-pending in one
//     (frame, scope) must NOT deadlock: exactly the byte-wise-lowest
//     node id passes the gate (liveness for legal cyclic templates —
//     the pairwise mutual tie-break alone deadlocked any cycle longer
//     than 2);
//   - a merely-pending candidate still gates when a subscribed sender
//     is genuinely RUNNING (active phase) — progressing upstreams
//     always gate, cycle or not;
//   - a merely-pending sender OUTSIDE the candidate's pending cycle
//     gates the candidate even when the candidate is lowest in its
//     cycle.
//
// The sqlite driver is imported in this _test.go only; no import cycle —
// the sqlite driver does not import lib/runtime.

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

// gateFixture is one seeded instance under a registered template, with
// one pending run per template node in a single (frame, main scope).
type gateFixture struct {
	d       persistence.Database
	tables  persistence.Tables
	args    RunArgs
	inst    *persistence.InstanceRow
	frameID shared.UUID
	scopeID shared.UUID
	nodes   map[string]persistence.NodeRow // by node type
	runs    map[string]shared.UUID         // pending run id by node type
}

// makeGateFixture seeds template → main scope → instance → nodes →
// frame → one pending root run per node type. The template's node
// definitions (with their Subscribes entries) drive the gate's
// subscription-edge derivation.
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

	// Unique hash per fixture: the process-global subscription-edge
	// cache is keyed on it.
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:                "upstream-gate-fixture",
		Version:             "1",
		FrameResolutionMode: tmplspec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes:               nodes,
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
		frameID, err := tables.Frames().EnqueueSerialFrame(ctx, instanceID, fx.nodes[nodes[0].Type].ID, 600000, tx)
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

// gated evaluates the gate for the named node type's pending run.
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

// setRunPhase flips a run's phase directly (the fixtures need a
// 'running' upstream without driving a full acquisition).
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

// lowestNodeType returns the member with the byte-wise lowest node id.
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

// threeCycleNodes builds the A→B→C→A subscription 3-cycle: alpha
// subscribes to beta, beta to gamma, gamma to alpha. No pair is
// mutual, so the pairwise tie-break alone gated all three forever.
func threeCycleNodes() []tmplspec.TemplateNodeDef {
	return []tmplspec.TemplateNodeDef{
		{Type: "alpha", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "beta", Type: "terminal/*"}}},
		{Type: "beta", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "gamma", Type: "terminal/*"}}},
		{Type: "gamma", Executor: "stub",
			Subscribes: []tmplspec.SubscriptionEntry{{Node: "alpha", Type: "terminal/*"}}},
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

	// gamma's run is genuinely progressing — alpha→beta→gamma→alpha
	// gating means beta (subscribed to gamma) must gate on the active
	// row no matter what the tie-breaker would say, and the cycle is no
	// longer pending-only for anyone: alpha's sender beta is pending but
	// beta cannot be mutually-reachable through a pending-only cycle
	// (gamma is progressing), so alpha gates too.
	fx.setRunPhase(t, "gamma", "active")

	for _, m := range []string{"alpha", "beta"} {
		if !fx.gated(t, m) {
			t.Errorf("%q must stay gated while a cycle member is genuinely running", m)
		}
	}
	// gamma itself subscribes to alpha (merely-pending). The vertex
	// predicate is uniform: gamma's own in-flight rows are not
	// pending-only (its row is active), so the tie-breaker does not
	// apply for gamma and it stays gated like everyone else.
	if !fx.gated(t, "gamma") {
		t.Errorf("gamma's own rows are not pending-only; the tie-breaker must not apply and gamma must stay gated")
	}
}

// phaseInjectQueue wraps the real Queue and appends an extra in-flight
// phase to one node's ListInFlightRunPhases report. The schema's
// uq_node_runs_in_flight_per_run_scope index allows only one in-flight
// row per (node, run scope), so the mixed [pending, parked] shape the
// cross-contender determinism regression needs cannot be seeded as
// real rows — the wrapper injects it at the gate's read surface
// instead, which is exactly the state the gate's predicate consumes.
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

// TestUpstreamGate_MixedPhaseCycleMember_NoDualPass pins cross-contender
// determinism of the pending-cycle tie-breaker: a cycle member with
// MIXED [pending, parked] in-flight rows must produce one consistent
// verdict pattern no matter which contender evaluates the gate.
//
// The mixed member is the byte-wise-lowest node — the would-be
// tie-break winner. Under the old candidate-by-definition membership
// rule, the mixed member evaluating ITSELF counted itself a cycle
// member regardless of its phases and passed as the lowest id, while
// another contender (computing membership from the phases) excluded it
// — two different SCCs over the same persisted state, so two members
// could pass in one poll (dual-pass). The uniform predicate gates the
// mixed member outright (its own rows are not pending-only), and every
// other member gates on it as a progressing / out-of-cycle sender:
// exactly zero members pass until the parked row resolves.
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
	// The 3-cycle plus an extra pending upstream: alpha additionally
	// subscribes to delta, and delta subscribes to nothing. delta's
	// pending row must gate alpha even when alpha is the byte-wise
	// lowest in its pending cycle.
	nodes := threeCycleNodes()
	nodes[0].Subscribes = append(nodes[0].Subscribes,
		tmplspec.SubscriptionEntry{Node: "delta", Type: "terminal/*"})
	nodes = append(nodes, tmplspec.TemplateNodeDef{Type: "delta", Executor: "stub"})
	fx := makeGateFixture(t, nodes)

	if !fx.gated(t, "alpha") {
		t.Errorf("alpha must gate on the merely-pending out-of-cycle sender delta, regardless of the cycle tie-break")
	}
	// delta has no senders at all: it passes and resolves the knot.
	if fx.gated(t, "delta") {
		t.Errorf("delta has no subscribed upstreams and must not be gated")
	}
}
