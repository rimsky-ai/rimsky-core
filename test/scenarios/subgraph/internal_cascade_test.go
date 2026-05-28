// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N3 scenario — internal_cascade.
//
// At entry-success terminal, the supervisor must stay in `running` on
// the calling node and stale-mark the non-entry internal nodes as
// children of the calling-node parent run. The state-machine accepts
// the `running → running` self-transition under the
// `subgraph_internal_cascade_fired` reason. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Invocation semantics step 4 (success).
//
// This scenario exercises the runtime helper
// `SubgraphParentSuccessCascade` against a canonicalized template,
// verifying the internal-node set and the cascade reason match the
// spec's contract.
package subgraph

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestInternalCascade_FiresNonEntryNodes(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "delegate-template",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
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
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*"}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*"}}},
				},
			},
		},
	}
	if res := node.ValidateTemplate(tmpl, node.RegistryHooks{}); len(res.Errors) != 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}
	internals, reason, err := runtime.SubgraphParentSuccessCascade(runtime.SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "staging",
	})
	if err != nil {
		t.Fatalf("SubgraphParentSuccessCascade: %v", err)
	}
	// The entry (validate) is absorbed into the calling node — it must
	// NOT appear in the internal-cascade set. transform and promote
	// (exit) remain.
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
	if reason.Kind != cascade.ReasonSubGraphInternalCascadeFired.Kind {
		t.Errorf("transition reason: %s (want subgraph_internal_cascade_fired)", reason.Kind)
	}

	// And the canonicalizer must have flagged the entry-referencing
	// subscription edge for runtime resolution to the calling node.
	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}
	transform := byType["transform"]
	if transform == nil || len(transform.Subscribes) != 1 || !transform.Subscribes[0].ResolvesViaCallingNode {
		t.Errorf("transform's subscription to entry alias must carry ResolvesViaCallingNode marker: %+v", transform)
	}
	// promote subscribes to transform (an interior internal) — not the
	// entry alias — so the marker should be unset.
	promote := byType["promote"]
	if promote == nil || len(promote.Subscribes) != 1 || promote.Subscribes[0].ResolvesViaCallingNode {
		t.Errorf("promote's subscription to transform should not carry ResolvesViaCallingNode: %+v", promote)
	}
}

func TestInternalCascade_RejectsMissingGraph(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:                "delegate-template",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
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
					{Type: "x", Subscribes: []tmplspec.SubscriptionEntry{{Node: "v", Type: "terminal/*"}}},
				},
			},
		},
	}
	_ = node.ValidateTemplate(tmpl, node.RegistryHooks{})
	_, _, err := runtime.SubgraphParentSuccessCascade(runtime.SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "missing",
	})
	if err == nil {
		t.Fatalf("expected error for missing delegate graph")
	}
}
