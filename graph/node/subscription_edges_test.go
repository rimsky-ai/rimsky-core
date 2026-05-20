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

// TestBuildSubscriptionEdges_BareAttributePull — spec 2026-05-19 §Item 3
// "Empty trailing path". A receiver with `source: "{{nodes.stage.attribute}}"`
// (no field path) pulls the whole upstream attribute object. The
// inverse-edge map must still emit an auto-subscribe entry for the
// receiver so the cascade walk fires when `stage` invalidates. The Name
// filter is empty for the bare form (the cascade walk does not filter on
// attribute name; the filter is informational).
func TestBuildSubscriptionEdges_BareAttributePull(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "stage", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"out": map[string]any{"type": "string"},
				},
			}}},
		{Type: "verify", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"whole": map[string]any{
						"type":   "object",
						"source": "{{nodes.stage.attribute}}",
					},
				},
			}}},
	}}
	refs := ExtractSubstitutionRefsFromTemplate(tmpl)
	out := BuildSubscriptionEdges(tmpl, refs)
	edges, ok := out["stage"]
	if !ok {
		t.Fatalf("expected implicit subscription on 'stage'; got keys %v", keys(out))
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 implicit edge, got %d", len(edges))
	}
	if edges[0].ReceiverNodeType != "verify" {
		t.Errorf("ReceiverNodeType: got %q want verify", edges[0].ReceiverNodeType)
	}
	if edges[0].TopicKind != "attribute" {
		t.Errorf("TopicKind: got %q want attribute", edges[0].TopicKind)
	}
	if edges[0].Filter.Name != "" {
		t.Errorf("Filter.Name on bare-attribute pull: got %q want empty", edges[0].Filter.Name)
	}
}

// TestBuildSubscriptionEdges_BareEventPullRejected — bare-form event
// pulls still require the event name. A `{{nodes.X.event}}` directive
// (no event name) is malformed; parseSubstitutionDirective returns
// ok=false and no inverse-edge is emitted. (The validator separately
// rejects the directive at registration; this test pins the lower-level
// parser's behaviour for safety.)
func TestBuildSubscriptionEdges_BareEventPullRejected(t *testing.T) {
	got, ok := parseSubstitutionDirective("nodes.emit.event")
	if ok {
		t.Fatalf("parseSubstitutionDirective(`nodes.emit.event`) expected ok=false; got %+v", got)
	}
}

// TestBuildSubscriptionEdges_BareEventWithNamePull — `{{nodes.X.event.<name>}}`
// (no trailing path) is the bare-event form. The inverse-edge map
// records (sender=X, kind=event, Name=<name>) so the cascade walk fires
// on any emission of that event.
func TestBuildSubscriptionEdges_BareEventWithNamePull(t *testing.T) {
	ref, ok := parseSubstitutionDirective("nodes.emit.event.progress")
	if !ok {
		t.Fatalf("parseSubstitutionDirective: expected ok=true for bare-event form")
	}
	if ref.TopicKind != "event" || ref.Name != "progress" {
		t.Errorf("ref mismatch: %+v", ref)
	}
}
