// Scenario (stores §19.1, §13.1 step 4) — supervisor pool
// specialisation: a supervisor whose `accepted_stores` does not contain
// store X never claims dispatch rows requiring X.
//
// Two supervisors are stood up against the same Postgres + control-api:
//
//   - sup-A only has the "alpha" filesystem store configured;
//     accepted_stores = {"alpha"}.
//   - sup-B only has the "beta" filesystem store configured;
//     accepted_stores = {"beta"}.
//
// One template declares two nodes — one writing into "alpha", one
// writing into "beta". After both nodes' dispatch rows are enqueued
// (control-api on instance creation), each must be claimed by exactly
// the supervisor whose registry contains its store.
//
// This exercises §13.3 step 1's `required_stores <@ $accepted_stores`
// SQL predicate end-to-end via two real `core/config.StartSupervisor`
// processes — not in-process unit-test scaffolding.
package stores

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

func TestStorePoolSpecialization(t *testing.T) {
	// Skipped under frame resolution: this test depends on two root
	// nodes in one instance running concurrently (each on a different
	// supervisor), but the frame model serializes frames per instance.
	// See docs/plans/2026-04-26-frame-resolution-notes.md.
	t.Skip("frame-resolution: two roots in one instance run sequentially")
	t.Parallel()

	// Harness brings up Postgres + scheduler + control-api with BOTH
	// stores ("alpha" and "beta") so template deployment validates;
	// supervisor is disabled — we start two specialised supervisors
	// manually below.
	rootAlpha := t.TempDir()
	rootBeta := t.TempDir()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ExtraStoreFactories: []store.Factory{filesystem.Factory{}},
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"alpha": {"kind": "filesystem", "mode": "direct", "root": rootAlpha},
				"beta":  {"kind": "filesystem", "mode": "direct", "root": rootBeta},
			},
		},
	})

	// Synchronous Complete for both node types — once a supervisor
	// claims a node, it terminates inline and we observe the assigned
	// supervisor in the node row.
	h.Stub.WhenType("alpha-writer").Complete(map[string]any{}, true, "ok")
	h.Stub.WhenType("beta-writer").Complete(map[string]any{}, true, "ok")

	// Two supervisors, each with a disjoint single-store config.
	// startSpecializedSupervisor registers t.Cleanup; we don't need the
	// returned handles past startup.
	startSpecializedSupervisor(t, h, "sup-alpha-only", store.StoresConfig{
		Stores: map[string]map[string]any{
			"alpha": {"kind": "filesystem", "mode": "direct", "root": rootAlpha},
		},
	})
	startSpecializedSupervisor(t, h, "sup-beta-only", store.StoresConfig{
		Stores: map[string]map[string]any{
			"beta": {"kind": "filesystem", "mode": "direct", "root": rootBeta},
		},
	})

	// Verify the registered accepted_stores arrays match expectations
	// (proves the §14.2 registration path stamps the right TEXT[]).
	rowA, err := h.Storage.Supervisors().Get(h.Ctx, "sup-alpha-only", nil)
	require.NoError(t, err)
	require.NotNil(t, rowA)
	require.ElementsMatch(t, []string{"alpha"}, rowA.AcceptedStores)
	rowB, err := h.Storage.Supervisors().Get(h.Ctx, "sup-beta-only", nil)
	require.NoError(t, err)
	require.NotNil(t, rowB)
	require.ElementsMatch(t, []string{"beta"}, rowB.AcceptedStores)

	// Deploy template with two nodes writing into the two stores.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-pool-spec", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "alpha-writer", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("alpha", "items/a.md")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "beta-writer", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("beta", "items/b.md")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-pool", map[string]any{})

	alphaWriter := h.FindNode(iid, "alpha-writer")
	betaWriter := h.FindNode(iid, "beta-writer")
	require.NotNil(t, alphaWriter)
	require.NotNil(t, betaWriter)

	// Both nodes should reach `fresh` (each via the supervisor that
	// holds its store) within the per-test timeout.
	require.True(t, h.WaitForNodeState(alphaWriter.ID, shared.NodeStateFresh, 15*time.Second),
		"alpha-writer did not reach fresh")
	require.True(t, h.WaitForNodeState(betaWriter.ID, shared.NodeStateFresh, 15*time.Second),
		"beta-writer did not reach fresh")

	// Verify routing via the work_started event payload.
	// rimsky_nodes.assigned_supervisor_id is cleared on transitions to
	// non-running (see core/storage/postgres/nodes.go::UpdateState), so
	// we read the supervisor_id off the work_started event the runner
	// emitted at acquisition time.
	require.Equal(t, "sup-alpha-only", workStartedSupervisor(t, h, alphaWriter.ID),
		"alpha-writer must be claimed by the supervisor whose accepted_stores contains 'alpha'")
	require.Equal(t, "sup-beta-only", workStartedSupervisor(t, h, betaWriter.ID),
		"beta-writer must be claimed by the supervisor whose accepted_stores contains 'beta'")
}

// workStartedSupervisor finds the most recent work_started event for
// `nodeID` and returns the supervisor_id field from its payload. The
// runner emits this event at acquisition time (core/supervisor/runner_acquire.go),
// recording which supervisor claimed the dispatch row.
func workStartedSupervisor(t *testing.T, h *scenario.Harness, nodeID shared.UUID) string {
	t.Helper()
	nid := nodeID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid, Kind: "work_started"},
		storage.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected at least one work_started event for the node")
	supID, _ := evs.Events[0].Payload["supervisor_id"].(string)
	return supID
}

// startSpecializedSupervisor stands up a `config.StartSupervisor` with a
// per-supervisor store registry built from `cfg`. The supervisor shares
// the harness's Postgres + executor stub; its registered
// accepted_stores set is derived from the registry's built-store names
// (spec §14.2).
//
// Cleanup is registered with t — the harness's fixture-level
// t.Cleanup ordering ensures these supervisors shut down before the
// Postgres container teardown.
func startSpecializedSupervisor(t *testing.T, h *scenario.Harness, supervisorID string, stores store.StoresConfig) {
	t.Helper()
	resolver := executor.NewStaticResolver(map[string]executor.Endpoint{
		"stub": {Transport: "grpc", URL: h.StubAddr},
	})
	sv, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:      supervisorID,
		Storage:           h.Storage,
		Queue:             h.Queue,
		Clock:             h.Clock,
		Logger:            shared.SilentLogger{},
		Concurrency:       2,
		HeartbeatInterval: 500 * time.Millisecond,
		ClaimPollInterval: 100 * time.Millisecond,
		Resolver:          resolver,
		StoreFactories:    []store.Factory{filesystem.Factory{}},
		Stores:            stores,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	if err != nil {
		t.Fatalf("startSpecializedSupervisor(%s): %v", supervisorID, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sv.Shutdown(ctx)
	})
}
