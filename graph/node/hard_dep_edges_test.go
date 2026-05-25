// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"strings"
	"testing"

	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

// hardDepSchema is a tiny helper for assembling an attributes schema
// with a `hard_dep` flag on one field.
func hardDepSchema(field, source string, hardDep bool) *spec.NodeAttributesDef {
	prop := map[string]any{
		"source": source,
	}
	if hardDep {
		prop["hard_dep"] = true
	}
	return &spec.NodeAttributesDef{
		Schema: map[string]any{
			"properties": map[string]any{
				field: prop,
			},
		},
	}
}

func TestBuildHardDepEdges_NoHardDep(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub"},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", false),
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

func TestBuildHardDepEdges_SimpleHardDep(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub"},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", true),
		},
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
		{Type: "a", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", true),
		},
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("self-reference must be excluded, got %v", out)
	}
}

func TestBuildHardDepEdges_CycleDetected(t *testing.T) {
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub",
			Attributes: hardDepSchema("y", "{{nodes.b.attribute.foo}}", true),
		},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", true),
		},
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
	// Two disjoint hard-dep cycles in a single template. The cycle
	// detector must surface both cycles in one error so template
	// authors can fix all topology issues in one round (rather than
	// playing whack-a-mole with one cycle at a time).
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		// Cycle 1: a ↔ b
		{Type: "a", Executor: "stub",
			Attributes: hardDepSchema("y", "{{nodes.b.attribute.foo}}", true),
		},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", true),
		},
		// Cycle 2: c ↔ d (disjoint from cycle 1).
		{Type: "c", Executor: "stub",
			Attributes: hardDepSchema("y", "{{nodes.d.attribute.foo}}", true),
		},
		{Type: "d", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.c.attribute.foo}}", true),
		},
	}}
	_, err := BuildHardDepEdges(tmpl)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cycle") {
		t.Fatalf("error %q does not mention cycle", msg)
	}
	// Both cycles must be mentioned. The detector reports cycles as
	// the node-types involved; both [a b] and [c d] components must
	// appear in the surfaced error string.
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Fatalf("error %q does not mention cycle 1 (a↔b)", msg)
	}
	if !strings.Contains(msg, "c") || !strings.Contains(msg, "d") {
		t.Fatalf("error %q does not mention cycle 2 (c↔d)", msg)
	}
	// The aggregate-error form should signal "more than one cycle".
	if !strings.Contains(msg, "(2)") && !strings.Contains(msg, "cycles") {
		t.Fatalf("error %q should signal multi-cycle aggregate (e.g. %q or %q)",
			msg, "(2)", "cycles")
	}
}

func TestBuildHardDepEdges_RejectsFanoutTarget(t *testing.T) {
	// A hard_dep edge pointing at a fan-out node-type is ambiguous —
	// the runtime pullHardDepUpstreams picks a single upstream per type
	// per instance, which is undefined for multi-instance fan-out. The
	// validator must reject this at registration.
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub",
			FanOut: &spec.FanOutSpec{
				Claim:            "alias",
				PartitionRequest: "all",
				ErrorPolicy:      spec.AggregationPolicy{Kind: spec.AggregationKindStrict},
			},
		},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{nodes.a.attribute.foo}}", true),
		},
	}}
	_, err := BuildHardDepEdges(tmpl)
	if err == nil {
		t.Fatalf("expected fan-out rejection error")
	}
	if !strings.Contains(err.Error(), "fan-out") {
		t.Fatalf("error %q does not mention fan-out", err.Error())
	}
}

func TestBuildHardDepEdges_NonAttributeKindIgnored(t *testing.T) {
	// Defensively: hard_dep on a claim-payload source must not produce
	// an edge. The validator separately rejects this shape, but the
	// edge builder is robust against it.
	tmpl := spec.TemplateSpec{Nodes: []spec.TemplateNodeDef{
		{Type: "a", Executor: "stub"},
		{Type: "b", Executor: "stub",
			Attributes: hardDepSchema("x", "{{claim.alias.payload.foo}}", true),
		},
	}}
	out, err := BuildHardDepEdges(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty map (non-attribute source kind ignored), got %v", out)
	}
}
