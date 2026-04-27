// Spec §19.1 + §10.5 — substitution-time race: an upstream is invalidated
// between dispatch eligibility and substitution. The verify-before-run /
// transition-to-running guard catches the race; the runner bails as
// `orphaned_claim_lost_race` rather than running with stale data.
//
// We model the race deterministically by transitioning the dependent
// node out of `stale` after candidate-selection eligibility has been
// established but before the runner's state-transition guard runs. This
// reproduces the spec §13.3 step 4.5 path: the supervisor's
// transition-to-running guard rejects the non-stale start and routes
// through `handleOrphanedClaim`, which emits the
// `orphaned_claim_lost_race` event. (Same emit point as the
// verify-before-run separate-read variant; both branches feed into
// `handleOrphanedClaim`.)
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestAttributesSubstitutionRaceLost(t *testing.T) {
	t.Parallel()
	const supervisorID = "scenario-runner"
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	h.Stub.WhenType("upstream").Complete(map[string]any{"value": "hello"}, true, "u")
	h.Stub.WhenType("dependent").Complete(map[string]any{}, true, "d")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "race-lost", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "dependent", Executor: "stub", Dependencies: []string{"upstream"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string", "source": "{{deps.upstream.value}}"},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-race-lost", map[string]any{})

	dependent := h.FindNode(iid, "dependent")
	require.NotNil(t, dependent)

	// Seed the upstream's attributes (the runner's substitution would
	// read this row at dispatch — it's required for the reachable
	// failure path; without it the substitution would fail earlier with
	// template_resolution_failed instead).
	require.NoError(t, h.Storage.NodeAttributes().Upsert(h.Ctx, h.FindNode(iid, "upstream").ID, 0,
		map[string]any{"value": "hello"}))

	// Enqueue an unclaimed dispatch row for the dependent so the
	// runner's candidate selection picks it up. Source frame_id from
	// the node row (the harness's CreateInstance + frame engine seeded
	// it via the cascade or initial advance).
	depRow, err := h.Storage.Nodes().Get(h.Ctx, dependent.ID, nil)
	require.NoError(t, err)
	if depRow.FrameID == nil {
		// Fall back: reuse the existing running frame for this instance,
		// or seed a new one.
		var fid shared.UUID
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
			iid,
		).Scan(&fid)
		if err != nil {
			require.NoError(t, h.Pool.QueryRow(h.Ctx, `
                INSERT INTO rimsky_frames
                    (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
                VALUES ($1, 'serial_queue', 'running', ARRAY[$2]::UUID[], now(), now(), 600000)
                RETURNING frame_id
            `, iid, dependent.ID).Scan(&fid))
		}
		_, err = h.Pool.Exec(h.Ctx,
			`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, fid, dependent.ID)
		require.NoError(t, err)
		depRow.FrameID = &fid
	}
	require.NoError(t, h.Queue.Enqueue(h.Ctx, queue.DispatchRequest{
		NodeID:       dependent.ID,
		ExecutorName: "stub",
		EnqueuedAt:   time.Now().Add(-time.Second),
		FrameID:      *depRow.FrameID,
	}))

	// Manually drive the dependent's dispatch into a "race-lost" shape:
	// dispatch is unclaimed (so SelectCandidates picks it up), but the
	// node state is `running` rather than `stale`. The runner will
	// candidate-select, claim, verify-before-run, then fail at
	// transition-to-running (no `running → running` edge under
	// dispatch_claimed) — the same handleOrphanedClaim → emit
	// `orphaned_claim_lost_race` path the §13.3 step 4 verify catches.
	// Direct UPDATE because the state machine has no fresh→running edge.
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET state = 'running' WHERE id = $1`, dependent.ID)
	require.NoError(t, err)

	// Drive one runner cycle.
	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })
	args := supervisor.RunArgs{
		Storage:           h.Storage,
		Queue:             h.Queue,
		QueuePool:         h.Pool,
		LockHolders:       store.NewLockHoldersClient(h.Pool),
		StoreRegistry:     h.Stores,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      supervisorID,
		AcceptedExecutors: []string{"stub"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.False(t, out.Ran,
		"runner must bail before running (state-transition guard fired)")

	// The orphan-claim handler emitted the canonical event.
	nid := dependent.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "orphaned_claim_lost_race"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events,
		"expected orphaned_claim_lost_race event")
}
