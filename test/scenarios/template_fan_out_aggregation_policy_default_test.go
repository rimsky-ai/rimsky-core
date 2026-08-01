// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateFanOut_OmittedErrorPolicyKindStampedStrictAtRegistration(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-fan-out-omitted-kind", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
					},
				},
				openAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	resp, err := http.Get(h.ControlBase + "/v1/templates/" + tid)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET /v1/templates/%s: %s", tid, string(raw))

	var out struct {
		Spec node.TemplateSpec `json:"spec"`
	}
	require.NoErrorf(t, json.Unmarshal(raw, &out), "decode GET /v1/templates/%s: %s", tid, string(raw))

	var fanParent *node.TemplateNodeDef
	for i := range out.Spec.Nodes {
		if out.Spec.Nodes[i].Type == "fan-parent" {
			fanParent = &out.Spec.Nodes[i]
			break
		}
	}
	require.NotNil(t, fanParent, "fan-parent node missing from persisted template spec")
	require.NotNil(t, fanParent.FanOut, "fan-parent must retain its fan_out declaration in the persisted spec")
	require.Equal(t, tmplspec.AggregationKindStrict, fanParent.FanOut.ErrorPolicy.Kind,
		"the persisted template spec must have error_policy.kind stamped to %q at registration "+
			"time (node.CanonicalizeAggregationPolicyDefault, called from the /v1/templates POST "+
			"handler before the spec is hashed and persisted) even though the request omitted "+
			"kind entirely; got %q -- this proves the stamping happens at template-load, not as "+
			"read-side blank patching", tmplspec.AggregationKindStrict, fanParent.FanOut.ErrorPolicy.Kind)
}
