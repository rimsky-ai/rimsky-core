// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lifecycle_e2e_test.go — end-to-end coverage of the six store-lifecycle
// events from spec §4.1, driven through the scenario harness against a
// loopback stub store-service.
//
// Sequence: register → deploy → instantiate → drive instance to terminal
// → undeploy → deregister. After each control-api transition we assert
// the rimsky_lifecycle_idempotencies row counts match the spec's expected
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

	"github.com/rimsky-ai/rimsky-core/control/config"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

// TestLifecycleE2E_FullSequence walks the full template/instance
// lifecycle and asserts rimsky_lifecycle_idempotencies row deltas at every
// transition.
func TestLifecycleE2E_FullSequence(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
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
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	ctx := context.Background()

	// Register + deploy: harness's DeployTemplate handles both steps.
	spec := node.TemplateSpec{
		Name: "lifecycle-e2e", Version: "v1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
	tplRow := getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeTemplate, templateHash)
	require.NotNil(t, tplRow, "template-scope lifecycle row must exist after deploy")
	require.Equal(t, persistence.LifecycleIdempotencyStateDeployed, tplRow.State)

	// Instantiate: triggers OnInstanceCreated.
	instanceID := h.CreateInstance(templateHash, "ck-1", nil)
	instRow := getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeInstance, instanceID.String())
	require.NotNil(t, instRow, "instance-scope lifecycle row must exist after create")
	require.Equal(t, persistence.LifecycleIdempotencyStateCreated, instRow.State)

	// Drive instance terminal — manual SQL bypass; lifecycle test
	// doesn't depend on the frame engine.
	require.NoError(t, h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.Instances().MarkTerminated(ctx, instanceID, tx)
	}))

	// DELETE /instances triggers OnInstanceTerminated fan-out, which
	// deletes the per-store lifecycle row before dropping the
	// instance row. We verify both outcomes below.
	deleteAndExpect(t, h, "/instances/"+instanceID.String(), http.StatusOK)
	require.Nil(t, getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeInstance, instanceID.String()),
		"instance-scope lifecycle row must be deleted by terminate fan-out")

	// Undeploy: template-scope row state='undeployed'.
	postAndExpect(t, h, "/templates/"+templateHash+"/undeploy", http.StatusOK)
	tplRow = getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeTemplate, templateHash)
	require.NotNil(t, tplRow)
	require.Equal(t, persistence.LifecycleIdempotencyStateUndeployed, tplRow.State)

	// Deregister: DELETE /templates/{hash}; lifecycle row must be gone.
	deleteAndExpect(t, h, "/templates/"+templateHash, http.StatusOK)
	require.Nil(t, getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeTemplate, templateHash),
		"template-scope lifecycle row must be deleted by deregister fan-out")
}

func getLifecycleRow(t *testing.T, h *scenario.Harness, storeName string, kind persistence.LifecycleIdempotencyScopeKind, scopeID string) *persistence.LifecycleIdempotencyRow {
	t.Helper()
	var row *persistence.LifecycleIdempotencyRow
	require.NoError(t, h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.LifecycleIdempotency().Get(ctx, storeName, kind, scopeID, tx)
		row = r
		return err
	}))
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
