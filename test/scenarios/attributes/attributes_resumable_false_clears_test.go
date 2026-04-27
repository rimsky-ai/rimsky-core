// Spec §19.1 — `resumable: false` (default) + retry clears
// executor-populated fields.
//
// Companion to attributes_resumable_preserve_test: same pre-seeded
// state but with `Resumable: false` on the store ref. The runner's
// rebind probe is conditional on the spec being resumable; when not,
// the prior lock-holder row (even if it exists) is not rebound and the
// run goes through the fresh path. With `Resumed=false` on every
// acquired lock, upsertAttributesPreDispatch overwrites the prior data
// outright, clearing executor-populated fields.
//
// We don't even need to pre-seed a lock-holder row for this test — the
// fresh path doesn't probe for one. Pre-seeding the prior data row is
// enough to verify the clear behaviour; the runner sees no rebind, so
// it Upserts only the freshly-substituted (empty) source-driven map.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestAttributesResumableFalseClears(t *testing.T) {
	t.Parallel()
	const supervisorID = "scenario-runner"
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"fs": {"kind": stub.KindFilesystem},
			},
		},
	})

	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-resume-clears", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				// resumable defaults to false here.
				scenario.WithStores(node.NodeStoreRef{
					Name: "fs", Write: []string{"region-a"},
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"executor_field": map[string]any{"type": "string"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-resume-clears", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Seed the prior run's executor-populated field. With resumable=false
	// the upcoming dispatch must overwrite this outright.
	require.NoError(t, h.Storage.NodeAttributes().Upsert(h.Ctx, worker.ID, 1, map[string]any{
		"executor_field": "should_be_cleared",
	}))

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
		AcceptedStores:    []string{"fs"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner should have picked up the dispatch and run")

	// Executor sees no preserved field — the resumable=false path
	// clears executor-populated state at dispatch.
	var observed map[string]any
	for _, obs := range h.Stub.Observed() {
		if obs.NodeID == worker.ID.String() {
			observed = obs.Attributes
			break
		}
	}
	require.NotNil(t, observed, "stub did not observe a dispatch")
	_, has := observed["executor_field"]
	require.False(t, has,
		"executor_field must be absent at dispatch when resumable=false")

	// Final attributes row must NOT carry the prior executor-populated
	// field (the stub returned an empty Complete delta, so the post-
	// commit row should reflect only the substituted source-driven
	// fields, which here is empty).
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 5*time.Second),
		"worker did not reach fresh after RunNode")
	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	_, finalHas := row.Data["executor_field"]
	require.False(t, finalHas,
		"committed attributes must NOT carry the prior executor-populated field when resumable=false")
}
