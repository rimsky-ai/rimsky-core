// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package subgraph

import (
	"testing"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestInternalCascade_FiresNonEntryNodes(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "delegate-template",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "caller", Delegate: "staging"}},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	if res := node.ValidateTemplate(tmpl, node.RegistryHooks{}); len(res.Errors) != 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}
	internals, err := runtime.SubgraphParentSuccessCascade(runtime.SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "staging",
	})
	if err != nil {
		t.Fatalf("SubgraphParentSuccessCascade: %v", err)
	}
	if len(internals) != 2 {
		t.Fatalf("internals: %d (want 2)", len(internals))
	}
	seen := map[string]struct{}{}
	for _, n := range internals {
		seen[n.Type] = struct{}{}
		if n.Type == "validate" {
			t.Errorf("entry (validate) must not be in the internal-cascade set")
		}
	}
	if _, ok := seen["transform"]; !ok {
		t.Errorf("transform must be in the internal-cascade set")
	}
	if _, ok := seen["promote"]; !ok {
		t.Errorf("promote (exit) must be in the internal-cascade set — exit is a child like any other internal")
	}

	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}
	transform := byType["transform"]
	if transform == nil || len(transform.Subscribes) != 1 || !transform.Subscribes[0].ResolvesViaCallingNode {
		t.Errorf("transform's subscription to entry alias must carry ResolvesViaCallingNode marker: %+v", transform)
	}
	promote := byType["promote"]
	if promote == nil || len(promote.Subscribes) != 1 || promote.Subscribes[0].ResolvesViaCallingNode {
		t.Errorf("promote's subscription to transform should not carry ResolvesViaCallingNode: %+v", promote)
	}
}

func TestInternalCascade_RejectsMissingGraph(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "delegate-template",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "caller", Delegate: "staging"}},
			},
			{
				Name:  "staging",
				Entry: "v",
				Exit:  "x",
				Nodes: []node.TemplateNodeDef{
					{Type: "v"},
					{Type: "x", Subscribes: []tmplspec.SubscriptionEntry{{Node: "v", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	_ = node.ValidateTemplate(tmpl, node.RegistryHooks{})
	_, err := runtime.SubgraphParentSuccessCascade(runtime.SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "missing",
	})
	if err == nil {
		t.Fatalf("expected error for missing delegate graph")
	}
}
