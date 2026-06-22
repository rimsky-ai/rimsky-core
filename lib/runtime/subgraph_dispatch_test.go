// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func makeSubgraphTemplate(graphName string) *node.TemplateSpec {
	return &node.TemplateSpec{
		Name: "delegating-template",
		Graphs: []spec.GraphSpec{
			{
				Name: spec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "outer-caller", Delegate: graphName},
				},
			},
			{
				Name:  graphName,
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub"},
					{Type: "promote", Executor: "stub"},
				},
			},
		},
	}
}

func TestSubgraphInternalCascade_ExcludesEntry(t *testing.T) {
	tmpl := makeSubgraphTemplate("staging-pipeline")
	internals, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
		CallingNodeRunID:  shared.UUID{},
		Template:          tmpl,
		DelegateGraphName: "staging-pipeline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(internals) != 2 {
		t.Fatalf("internals: %d (want 2; validate is entry, transform+promote remain)", len(internals))
	}
	for _, n := range internals {
		if n.Type == "validate" {
			t.Errorf("entry %q must not appear in internal-cascade set", n.Type)
		}
	}
}

func TestSubgraphInternalCascade_RejectsUnknownGraph(t *testing.T) {
	tmpl := makeSubgraphTemplate("staging-pipeline")
	_, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "missing",
	})
	if err == nil {
		t.Fatal("expected error for unknown delegate graph")
	}
}

func TestSubgraphInternalCascade_RejectsEmptyDelegate(t *testing.T) {
	tmpl := makeSubgraphTemplate("staging-pipeline")
	_, err := SubgraphInternalCascade(SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "",
	})
	if err == nil {
		t.Fatal("expected error for empty DelegateGraphName")
	}
}

func TestSubgraphParentSuccessCascade_ReturnsInternals(t *testing.T) {
	tmpl := makeSubgraphTemplate("staging-pipeline")
	internals, err := SubgraphParentSuccessCascade(SubgraphInternalCascadeArgs{
		Template:          tmpl,
		DelegateGraphName: "staging-pipeline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(internals) != 2 {
		t.Errorf("internals: %d (want 2)", len(internals))
	}
}

func TestIsSubgraphCaller(t *testing.T) {
	cases := []struct {
		name string
		def  *node.TemplateNodeDef
		want bool
	}{
		{"nil", nil, false},
		{"executor", &node.TemplateNodeDef{Executor: "stub"}, false},
		{"delegate", &node.TemplateNodeDef{Delegate: "sub-graph"}, true},
		{"empty delegate", &node.TemplateNodeDef{Delegate: ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if IsSubgraphCaller(c.def) != c.want {
				t.Errorf("got %v want %v", !c.want, c.want)
			}
		})
	}
}

func TestIsSubgraphExit(t *testing.T) {
	tmpl := makeSubgraphTemplate("staging-pipeline")
	if !IsSubgraphExit(tmpl, "promote") {
		t.Errorf("promote should be the sub-graph exit")
	}
	if IsSubgraphExit(tmpl, "outer-caller") {
		t.Errorf("outer-caller is in main; not a sub-graph exit")
	}
	if IsSubgraphExit(tmpl, "validate") {
		t.Errorf("validate is entry; not exit")
	}
	if IsSubgraphExit(tmpl, "transform") {
		t.Errorf("transform is interior internal; not exit")
	}
	if IsSubgraphExit(nil, "anything") {
		t.Errorf("nil tmpl returns false")
	}
}
