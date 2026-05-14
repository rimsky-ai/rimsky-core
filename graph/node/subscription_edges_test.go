// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"reflect"
	"sort"
	"testing"

	"github.com/fallguy/rimsky/foundation/spec"
)

func TestBuildSubscriptionEdges_Empty(t *testing.T) {
	out := BuildSubscriptionEdges(spec.TemplateSpec{}, nil)
	if len(out) != 0 {
		t.Fatalf("empty template should produce empty map; got %d keys", len(out))
	}
}

func TestBuildSubscriptionEdges_ExplicitDirect(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", On: "state", When: "fresh"},
			},
		},
	}}
	out := BuildSubscriptionEdges(tmpl, nil)
	edges, ok := out["sender"]
	if !ok {
		t.Fatalf("expected key 'sender' in map")
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	want := SubscriptionEdge{
		ReceiverNodeType:  "receiver",
		TopicKind:         "state",
		SubscriptionScope: "direct",
		Filter:            SubscriptionFilter{When: "fresh"},
		Frame:             "in",
	}
	if edges[0] != want {
		t.Fatalf("edge mismatch: got %+v want %+v", edges[0], want)
	}
}

func TestBuildSubscriptionEdges_CrossCutting(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "cleanup", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Instance: true, On: "state", When: "failed", ErrorClass: "rate_limited"},
			},
		},
	}}
	out := BuildSubscriptionEdges(tmpl, nil)
	edges, ok := out[""]
	if !ok {
		t.Fatalf("expected cross-cutting key '' in map; got keys %v", keys(out))
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].SubscriptionScope != "instance" {
		t.Fatalf("want scope=instance, got %q", edges[0].SubscriptionScope)
	}
	if edges[0].Frame != "next" {
		t.Fatalf("want default Frame=next for cross-cutting, got %q", edges[0].Frame)
	}
}

func TestBuildSubscriptionEdges_ImplicitFromSubstitutionRefs(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "stage", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"out": map[string]any{
						"type":   "string",
						"source": "out_value",
					},
				},
			}}},
		{Type: "verify", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"in": map[string]any{
						"type":   "string",
						"source": "{{nodes.stage.attribute.out}}",
					},
				},
			}}},
	}}
	refs := ExtractSubstitutionRefsFromTemplate(tmpl)
	out := BuildSubscriptionEdges(tmpl, refs)
	edges, ok := out["stage"]
	if !ok {
		t.Fatalf("expected implicit subscription on 'stage'")
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 implicit edge, got %d", len(edges))
	}
	if edges[0].TopicKind != "attribute" || edges[0].Filter.Name != "out" {
		t.Fatalf("implicit edge mismatch: %+v", edges[0])
	}
}

func TestBuildSubscriptionEdges_UnionAndDedup(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", On: "attribute", Name: "out"},
				// Duplicate of the implicit ref below.
				{Node: "sender", On: "attribute", Name: "out"},
			},
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"v": map[string]any{
						"type":   "string",
						"source": "{{nodes.sender.attribute.out}}",
					},
				},
			}}},
	}}
	refs := ExtractSubstitutionRefsFromTemplate(tmpl)
	out := BuildSubscriptionEdges(tmpl, refs)
	edges := out["sender"]
	if len(edges) != 1 {
		t.Fatalf("expected dedup → 1 edge, got %d", len(edges))
	}
}

func TestBuildSubscriptionEdges_FrameDefaults(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "x", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "y", On: "state"},                               // per-node default → "in"
				{Instance: true, On: "state"},                          // cross-cutting default → "next"
				{Node: "y", On: "state", When: "fresh", Frame: "next"}, // explicit override
			},
		},
		{Type: "y", Executor: "stub"},
	}}
	out := BuildSubscriptionEdges(tmpl, nil)
	yEdges := out["y"]
	if len(yEdges) != 2 {
		t.Fatalf("want 2 edges keyed under sender 'y', got %d", len(yEdges))
	}
	frames := []string{}
	for _, e := range yEdges {
		frames = append(frames, e.Frame)
	}
	sort.Strings(frames)
	if !reflect.DeepEqual(frames, []string{"in", "next"}) {
		t.Fatalf("frame defaults mismatch: %v", frames)
	}
	crossEdges := out[""]
	if len(crossEdges) != 1 {
		t.Fatalf("want 1 cross-cutting edge, got %d", len(crossEdges))
	}
	if crossEdges[0].Frame != "next" {
		t.Fatalf("cross-cutting default frame should be next, got %q", crossEdges[0].Frame)
	}
}

func keys(m map[string][]SubscriptionEdge) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
