// §19.1 / §11.4 — held-claim resolution validation: a template that
// declares `hold: true` on a claim store but lacks a matching
// `claim_resolutions` entry on every terminal-leaf of the holding
// subgraph must be REJECTED at deploy time.
//
// The validator's §11.4 DAG walk:
//   - finds the holding subgraph (sourceNode + transitive descendants)
//   - identifies the leaves of that subgraph (no descendants in it)
//   - checks each leaf carries a (source, store) entry in
//     `claim_resolutions`
//   - flags any leaf that doesn't, naming the missing terminal in the
//     deploy error.
//
// We deploy a template via `POST /templates` — the same path real
// operators use — so we exercise both the validator and the HTTP
// surface end-to-end. The validator only cares about the kind of the
// referenced store; we use the stub claim store kind here for harness
// simplicity (the harness already registers the stub factories — see
// `core/scenario/harness.go`). The §11.4 walk is store-impl-agnostic;
// it operates on the parsed template, not on any live items table.
package claim_stores

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
)

func TestClaimResolutionsMissingTemplateDeployFails(t *testing.T) {
	t.Parallel()
	// NoSupervisor + NoScheduler keeps the harness lightweight: we only
	// need the control-api (the deploy validator runs there).
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"queue": {"kind": stub.KindClaimStore},
			},
		},
	})

	// Linear chain: claim-source → middle → terminal. Source declares
	// hold=true. The terminal-leaf "terminal" must declare a matching
	// claim_resolutions entry — but doesn't. Validator must flag it.
	//
	// Template JSON is constructed inline (bypassing scenario.DeployTemplate)
	// because that helper fatals on a non-Created response — exactly the
	// shape we want to assert here.
	failBody := map[string]any{
		"name":    "missing-resolution",
		"version": "1",
		"nodes": []map[string]any{
			{
				"type": "claim-source",
				"stores": []map[string]any{
					{"name": "queue", "claim": true, "hold": true},
				},
			},
			{
				"type":         "middle",
				"executor":     "stub",
				"dependencies": []string{"claim-source"},
			},
			{
				"type":         "terminal",
				"executor":     "stub",
				"dependencies": []string{"middle"},
				// Intentionally no claim_resolutions — must fail.
			},
		},
	}
	body, err := json.Marshal(failBody)
	require.NoError(t, err)

	resp, err := http.Post(h.ControlBase+"/templates", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBytes, _ := io.ReadAll(resp.Body)

	require.NotEqual(t, http.StatusCreated, resp.StatusCode,
		"deploy must fail for unresolved held claim, got %d body=%s", resp.StatusCode, respBytes)
	require.GreaterOrEqual(t, resp.StatusCode, 400,
		"deploy must return a 4xx for unresolved held claim, got %d", resp.StatusCode)

	// Error body must name the missing terminal so the operator knows
	// which leaf to add the resolution to (per §11.4 error message shape).
	require.Contains(t, string(respBytes), "terminal",
		"deploy error must name the unresolved leaf, got %s", respBytes)

	// Sanity: the same template with a matching resolution at the
	// terminal must succeed. Isolates the failure cause to the missing
	// resolution rather than some unrelated grammar issue. Use the
	// helper here — success path is fine.
	specOk := node.TemplateSpec{
		Name: "missing-resolution-fixed", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "claim-source"},
				scenario.WithStores(scenario.ClaimAndHoldRef("queue")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "middle", Executor: "stub", Dependencies: []string{"claim-source"}},
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "terminal", Executor: "stub", Dependencies: []string{"middle"}},
				scenario.WithClaimResolutions(scenario.ResolveClaim("claim-source", "queue")),
			),
		},
	}
	tid := h.DeployTemplate(specOk)
	require.NotEqual(t, "", tid.String(),
		"the same template WITH a terminal resolution must deploy cleanly")
}
