// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package lifecycle

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestLifecycleE2E_FullSequence(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"alpha": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolLifecycleSubscriber},
				},
			},
		},
	})

	ctx := context.Background()

	spec := node.TemplateSpec{
		Name: "lifecycle-e2e", Version: "v1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "n1",
			Executor: "stub",
			ClaimProducers: []node.NodeClaimProducerRef{{
				Name: "alpha", Selector: "x", Intent: "r",
			}},
		}},
	}
	templateHash := h.DeployTemplate(spec)

	tplRow := getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeTemplate, templateHash)
	require.NotNil(t, tplRow, "template-scope lifecycle row must exist after deploy")
	require.Equal(t, persistence.LifecycleIdempotencyStateDeployed, tplRow.State)

	instanceID := h.CreateInstance(templateHash, "ck-1", nil)
	instRow := getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeInstance, instanceID.String())
	require.NotNil(t, instRow, "instance-scope lifecycle row must exist after create")
	require.Equal(t, persistence.LifecycleIdempotencyStateCreated, instRow.State)

	require.NoError(t, h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.Instances().MarkTerminated(ctx, instanceID, tx)
	}))

	deleteAndExpect(t, h, "/v1/instances/"+instanceID.String(), http.StatusOK)
	require.Nil(t, getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeInstance, instanceID.String()),
		"instance-scope lifecycle row must be deleted by terminate fan-out")

	postAndExpect(t, h, "/v1/templates/"+templateHash+"/undeploy", http.StatusOK)
	tplRow = getLifecycleRow(t, h, "alpha", persistence.LifecycleIdempotencyScopeTemplate, templateHash)
	require.NotNil(t, tplRow)
	require.Equal(t, persistence.LifecycleIdempotencyStateUndeployed, tplRow.State)

	deleteAndExpect(t, h, "/v1/templates/"+templateHash, http.StatusOK)
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
