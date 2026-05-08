// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Driver-level tests for the nodes accessor. Most node behavior is
// exercised through the modeling-layer scenario tests against the
// postgres testcontainer; tests here cover query shapes that the
// scenarios don't drive directly (e.g. supervisor-scoped filters
// used by the heartbeat tick).

package sqlite_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

// TestNodes_ListRunningBySupervisor verifies the supervisor-scoped
// running-nodes query used by Supervisor.doHeartbeat. Three nodes are
// seeded — two assigned to the supervisor under test (one running, one
// stale) and one running but assigned to a different supervisor — and
// only the running-and-assigned-to-self row is returned.
//
// This is the source of truth that replaced the in-memory `activeNodes`
// map: the heartbeat tick reads the DB to pick up both sync dispatches
// (RunNode in-flight) and async dispatches (handed off to the callback
// server but still `running` in the DB until the terminal callback
// arrives). The previous in-memory tracking missed the entire async
// window between AsyncAccepted and the terminal callback.
func TestNodes_ListRunningBySupervisor(t *testing.T) {
	d := openMigratedSQLite(t)
	store := d.Store()
	ctx := context.Background()

	tmplHash := mustHashSpec(node.TemplateSpec{Name: "lrbs", Version: "1"})
	instID := shared.UUID(uuid.New())
	runningSelf := shared.UUID(uuid.New())
	staleSelf := shared.UUID(uuid.New())
	runningOther := shared.UUID(uuid.New())

	require := requireT(t)

	require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmplHash, Spec: node.TemplateSpec{Name: "lrbs", Version: "1"},
			State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		ck := "ck-lrbs"
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmplHash, InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		for _, id := range []shared.UUID{runningSelf, staleSelf, runningOther} {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: id, InstanceID: instID, NodeType: "worker", Executor: "stub",
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	// Nodes are created in `fresh` state; the cascade machine requires
	// fresh → stale → running. Drive each test row through the legal path.
	transitionToRunning := func(id shared.UUID, supervisorID string, hbAt time.Time) {
		t.Helper()
		require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := store.Nodes().UpdateState(ctx, id, shared.NodeStateStale, cascade.ReasonInvalidateReceived, "", tx); err != nil {
				return err
			}
			if err := store.Nodes().UpdateState(ctx, id, shared.NodeStateRunning, cascade.ReasonDispatchClaimed, "", tx); err != nil {
				return err
			}
			return store.Nodes().UpdateHeartbeat(ctx, id, hbAt, supervisorID, tx)
		}))
	}

	// runningSelf: running, supervisor=sup-A, stale heartbeat (the kind that
	// would trip SweepStaleHeartbeats absent doHeartbeat refreshing it).
	transitionToRunning(runningSelf, "sup-A", time.Now().Add(-30*time.Second))
	// staleSelf: still in fresh state (default after Create), supervisor=sup-A
	// — present in the table assigned to the same supervisor but should NOT
	// be returned because state != running.
	require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Nodes().UpdateHeartbeat(ctx, staleSelf, time.Now(), "sup-A", tx)
	}))
	// runningOther: running, supervisor=sup-B — must NOT show up under sup-A.
	transitionToRunning(runningOther, "sup-B", time.Now())

	var got []persistence.NodeRow
	require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-A", tx)
		got = rows
		return err
	}))

	if len(got) != 1 {
		t.Fatalf("ListRunningBySupervisor(sup-A): got %d rows, want 1", len(got))
	}
	if got[0].ID != runningSelf {
		t.Fatalf("ListRunningBySupervisor(sup-A): got id=%s, want %s", got[0].ID, runningSelf)
	}
	if got[0].State != shared.NodeStateRunning {
		t.Fatalf("ListRunningBySupervisor(sup-A): got state=%s, want running", got[0].State)
	}
	if got[0].AssignedSupervisorID != "sup-A" {
		t.Fatalf("ListRunningBySupervisor(sup-A): got supervisor=%q, want sup-A", got[0].AssignedSupervisorID)
	}

	// sup-B: should see only runningOther.
	require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-B", tx)
		got = rows
		return err
	}))
	if len(got) != 1 || got[0].ID != runningOther {
		t.Fatalf("ListRunningBySupervisor(sup-B): got %v, want exactly runningOther", nodeIDs(got))
	}

	// Unknown supervisor: empty.
	require(store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-zzz", tx)
		got = rows
		return err
	}))
	if len(got) != 0 {
		t.Fatalf("ListRunningBySupervisor(sup-zzz): got %d rows, want 0", len(got))
	}
}

func nodeIDs(rows []persistence.NodeRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID.String())
	}
	sort.Strings(out)
	return out
}

func mustHashSpec(spec node.TemplateSpec) string {
	sum := sha256.Sum256([]byte(spec.Name + ":" + spec.Version))
	return "sha256-" + hex.EncodeToString(sum[:])
}

func requireT(t *testing.T) func(error) {
	t.Helper()
	return func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
