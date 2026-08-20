// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
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
				{Node: "sender", Type: "terminal/success", ForceUpstreamRefresh: spec.BoolPtr(false)},
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
	if matched[0].ForceUpstreamRefresh {
		t.Errorf("ForceUpstreamRefresh: got true, want false")
	}
}

// @decision: subscription-edges-only-from-explicit-block
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
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("stage", signal.TypePath("attribute/out/changed"))
	if len(matched) != 0 {
		t.Fatalf("substitution ref alone must not register an edge; got %d matched", len(matched))
	}
}

// @decision: subscription-edges-only-from-explicit-block
func TestBuildSubscriptionEdges_NoImplicitEdgeFromMessageRef(t *testing.T) {
	tmpl := spec.TemplateSpec{
		Messages: []spec.MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []spec.TemplateNodeDef{
			{Type: "receiver", Executor: "stub",
				Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.reason}}",
						},
					},
				}}},
		},
	}
	msgRefs := ExtractMessageRefsFromTemplate(tmpl)
	out, err := BuildSubscriptionEdges(tmpl, msgRefs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("ping/recheck", signal.TypePath("terminal/success"))
	if len(matched) != 0 {
		t.Fatalf("a messages.* reference alone (no explicit subscribes entry) must not register an edge; got %d matched", len(matched))
	}
	for _, sender := range out.Senders() {
		if sender == "ping/recheck" {
			t.Fatalf("BuildSubscriptionEdges must not register any sender key for an uncovered message ref; got sender %q", sender)
		}
	}
}

// @concept: template
func TestBuildSubscriptionEdges_NoImplicitEdgeFromEnvRef(t *testing.T) {
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
					"in": map[string]any{"type": "string", "source": "{{env.SOME_VAR}}"},
				},
			}}},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	// @decision: structural-root-edges-derived-on-demand
	for _, sender := range out.Senders() {
		if sender != "" {
			t.Fatalf("an env directive alone must not register a cascade-coupling subscription edge "+
				"(only the sanctioned structural-root \"\" sender key is allowed here); got unexpected "+
				"sender key %q", sender)
		}
	}

	nodeRefs := ExtractSubstitutionRefsFromTemplate(tmpl)
	if len(nodeRefs) != 0 {
		t.Fatalf("ExtractSubstitutionRefsFromTemplate must not surface an env directive as a node ref; got %v", nodeRefs)
	}
	msgRefs := ExtractMessageRefsFromTemplate(tmpl)
	if len(msgRefs) != 0 {
		t.Fatalf("ExtractMessageRefsFromTemplate must not surface an env directive as a message ref; got %v", msgRefs)
	}
}

func TestBuildSubscriptionEdges_Dedup(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "attribute/out/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("attribute/out/changed"))
	if len(matched) != 1 {
		t.Fatalf("expected dedup → 1 matched edge, got %d", len(matched))
	}
}

func TestBuildSubscriptionEdges_Dedup_SameWhenText(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("terminal/success"))
	if len(matched) != 1 {
		t.Fatalf("two identical when: entries must dedup to 1 matched edge, got %d", len(matched))
	}
}

func TestBuildSubscriptionEdges_NoDedup_DifferentWhenText(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "terminal/success", When: "payload.changed == true", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("terminal/success"))
	if len(matched) != 2 {
		t.Fatalf("two distinct when: entries must not dedup, got %d matched", len(matched))
	}
}

