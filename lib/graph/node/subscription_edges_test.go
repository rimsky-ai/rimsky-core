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
	out, err := BuildSubscriptionEdges(spec.TemplateSpec{}, nil, nil)
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
				{Node: "sender", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
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
	if !matched[0].WakeOnChange {
		t.Errorf("WakeOnChange: got false, want true")
	}
	if matched[0].ForceUpstreamRefresh {
		t.Errorf("ForceUpstreamRefresh: got true, want false")
	}
}

func TestBuildSubscriptionEdges_CrossCutting(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "cleanup", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Instance: true, Type: "terminal/error/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
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
	matched := out.Match("any-sender", signal.TypePath("terminal/error/rate_limited"))
	if len(matched) != 1 {
		t.Fatalf("cross-cutting prefix match: want 1, got %d", len(matched))
	}
}

//	@decision: subscription-edges-only-from-explicit-block
func TestBuildSubscriptionEdges_NoImplicitEdgeFromSubstitutionRef(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "stage", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"out": map[string]any{"type": "string", "source": "out_value"},
				},
			}}},
		{Type: "verify", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"in": map[string]any{"type": "string", "source": "{{nodes.stage.attribute.out}}"},
				},
			}}},
	}}
	refs := ExtractSubstitutionRefsFromTemplate(tmpl)
	out, err := BuildSubscriptionEdges(tmpl, refs, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("stage", signal.TypePath("attribute/out/changed"))
	if len(matched) != 0 {
		t.Fatalf("substitution ref alone must not register an edge; got %d matched", len(matched))
	}
}

func TestBuildSubscriptionEdges_Dedup(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	refs := ExtractSubstitutionRefsFromTemplate(tmpl)
	out, err := BuildSubscriptionEdges(tmpl, refs, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("attribute/out/changed"))
	if len(matched) != 1 {
		t.Fatalf("expected dedup → 1 matched edge, got %d", len(matched))
	}
}

func TestBuildSubscriptionEdges_FlagsDistinguishEdges(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(true)},
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(false), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("attribute/out/changed"))
	if len(matched) != 3 {
		t.Fatalf("expected 3 distinct edges (one per flag combination), got %d", len(matched))
	}
	seen := map[[2]bool]bool{}
	for _, e := range matched {
		seen[[2]bool{e.WakeOnChange, e.ForceUpstreamRefresh}] = true
	}
	for _, want := range [][2]bool{{true, false}, {true, true}, {false, false}} {
		if !seen[want] {
			t.Errorf("missing edge with WakeOnChange=%t ForceUpstreamRefresh=%t", want[0], want[1])
		}
	}
}

func TestBuildSubscriptionEdges_CrossCuttingAndPerNodeBothMatch(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "x", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "y", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Instance: true, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
		{Type: "y", Executor: "stub"},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	yMatches := out.Match("y", signal.TypePath("terminal/success"))
	if len(yMatches) != 2 {
		t.Fatalf("want 2 edges for sender y (per-node + cross-cutting), got %d", len(yMatches))
	}
}

func TestParseSubstitutionDirective_EventFormRetired(t *testing.T) {
	for _, body := range []string{
		"nodes.emit.event",
		"nodes.emit.event.progress",
		"nodes.emit.event.progress.field",
	} {
		got, ok := parseSubstitutionDirective(body)
		if ok {
			t.Fatalf("parseSubstitutionDirective(%q) expected ok=false (event form retired); got %+v", body, got)
		}
	}
}

