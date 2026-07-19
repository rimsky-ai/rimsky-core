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

func TestEntryAbsorption_MarkerEmittedOnCallingNode(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "delegate-template",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "outer-caller", Delegate: "staging"},
					{Type: "plain-node", Executor: "stub"},
				},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "validator"},
					{Type: "transform", Executor: "transformer",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
					{Type: "promote", Executor: "promoter",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}

	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}

	caller := byType["outer-caller"]
	if caller == nil {
		t.Fatalf("outer-caller missing from canonicalized template")
	}
	if !caller.IsSubgraphEntryAbsorbed {
		t.Errorf("outer-caller must carry IsSubgraphEntryAbsorbed=true after canonicalization: %+v", caller)
	}
	if !runtime.IsSubgraphCaller(caller) {
		t.Errorf("outer-caller must be recognized by IsSubgraphCaller (Delegate=%q)", caller.Delegate)
	}

	plain := byType["plain-node"]
	if plain == nil {
		t.Fatalf("plain-node missing")
	}
	if plain.IsSubgraphEntryAbsorbed {
		t.Errorf("plain-node should not carry IsSubgraphEntryAbsorbed: %+v", plain)
	}
	if runtime.IsSubgraphCaller(plain) {
		t.Errorf("plain-node should not be IsSubgraphCaller (Delegate=%q)", plain.Delegate)
	}
}

func TestEntryAbsorption_EntrySchemaMergedOntoCaller(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "delegate-template",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{
						Type:     "outer-caller",
						Delegate: "staging",
						Attributes: &node.NodeAttributesDef{Schema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"shared_key": map[string]any{
									"type": "string", "default": "caller-value",
								},
								"caller_only_key": map[string]any{
									"type": "string", "default": "caller-default",
								},
							},
						}},
					},
				},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{
						Type:     "validate",
						Executor: "validator",
						Attributes: &node.NodeAttributesDef{Schema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"shared_key": map[string]any{
									"type": "string", "default": "entry-value",
								},
								"entry_only_key": map[string]any{
									"type": "string", "default": "entry-default",
								},
							},
						}},
					},
					{Type: "transform", Executor: "transformer",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
					{Type: "promote", Executor: "promoter",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	if len(res.Errors) != 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}

	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}

	caller := byType["outer-caller"]
	if caller == nil {
		t.Fatalf("outer-caller missing from canonicalized template")
	}
	if caller.Attributes == nil {
		t.Fatalf("outer-caller.Attributes is nil after absorption; entry schema was not merged onto the caller")
	}
	props, ok := caller.Attributes.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("outer-caller.Attributes.Schema has no properties map: %+v", caller.Attributes.Schema)
	}

	sharedKey, ok := props["shared_key"].(map[string]any)
	if !ok {
		t.Fatalf("shared_key missing from merged caller schema: %+v", props)
	}
	if got := sharedKey["default"]; got != "caller-value" {
		t.Errorf("conflicting key shared_key: caller's own declared value must win over the absorbed entry's value; got default=%v, want caller-value", got)
	}

	entryOnly, ok := props["entry_only_key"].(map[string]any)
	if !ok {
		t.Fatalf("entry_only_key not copied from entry onto caller; entry-declared attribute schema must merge onto the caller: %+v", props)
	}
	if got := entryOnly["default"]; got != "entry-default" {
		t.Errorf("entry_only_key default = %v, want entry-default", got)
	}

	callerOnly, ok := props["caller_only_key"].(map[string]any)
	if !ok {
		t.Fatalf("caller_only_key lost from caller's own schema after absorption merge: %+v", props)
	}
	if got := callerOnly["default"]; got != "caller-default" {
		t.Errorf("caller_only_key default = %v, want caller-default", got)
	}
}

func TestEntryAbsorption_ExitNodeIdentified(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "delegate-template",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{{Type: "outer-caller", Delegate: "staging"}},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "validate", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []tmplspec.SubscriptionEntry{{Node: "transform", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)}}},
				},
			},
		},
	}
	if res := node.ValidateTemplate(tmpl, node.RegistryHooks{}); len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	if !runtime.IsSubgraphExit(tmpl, "promote") {
		t.Errorf("promote should be the sub-graph exit")
	}
	if runtime.IsSubgraphExit(tmpl, "validate") {
		t.Errorf("validate is entry, not exit")
	}
	if runtime.IsSubgraphExit(tmpl, "outer-caller") {
		t.Errorf("outer-caller is the calling node in main; not an exit")
	}
	byType := make(map[string]*node.TemplateNodeDef, len(tmpl.Nodes))
	for i := range tmpl.Nodes {
		byType[tmpl.Nodes[i].Type] = &tmpl.Nodes[i]
	}
	if exit := byType["promote"]; exit == nil || !exit.IsSubgraphExit {
		t.Errorf("promote must carry IsSubgraphExit=true after canonicalization: %+v", exit)
	}
	if entry := byType["validate"]; entry == nil || entry.IsSubgraphExit {
		t.Errorf("validate is entry; must not carry IsSubgraphExit: %+v", entry)
	}
	if caller := byType["outer-caller"]; caller == nil || caller.IsSubgraphExit {
		t.Errorf("outer-caller is in main; must not carry IsSubgraphExit: %+v", caller)
	}
}
