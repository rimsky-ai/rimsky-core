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
	// @deliberate: Cross-cutting edge fires for any sender via the
	// empty-key match path.
	matched := out.Match("any-sender", signal.TypePath("terminal/error/rate_limited"))
	if len(matched) != 1 {
		t.Fatalf("cross-cutting prefix match: want 1, got %d", len(matched))
	}
}

// TestBuildSubscriptionEdges_NoImplicitEdgeFromSubstitutionRef pins
// the post-2026-06-14 model: a substitution ref in a receiver's
// attribute schema produces NO edge in the inverse map. The receiver
// must declare an explicit `subscribes:` entry naming the sender for
// the cascade to wire. The registration-time coverage check (Pass 2)
// rejects a template with an uncovered ref; in this test we just
// confirm the edge map is empty when no explicit entry exists.
//
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
			// @deliberate: no Subscribes block — the substitution ref
			// alone must not register an edge.
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

// TestBuildSubscriptionEdges_Dedup pins that two explicit entries with
// the same (sender, type, when=nil, scope, flags) tuple dedup to one
// edge. Both flag values must match for content-equality; entries that
// differ only in flag values are NOT deduped (see
// TestBuildSubscriptionEdges_FlagsDistinguishEdges below) — the
// validator rejects flag-conflicting duplicates so by the time this
// builder runs, only flag-coherent duplicates exist to collapse.
func TestBuildSubscriptionEdges_Dedup(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				// @deliberate: Content-equal duplicate — collapses at insert time.
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

// TestBuildSubscriptionEdges_FlagsDistinguishEdges pins that two
// entries matching on (sender, type, when, scope) but DIFFERING in
// either cascade-shape flag (WakeOnChange or ForceUpstreamRefresh)
// land as two distinct edges rather than silently deduping to the
// first. Without this, an author-declared flag value would be dropped
// invisibly — exactly the invisible behavior
// decision:cascade-flags-required-no-defaults set out to eliminate.
// The validator rejects this combination as a flag-conflict at
// registration; this test exercises the edge builder directly to
// confirm full-edge-equality is the dedup contract.
func TestBuildSubscriptionEdges_FlagsDistinguishEdges(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				// @deliberate: same key — different ForceUpstreamRefresh flag value.
				{Node: "sender", Type: "attribute/out/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(true)},
				// @deliberate: same key — different WakeOnChange flag value.
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
	// @deliberate: confirm each combination is present — order is implementation-defined.
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

// TestBuildSubscriptionEdges_CrossCuttingAndPerNodeBothMatch confirms
// that a sender keyed under the per-node bucket AND the cross-cutting
// (empty-sender) bucket both surface on a Match — the cascade walker's
// one in-frame path applies to every matching edge regardless of
// whether the subscription named the sender explicitly or via
// `instance: true`.
func TestBuildSubscriptionEdges_CrossCuttingAndPerNodeBothMatch(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "x", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				// @deliberate: per-node entry — names the sender explicitly.
				{Node: "y", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				// @deliberate: cross-cutting entry — `instance: true` lives
				// under the empty sender-key bucket.
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
	// @deliberate: one y-keyed edge + the cross-cutting one = two matches total.
	if len(yMatches) != 2 {
		t.Fatalf("want 2 edges for sender y (per-node + cross-cutting), got %d", len(yMatches))
	}
}

// TestParseSubstitutionDirective_BareEventRejected — bare-form event
// pulls still require the event name. A `{{nodes.X.event}}` directive
// (no event name) is malformed; parseSubstitutionDirective returns
// ok=false. Tests the parsing primitive used by the coverage check.
func TestParseSubstitutionDirective_BareEventRejected(t *testing.T) {
	got, ok := parseSubstitutionDirective("nodes.emit.event")
	if ok {
		t.Fatalf("parseSubstitutionDirective(`nodes.emit.event`) expected ok=false; got %+v", got)
	}
}

// TestParseSubstitutionDirective_BareEventWithName — `{{nodes.X.event.<name>}}`
// (no trailing path) is the bare-event form. The parser yields
// (sender=X, kind=event, Name=<name>).
func TestParseSubstitutionDirective_BareEventWithName(t *testing.T) {
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
				{Node: "sender", Type: "terminal/error/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	// @deliberate: Fires for any terminal/error/* leaf.
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
	// @deliberate: Does NOT fire for terminal/success.
	if got := out.Match("sender", signal.TypePath("terminal/success")); len(got) != 0 {
		t.Errorf("Match(terminal/success): want 0, got %d", len(got))
	}
}
