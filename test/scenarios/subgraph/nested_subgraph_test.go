// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N3 scenario — nested_subgraph.
//
// A sub-graph internal node MAY delegate to a further sub-graph (it
// becomes the calling node for an inner invocation). The canonicalizer
// must accept such templates as long as there is no `delegate:` cycle
// across graphs; cycles are rejected with
// `subgraph_recursion_unsupported` per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Edge-case rejections at registration.
//
// This scenario validates both branches: a clean nested-but-acyclic
// template canonicalizes, and a self-referencing graph is rejected.
package subgraph

import (
	"strings"
	"testing"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestNestedSubgraph_AcyclicAccepted(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "nested",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "top", Delegate: "outer"}},
			},
			{
				Name:  "outer",
				Entry: "outer-entry",
				Exit:  "outer-exit",
				Nodes: []node.TemplateNodeDef{
					{Type: "outer-entry", Executor: "stub"},
					{Type: "outer-mid", Delegate: "inner",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "outer-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
					{Type: "outer-exit", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "outer-mid", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
			{
				Name:  "inner",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					{Type: "inner-entry", Executor: "stub"},
					{Type: "inner-exit", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("acyclic nested template should validate clean, got errors: %v", res.Errors)
	}
	// @constraint: Both calling nodes must carry the absorption marker.
	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}
	for _, name := range []string{"top", "outer-mid"} {
		def := byType[name]
		if def == nil || !def.IsSubgraphEntryAbsorbed {
			t.Errorf("%s should carry IsSubgraphEntryAbsorbed=true (Delegate set)", name)
		}
		if !runtime.IsSubgraphCaller(def) {
			t.Errorf("%s should be IsSubgraphCaller", name)
		}
	}
}

func TestNestedSubgraph_CycleRejected(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "cyclic",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "top", Delegate: "g1"}},
			},
			{
				Name:  "g1",
				Entry: "g1n",
				Exit:  "g1x",
				Nodes: []node.TemplateNodeDef{
					{Type: "g1n", Delegate: "g2"},
					{Type: "g1x", Subscribes: []tmplspec.SubscriptionEntry{{Node: "g1n", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
			{
				Name:  "g2",
				Entry: "g2n",
				Exit:  "g2x",
				Nodes: []node.TemplateNodeDef{
					{Type: "g2n", Delegate: "g1"}, // @deliberate: cycle: g1 → g2 → g1
					{Type: "g2x", Subscribes: []tmplspec.SubscriptionEntry{{Node: "g2n", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "subgraph_recursion_unsupported") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected subgraph_recursion_unsupported rejection; got: %v", res.Errors)
	}
}