//	@decision: structural-root-edge-injection-at-registration
//	@story: empty-message-wakes-roots
func TestBuildSubscriptionEdges_StructuralRootInjection(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "root-a", Executor: "stub"},
		{Type: "root-b", Executor: "stub"},
		{Type: "downstream", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "root-a", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("", signal.TypePath("terminal/success"))
	rootSeen := map[string]bool{}
	for _, e := range matched {
		if !e.SenderBoundToEmpty {
			t.Errorf("Match(\"\", terminal/success) surfaced a non-root edge for %q (SenderBoundToEmpty=false)", e.ReceiverNodeType)
		}
		rootSeen[e.ReceiverNodeType] = true
	}
	for _, want := range []string{"root-a", "root-b"} {
		if !rootSeen[want] {
			t.Errorf("Match(\"\", terminal/success) missing structural-root edge for %q", want)
		}
	}
	if rootSeen["downstream"] {
		t.Errorf("downstream node must not have a structural-root edge — it has an upstream subscription")
	}
}

//	@decision: structural-root-edge-injection-at-registration
func TestBuildSubscriptionEdges_StructuralRootInjection_CrossCuttingOnly(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "monitor", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Instance: true, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
		{Type: "root", Executor: "stub"},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("", signal.TypePath("terminal/success"))
	for _, e := range matched {
		if e.ReceiverNodeType == "monitor" && e.SenderBoundToEmpty {
			t.Errorf("monitor is cross-cutting-only; must not gain a structural-root edge (SenderBoundToEmpty=true)")
		}
	}
	sawRoot := false
	for _, e := range matched {
		if e.ReceiverNodeType == "root" && e.SenderBoundToEmpty {
			sawRoot = true
		}
	}
	if !sawRoot {
		t.Errorf("Match(\"\", terminal/success) missing structural-root edge for \"root\"")
	}
}

//	@decision: structural-root-edge-injection-at-registration
func TestBuildSubscriptionEdges_StructuralRootInjection_AttributeRef(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "upstream", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"out": map[string]any{"type": "string", "source": "v"},
				},
			}}},
		{Type: "receiver", Executor: "stub",
			Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"in": map[string]any{"type": "string", "source": "{{nodes.upstream.attribute.out}}"},
				},
			}}},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("", signal.TypePath("terminal/success"))
	for _, e := range matched {
		if e.ReceiverNodeType == "receiver" {
			t.Errorf("receiver names an upstream via substitution ref; must not be classified a structural root")
		}
	}
}

//     @decision: empty-sender-key-edge-disambiguation
func TestSubscriptionEdgeMap_Match_StructuralRootDisambiguation(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "cleanup", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Instance: true, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
		{Type: "root", Executor: "stub"},
		{Type: "executor-foo", Executor: "stub"},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	fromFoo := out.Match("executor-foo", signal.TypePath("terminal/success"))
	sawCross := false
	for _, e := range fromFoo {
		if e.SenderBoundToEmpty {
			t.Errorf("Match(executor-foo) surfaced structural-root edge for %q — must be suppressed under non-empty sender", e.ReceiverNodeType)
		}
		if e.ReceiverNodeType == "cleanup" && !e.SenderBoundToEmpty {
			sawCross = true
		}
	}
	if !sawCross {
		t.Errorf("Match(executor-foo) missing cross-cutting edge for cleanup")
	}
	fromEmpty := out.Match("", signal.TypePath("terminal/success"))
	sawCleanupCross := false
	sawRootInjected := false
	for _, e := range fromEmpty {
		if e.ReceiverNodeType == "cleanup" && !e.SenderBoundToEmpty {
			sawCleanupCross = true
		}
		if e.ReceiverNodeType == "root" && e.SenderBoundToEmpty {
			sawRootInjected = true
		}
	}
	if !sawCleanupCross {
		t.Errorf("Match(\"\") missing cross-cutting edge for cleanup")
	}
	if !sawRootInjected {
		t.Errorf("Match(\"\") missing structural-root edge for root")
	}
}

func TestSubscriptionEdgeMap_PrefixWildcardMatch(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/error/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
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
	if got := out.Match("sender", signal.TypePath("terminal/success")); len(got) != 0 {
		t.Errorf("Match(terminal/success): want 0, got %d", len(got))
	}
}
