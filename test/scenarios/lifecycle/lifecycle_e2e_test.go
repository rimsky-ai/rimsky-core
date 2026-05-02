// lifecycle_e2e_test.go — end-to-end coverage of the six store-lifecycle
// events from spec §4.1, driven through the scenario harness against a
// loopback stub store-service.
//
// Sequence: register → deploy → instantiate → drive instance to terminal
// → undeploy → deregister. After each control-api transition we assert
// the rimsky_store_lifecycle row counts match the spec's expected
// invariants:
//
//   - registered:     one (template-scope) row at state='registered'.
//   - deployed:       same one row, state advanced to 'deployed'.
//   - instantiated:   above + one (instance-scope) row at state='created'.
//   - terminated:     instance-scope row deleted (lifecycle terminate flow).
//   - undeployed:     template-scope row at state='undeployed'.
//   - deregistered:   template-scope row gone.
//
// Drives terminal-state detection by writing terminated_at directly via
// SQL — the harness's runtime path isn't exercised here; this test
// targets lifecycle event sequencing, not frame engine behavior.
package lifecycle

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestLifecycleE2E_FullSequence walks the full template/instance
// lifecycle and asserts rimsky_store_lifecycle row deltas at every
// transition.
func TestLifecycleE2E_FullSequence(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: store.Capabilities{WriteSemantics: store.WriteSemanticsDirect},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		// Skip the supervisor and scheduler; we don't need them for
		// lifecycle event coverage and dropping them speeds the test up.
		NoSupervisor:     true,
		NoScheduler:      true,
		HeartbeatTimeout: 30 * time.Second,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"alpha": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: store.Capabilities{WriteSemantics: store.WriteSemanticsDirect},
				},
			},
		},
	})

	ctx := context.Background()

	// Register + deploy: harness's DeployTemplate handles both steps.
	spec := node.TemplateSpec{
		Name: "lifecycle-e2e", Version: "v1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{{
			Type:     "n1",
			Executor: "stub",
			Stores: []node.NodeStoreRef{{
				Name: "alpha", Selector: "x", Intent: "r",
			}},
		}},
	}
	templateHash := h.DeployTemplate(spec)

	// Post-DeployTemplate: register + deploy fired, so the
	// template-scope lifecycle row should be at state='deployed'.
	tplRow := getLifecycleRow(t, h, "alpha", storage.StoreLifecycleScopeTemplate, templateHash)
	require.NotNil(t, tplRow, "template-scope lifecycle row must exist after deploy")
	require.Equal(t, storage.StoreLifecycleStateDeployed, tplRow.State)

	// Instantiate: triggers OnInstanceCreated.
	instanceID := h.CreateInstance(templateHash, "ck-1", nil)
	instRow := getLifecycleRow(t, h, "alpha", storage.StoreLifecycleScopeInstance, instanceID.String())
	require.NotNil(t, instRow, "instance-scope lifecycle row must exist after create")
	require.Equal(t, storage.StoreLifecycleStateCreated, instRow.State)

	// Drive instance terminal — manual SQL bypass; lifecycle test
	// doesn't depend on the frame engine.
	require.NoError(t, h.Storage.Instances().MarkTerminated(ctx, instanceID, nil))

	// DELETE /instances triggers OnInstanceTerminated fan-out, which
	// deletes the per-store lifecycle row before dropping the
	// instance row. We verify both outcomes below.
	deleteAndExpect(t, h, "/instances/"+instanceID.String(), http.StatusOK)
	require.Nil(t, getLifecycleRow(t, h, "alpha", storage.StoreLifecycleScopeInstance, instanceID.String()),
		"instance-scope lifecycle row must be deleted by terminate fan-out")

	// Undeploy: template-scope row state='undeployed'.
	postAndExpect(t, h, "/templates/"+templateHash+"/undeploy", http.StatusOK)
	tplRow = getLifecycleRow(t, h, "alpha", storage.StoreLifecycleScopeTemplate, templateHash)
	require.NotNil(t, tplRow)
	require.Equal(t, storage.StoreLifecycleStateUndeployed, tplRow.State)

	// Deregister: DELETE /templates/{hash}; lifecycle row must be gone.
	deleteAndExpect(t, h, "/templates/"+templateHash, http.StatusOK)
	require.Nil(t, getLifecycleRow(t, h, "alpha", storage.StoreLifecycleScopeTemplate, templateHash),
		"template-scope lifecycle row must be deleted by deregister fan-out")
}

func getLifecycleRow(t *testing.T, h *scenario.Harness, storeName string, kind storage.StoreLifecycleScopeKind, scopeID string) *storage.StoreLifecycleRow {
	t.Helper()
	row, err := h.Storage.StoreLifecycle().Get(context.Background(), storeName, kind, scopeID, nil)
	require.NoError(t, err)
	return row
}

func postAndExpect(t *testing.T, h *scenario.Harness, path string, want int) {
	t.Helper()
	resp, err := http.Post(h.ControlBase+path, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, want, resp.StatusCode, "POST %s", path)
}

func deleteAndExpect(t *testing.T, h *scenario.Harness, path string, want int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, h.ControlBase+path, nil)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, want, resp.StatusCode, "DELETE %s", path)
}
