// Scenario (stores §19.1) — two nodes whose write globs target
// disjoint subtrees run concurrently. The supervisor's region-conflict
// check (§13.3 step 3d → filesystem.RegionsConflict) accepts both
// acquisitions in the same supervisor tick, so both nodes transition
// to `running` simultaneously.
//
// We use AsyncAccepted to keep both nodes pinned in `running` long
// enough to observe the simultaneity, then resolve them via the
// supervisor's callback endpoint. This mirrors the agentic-handoff
// scenario but with the store dimension layered in.
package stores

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

func TestFilesystemDirectDisjointRegions(t *testing.T) {
	// Skipped under frame resolution
	// (docs/specs/2026-04-26-frame-resolution-design.md §3.1): both
	// modes serialize frames per instance, so two roots in the same
	// instance cannot run concurrently. The disjoint-regions semantic
	// must be re-expressed as two SEPARATE instances or as cascading
	// peers from a single source. TODO: rewrite for the frame model.
	t.Skip("frame-resolution: two roots in one instance run sequentially")
	t.Parallel()
	root := t.TempDir()
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraStoreFactories: []store.Factory{filesystem.Factory{}},
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"content": {
					"kind": "filesystem",
					"mode": "direct",
					"root": root,
				},
			},
		},
	})

	// Both nodes return AsyncAccepted so they pin in `running` until we
	// resolve them via the callback path. Distinct ack-ids per type.
	h.Stub.WhenType("alpha").AsyncAccepted("ack-alpha", 5000)
	h.Stub.WhenType("beta").AsyncAccepted("ack-beta", 5000)

	// Two parallel nodes, no dependencies between them, disjoint write
	// globs (different top-level subtrees → filesystem.RegionsConflict
	// returns false).
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-disjoint", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "alpha", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "alpha/**")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "beta", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "beta/**")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-disjoint", map[string]any{})

	alpha := h.FindNode(iid, "alpha")
	beta := h.FindNode(iid, "beta")
	require.NotNil(t, alpha)
	require.NotNil(t, beta)

	// Both must reach `running` simultaneously. Poll until BOTH are
	// running in the same observation; if either ever exits running
	// before the other reaches it, that's a serialisation regression.
	deadline := time.Now().Add(15 * time.Second)
	var sawBothRunning bool
	for time.Now().Before(deadline) {
		got1, _ := h.Storage.Nodes().Get(h.Ctx, alpha.ID, nil)
		got2, _ := h.Storage.Nodes().Get(h.Ctx, beta.ID, nil)
		if got1 != nil && got2 != nil &&
			got1.State == shared.NodeStateRunning &&
			got2.State == shared.NodeStateRunning {
			sawBothRunning = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawBothRunning,
		"expected both nodes to be in running simultaneously (disjoint regions should not serialise)")

	// Resolve both nodes via the callback endpoint.
	completeAck(t, h.Supervisor.CallbackAddr(), "ack-alpha")
	completeAck(t, h.Supervisor.CallbackAddr(), "ack-beta")

	require.True(t, h.WaitForNodeState(alpha.ID, shared.NodeStateFresh, 15*time.Second),
		"alpha did not reach fresh after callback")
	require.True(t, h.WaitForNodeState(beta.ID, shared.NodeStateFresh, 15*time.Second),
		"beta did not reach fresh after callback")
}

// completeAck POSTs a Complete terminal event to the supervisor's
// async-callback endpoint. Used by the disjoint/overlapping/read-vs-write
// scenarios in this directory to release nodes pinned in `running` via
// AsyncAccepted.
//
// The path shape (`/v1/callback/<ackID>`) and body keying (`type` rather
// than `kind`) match the chi route in core/supervisor/callback.go and the
// end-to-end TS test in executors/claude-agent/src/server.test.ts.
func completeAck(t *testing.T, callbackAddr, ackID string) {
	t.Helper()
	cbURL := "http://" + callbackAddr + "/v1/callback/" + ackID
	body, _ := json.Marshal(map[string]any{
		"type":           "complete",
		"changed":        true,
		"change_summary": "ok",
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(cbURL, "application/json", bytes.NewReader(body))
		if err == nil {
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("callback %s did not return 200 within deadline", cbURL)
}
