// Spec §19.1 — `resumable: true` + `resume_then_retry` preserves
// executor-populated fields.
//
// Drives RunNode directly with pre-seeded state to exercise the §13.3
// step 3a rebind path: a `rimsky_lock_holders` row owned by our
// supervisor, far-future `expires_at`, kind=region against the
// stub-filesystem store. The runner rebinds it, sets Resumed=true on
// the spec, and (per spec §5.7.3) the dispatch-time attributes upsert
// preserves the prior row's executor-populated fields.
//
// The test asserts the dispatched ExecuteRequest carries the preserved
// "executor_field" verbatim, and the final committed attributes row
// still carries it.
package attributes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
)

func TestAttributesResumablePreserve(t *testing.T) {
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
		Name: "attr-resume-preserve", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name: "fs", Write: []string{"region-a"}, Resumable: true,
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
	iid := h.CreateInstance(tid, "ck-resume-preserve", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Seed a preserved-for-resume lock-holder row owned by our supervisor:
	// kind=region, store_name=fs, region tokens=["region-a"], expires_at
	// far in the future. Mirrors what releaseLocksInTx's PreserveForResume
	// branch leaves behind.
	regionData, err := json.Marshal([]string{"region-a"})
	require.NoError(t, err)
	storeName := "fs"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 uuid.New(),
			LockKind:           storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         regionData,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(30 * time.Minute),
		}, tx)
	}))

	// Seed a node_attributes row carrying the executor-populated field
	// from the prior run. run_attempt=1 so the runner's bump produces
	// run_attempt=2 in the dispatched request.
	require.NoError(t, h.Storage.NodeAttributes().Upsert(h.Ctx, worker.ID, 1, map[string]any{
		"executor_field": "preserved_value",
	}))

	// Drive one runner cycle. The harness's auto-enqueue from
	// CreateInstance left a dispatch row eligible for our supervisor.
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

	// Verify the executor saw the preserved field in its Attributes
	// argument — i.e. upsertAttributesPreDispatch correctly merged the
	// prior row on the resume path.
	var observed map[string]any
	for _, obs := range h.Stub.Observed() {
		if obs.NodeID == worker.ID.String() {
			observed = obs.Attributes
			break
		}
	}
	require.NotNil(t, observed, "stub did not observe a dispatch for the worker")
	require.Equal(t, "preserved_value", observed["executor_field"],
		"executor-populated field must be preserved across resume_then_retry")

	// Final state: node fresh, attributes row still carries the field.
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 5*time.Second),
		"worker did not reach fresh after RunNode")
	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "preserved_value", row.Data["executor_field"],
		"committed attributes must still carry the preserved field")
}
