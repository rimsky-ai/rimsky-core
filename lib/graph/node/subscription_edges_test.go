// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestBuildSubscriptionEdges_Empty(t *testing.T) {
	out, err := BuildSubscriptionEdges(spec.TemplateSpec{}, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	if len(out.Senders()) != 0 {
		t.Fatalf("empty template should produce empty map; got %d sender keys", len(out.Senders()))
	}
}

func TestBuildSubscriptionEdges_ExplicitDirect(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/success"},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("terminal/success"))
	if len(matched) != 1 {
		t.Fatalf("want 1 matched edge, got %d", len(matched))
	}
	if matched[0].ReceiverNodeType != "receiver" {
		t.Errorf("ReceiverNodeType: got %q want receiver", matched[0].ReceiverNodeType)
	}
	if matched[0].TypePattern != signal.TypePath("terminal/success") {
		t.Errorf("TypePattern: got %q want terminal/success", matched[0].TypePattern)
	}
	if matched[0].Frame != "in" {
		t.Errorf("Frame: got %q want in", matched[0].Frame)
	}
}

func TestBuildSubscriptionEdges_CrossCutting(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "cleanup", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Instance: true, Type: "terminal/error/*"},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	cross := out.CrossCuttingEdges()
	if len(cross) != 1 {
		t.Fatalf("want 1 cross-cutting edge, got %d", len(cross))
	}
	if cross[0].SubscriptionScope != "instance" {
		t.Errorf("want scope=instance, got %q", cross[0].SubscriptionScope)
	}
	if cross[0].Frame != "next" {
		t.Errorf("want default Frame=next for cross-cutting, got %q", cross[0].Frame)
	}
	// Cross-cutting edge fires for any sender via the empty-key match
	// path.
	matched := out.Match("any-sender", signal.TypePath("terminal/error/rate_limited"))
	if len(matched) != 1 {
		t.Fatalf("cross-cutting prefix match: want 1, got %d", len(matched))
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
	out, err := BuildSubscriptionEdges(tmpl, refs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("stage", signal.TypePath("attribute/out/changed"))
	if len(matched) != 1 {
		t.Fatalf("implicit attribute-edge match: want 1, got %d", len(matched))
	}
	if matched[0].ReceiverNodeType != "verify" {
		t.Errorf("implicit receiver: got %q want verify", matched[0].ReceiverNodeType)
	}
	if matched[0].TypePattern != signal.TypePath("attribute/out/changed") {
		t.Errorf("implicit pattern: got %q", matched[0].TypePattern)
	}
}

func TestBuildSubscriptionEdges_UnionAndDedup(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed"},
				// Duplicate of the implicit ref below.
				{Node: "sender", Type: "attribute/out/changed"},
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
	out, err := BuildSubscriptionEdges(tmpl, refs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("attribute/out/changed"))
	// Dedup happens at insert time: the explicit-dup of the same
	// (sender, type, when=nil, scope, frame) tuple collapses to one
	// entry; the implicit ref produces the same tuple and is also
	// deduped. Result: a single matched edge.
	if len(matched) != 1 {
		t.Fatalf("expected dedup → 1 matched edge, got %d", len(matched))
	}
}

func TestBuildSubscriptionEdges_FrameDefaults(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "x", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "y", Type: "terminal/success"},                // per-node default → "in"
				{Instance: true, Type: "terminal/success"},           // cross-cutting default → "next"
				{Node: "y", Type: "terminal/success", Frame: "next"}, // explicit override
			},
		},
		{Type: "y", Executor: "stub"},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	yMatches := out.Match("y", signal.TypePath("terminal/success"))
	// Two y-keyed edges (in + next) + the cross-cutting one (next) =
	// three matches total.
	if len(yMatches) != 3 {
		t.Fatalf("want 3 edges for sender y (including cross-cutting), got %d", len(yMatches))
	}
	frames := map[string]int{}
	for _, e := range yMatches {
		frames[e.Frame]++
	}
	if frames["in"] != 1 {
		t.Errorf("want exactly 1 frame=in edge, got %d", frames["in"])
	}
	if frames["next"] != 2 {
		t.Errorf("want exactly 2 frame=next edges (explicit + cross-cutting), got %d", frames["next"])
	}
}

// TestBuildSubscriptionEdges_BareAttributePull — spec 2026-05-19 §Item 3
// "Empty trailing path". A receiver with `source: "{{nodes.stage.attribute}}"`
// (no field path) pulls the whole upstream attribute object. The
// inverse-edge map emits a prefix-wildcard auto-subscribe entry
// (`attribute/*`) so the cascade walk fires on any attribute change on
// the sender.
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
	out, err := BuildSubscriptionEdges(tmpl, refs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	// Bare-attribute auto-subscribe expands to attribute/*/changed
	// so the implicit subscription scopes to delta signals only.
	matched := out.Match("stage", signal.TypePath("attribute/anykey/changed"))
	if len(matched) != 1 {
		t.Fatalf("bare-attr prefix match: want 1, got %d", len(matched))
	}
	if matched[0].ReceiverNodeType != "verify" {
		t.Errorf("ReceiverNodeType: got %q want verify", matched[0].ReceiverNodeType)
	}
	if matched[0].TypePattern != signal.TypePath("attribute/*/changed") {
		t.Errorf("TypePattern: got %q want attribute/*/changed", matched[0].TypePattern)
	}
}

// TestBuildSubscriptionEdges_BareEventPullRejected — bare-form event
// pulls still require the event name. A `{{nodes.X.event}}` directive
// (no event name) is malformed; parseSubstitutionDirective returns
// ok=false and no inverse-edge is emitted.
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

// TestSubscriptionEdgeMap_PrefixWildcardMatch confirms that a
// trailing-`*` edge inserted at one depth fires for any deeper-or-equal
// signal path.
func TestSubscriptionEdgeMap_PrefixWildcardMatch(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/error/*"},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	// Fires for any terminal/error/* leaf.
	for _, leaf := range []string{
		"terminal/error/foo",
		"terminal/error/http/timeout",
		"terminal/error/agent/rate_limited",
	} {
		matched := out.Match("sender", signal.TypePath(leaf))
		if len(matched) != 1 {
			t.Errorf("Match(%q): want 1, got %d", leaf, len(matched))
		}
	}
	// Does NOT fire for terminal/success.
	if got := out.Match("sender", signal.TypePath("terminal/success")); len(got) != 0 {
		t.Errorf("Match(terminal/success): want 0, got %d", len(got))
	}
}
