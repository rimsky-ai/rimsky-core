// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// makeNodeWithRefresh assembles a TemplateNodeDef with a single
// subscribes: entry naming `sender` with force_upstream_refresh: true.
// The signal type is irrelevant to BuildHardDepEdges — only the flag
// and the sender node-type matter.
func makeNodeWithRefresh(receiverType, sender string) spec.TemplateNodeDef {
	return spec.TemplateNodeDef{
		Type:     receiverType,
		Executor: "stub",
		Subscribes: []spec.SubscriptionEntry{
			{
				Node:                 sender,
				Type:                 "attribute/foo/changed",
				WakeOnChange:         spec.BoolPtr(true),
				ForceUpstreamRefresh: spec.BoolPtr(true),
			},
		},
	}
}

func TestBuildHardDepEdges_NoForceRefresh(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub"},
		{Type: "b", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{{
				Node:                 "a",
				Type:                 "attribute/foo/changed",
				WakeOnChange:         spec.BoolPtr(true),
				ForceUpstreamRefresh: spec.BoolPtr(false),
			}},
		},
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %d keys: %v", len(out), out)
	}
}

func TestBuildHardDepEdges_SimpleForceRefresh(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub"},
		makeNodeWithRefresh("b", "a"),
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 key, got %v", out)
	}
	senders := out["b"]
	if len(senders) != 1 || senders[0] != "a" {
		t.Fatalf("expected {b: [a]}, got %v", out)
	}
}

func TestBuildHardDepEdges_SelfReferenceIgnored(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		// @deliberate: a node that names itself in a
		// force_upstream_refresh entry is silently skipped (it doesn't
		// pull itself).
		makeNodeWithRefresh("a", "a"),
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("self-reference must be excluded, got %v", out)
	}
}

func TestBuildHardDepEdges_CrossCuttingIgnored(t *testing.T) {
	// @deliberate: a cross-cutting subscription cannot carry
	// force_upstream_refresh: true (the validator rejects the
	// combination); the edge builder defensively skips it so a
	// malformed input that slipped past validation does not produce
	// a sender-agnostic edge.
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "watcher", Executor: "stub",
			Subscribes: []spec.SubscriptionEntry{{
				Instance:             true,
				Type:                 "terminal/error/*",
				WakeOnChange:         spec.BoolPtr(true),
				ForceUpstreamRefresh: spec.BoolPtr(true),
			}},
		},
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("cross-cutting entries must be excluded, got %v", out)
	}
}

func TestBuildHardDepEdges_CycleDetected(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		makeNodeWithRefresh("a", "b"),
		makeNodeWithRefresh("b", "a"),
	}}
	_, err := BuildHardDepEdges(tmpl)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error %q does not mention cycle", err.Error())
	}
}

func TestBuildHardDepEdges_MultipleCyclesReported(t *testing.T) {
	// @deliberate: two disjoint upstream-refresh cycles in a single
	// template. The cycle detector must surface both cycles in one error
	// so template authors can fix all topology issues in one round
	// (rather than playing whack-a-mole with one cycle at a time).
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		// @deliberate: cycle 1 — a ↔ b.
		makeNodeWithRefresh("a", "b"),
		makeNodeWithRefresh("b", "a"),
		// @deliberate: cycle 2 — c ↔ d, disjoint from cycle 1.
		makeNodeWithRefresh("c", "d"),
		makeNodeWithRefresh("d", "c"),
	}}
	_, err := BuildHardDepEdges(tmpl)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cycle") {
		t.Fatalf("error %q does not mention cycle", msg)
	}
	// @deliberate: Both cycles must be mentioned. The detector reports cycles as
	// cycles as the node-types involved; both [a b] and [c d] components
	// must appear in the surfaced error string.
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Fatalf("error %q does not mention cycle 1 (a↔b)", msg)
	}
	if !strings.Contains(msg, "c") || !strings.Contains(msg, "d") {
		t.Fatalf("error %q does not mention cycle 2 (c↔d)", msg)
	}
	// @deliberate: the aggregate-error form should signal "more than one
	// cycle".
	if !strings.Contains(msg, "(2)") && !strings.Contains(msg, "cycles") {
		t.Fatalf("error %q should signal multi-cycle aggregate (e.g. %q or %q)",
			msg, "(2)", "cycles")
	}
}

func TestBuildHardDepEdges_RejectsFanoutTarget(t *testing.T) {
	// @deliberate: a force-refresh edge pointing at a fan-out node-type
	// is ambiguous — the runtime pullForceRefreshUpstreams picks a
	// single upstream per type per instance, which is undefined for
	// multi-instance fan-out. The validator must reject this at
	// registration.
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub",
			FanOut: &spec.FanOutSpec{
				Claim:            "alias",
				PartitionRequest: "all",
				ErrorPolicy:      spec.AggregationPolicy{Kind: spec.AggregationKindStrict},
			},
		},
		makeNodeWithRefresh("b", "a"),
	}}
	_, err := BuildHardDepEdges(tmpl)
	if err == nil {
		t.Fatalf("expected fan-out rejection error")
	}
	if !strings.Contains(err.Error(), "fan-out") {
		t.Fatalf("error %q does not mention fan-out", err.Error())
	}
}