func TestBuildSubscriptionEdges_FlagsDistinguishEdges(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "attribute/out/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
				{Node: "sender", Type: "attribute/out/changed", ForceUpstreamRefresh: spec.BoolPtr(true)},
				{Node: "sender", Type: "attribute/out/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("sender", signal.TypePath("attribute/out/changed"))
	if len(matched) != 2 {
		t.Fatalf("expected 2 distinct edges (one per force_upstream_refresh value), got %d", len(matched))
	}
	seen := map[bool]bool{}
	for _, e := range matched {
		seen[e.ForceUpstreamRefresh] = true
	}
	for _, want := range []bool{false, true} {
		if !seen[want] {
			t.Errorf("missing edge with ForceUpstreamRefresh=%t", want)
		}
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

// @decision: structural-root-edges-derived-on-demand
// @story: empty-message-wakes-roots
func TestBuildSubscriptionEdges_StructuralRootInjection(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "root-a", Executor: "stub"},
		{Type: "root-b", Executor: "stub"},
		{Type: "downstream", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "root-a", Type: "terminal/success", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("", signal.TypePath("terminal/success"))
	rootSeen := map[string]bool{}
	for _, e := range matched {
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

// @decision: structural-root-edges-derived-on-demand
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
	out, err := BuildSubscriptionEdges(tmpl, nil)
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

func TestSubscriptionEdgeMap_Match_EmptySenderIsolated(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "root", Executor: "stub"},
		{Type: "executor-foo", Executor: "stub"},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	fromFoo := out.Match("executor-foo", signal.TypePath("terminal/success"))
	for _, e := range fromFoo {
		if e.ReceiverNodeType == "root" {
			t.Errorf("Match(executor-foo) surfaced structural-root edge for %q — must be isolated to empty-sender lookups", e.ReceiverNodeType)
		}
	}
	fromEmpty := out.Match("", signal.TypePath("terminal/success"))
	sawRoot := false
	for _, e := range fromEmpty {
		if e.ReceiverNodeType == "root" {
			sawRoot = true
		}
	}
	if !sawRoot {
		t.Errorf("Match(\"\") missing structural-root edge for root")
	}
}

func TestSubscriptionEdgeMap_PrefixWildcardMatch(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/error/*", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
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

func TestBuildSubscriptionEdges_Error_MissingForceUpstreamRefresh(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "sender", Executor: "stub"},
		{Type: "receiver", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "sender", Type: "terminal/success"},
			},
		},
	}}
	_, err := BuildSubscriptionEdges(tmpl, nil)
	if err == nil {
		t.Fatal("expected an error for a subscribes entry with no force_upstream_refresh")
	}
	if !strings.Contains(err.Error(), "missing force_upstream_refresh") {
		t.Fatalf("expected error naming the missing force_upstream_refresh, got: %v", err)
	}
}

// @decision: structural-root-edges-derived-on-demand
// @story: empty-message-wakes-roots
func TestBuildSubscriptionEdges_MessageRefSuppressesStructuralRoot(t *testing.T) {
	tmpl := spec.TemplateSpec{
		Messages: []spec.MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []spec.TemplateNodeDef{
			{Type: "receiver", Executor: "stub",
				Attributes: &spec.NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.reason}}",
						},
					},
				}}},
		},
	}

	withoutRefs, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges (nil messageRefs): %v", err)
	}
	rootless := false
	for _, e := range withoutRefs.Match("", signal.TypePath("terminal/success")) {
		if e.ReceiverNodeType == "receiver" {
			rootless = true
		}
	}
	if !rootless {
		t.Fatalf("with nil messageRefs, receiver has no other upstream and must be a structural root")
	}

	msgRefs := ExtractMessageRefsFromTemplate(tmpl)
	if len(msgRefs["receiver"]) == 0 {
		t.Fatalf("ExtractMessageRefsFromTemplate must surface the messages.* ref keyed by receiver type; got %v", msgRefs)
	}
	withRefs, err := BuildSubscriptionEdges(tmpl, msgRefs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges (with messageRefs): %v", err)
	}
	for _, e := range withRefs.Match("", signal.TypePath("terminal/success")) {
		if e.ReceiverNodeType == "receiver" {
			t.Fatalf("a receiver with an (even uncovered) messages.* ref must not be injected as a structural root " +
				"once its messageRefs entry is supplied — the ref counts as upstream coupling")
		}
	}
}

func TestBuildSubscriptionEdges_ResolvesViaCallingNodePlumbed(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "inner-entry", Executor: "stub"},
		{Type: "inner-mid", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "inner-entry", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false), ResolvesViaCallingNode: true},
			},
		},
		{Type: "inner-exit", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{
				{Node: "inner-mid", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false)},
			},
		},
	}}
	out, err := BuildSubscriptionEdges(tmpl, nil)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := out.Match("inner-entry", signal.TypePath("terminal/success"))
	if len(matched) != 1 || !matched[0].ResolvesViaCallingNode {
		t.Fatalf("entry-alias edge must carry ResolvesViaCallingNode; got %+v", matched)
	}
	plain := out.Match("inner-mid", signal.TypePath("terminal/success"))
	if len(plain) != 1 || plain[0].ResolvesViaCallingNode {
		t.Fatalf("internal-to-internal edge must not carry ResolvesViaCallingNode; got %+v", plain)
	}

	aliasSenders := out.CallingNodeSenderTypesForReceiver("inner-mid")
	if len(aliasSenders) != 1 || aliasSenders[0] != "inner-entry" {
		t.Fatalf("CallingNodeSenderTypesForReceiver(inner-mid): got %v, want [inner-entry]", aliasSenders)
	}
	if got := out.CallingNodeSenderTypesForReceiver("inner-exit"); len(got) != 0 {
		t.Fatalf("CallingNodeSenderTypesForReceiver(inner-exit): got %v, want none", got)
	}
}
