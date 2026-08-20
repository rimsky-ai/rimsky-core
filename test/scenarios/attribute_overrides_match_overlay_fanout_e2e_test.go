// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesMatchOverlayFanout_ChildKeyMatcherRoutesPerChild(t *testing.T) {
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
	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tag": map[string]any{"type": "string"},
			"ok":  map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-fanout-child-key", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-child",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: "best_effort"},
					},
				},
				openAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "a"},
				"overlay": map[string]any{"tag": "for-a"},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "b"},
				"overlay": map[string]any{"tag": "for-b"},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "c"},
				"overlay": map[string]any{"tag": "for-c"},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-fanout", map[string]any{}, overrides)

	wantTags := map[string]bool{"for-a": true, "for-b": true, "for-c": true}
	awaited.Until(t, "each child_key matcher to fire on its own child's dispatch exactly once", func() bool {
		seen := map[string]int{}
		for _, o := range h.Stub.Observed() {
			if o.NodeType != "fan-child" {
				continue
			}
			tag, _ := o.Attributes["tag"].(string)
			if tag == "" {
				continue
			}
			seen[tag]++
		}
		if len(seen) != len(wantTags) {
			return false
		}
		for want := range wantTags {
			if seen[want] != 1 {
				return false
			}
		}
		return true
	})

	awaited.Until(t, "the three override match-counts to read [1, 1, 1]", func() bool {
		c := attributeOverrideMatchCounts(t, h, iid, 3)
		return len(c) == 3 && c[0] == 1 && c[1] == 1 && c[2] == 1
	})
}
