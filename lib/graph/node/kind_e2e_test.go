// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import "testing"

// TestKindE2E_LoopCounterReferenceAcceptsAndCanonicalizes pins the
// end-to-end registration contract for STORY-inproc-utility-executor:
// a template declaring `kind: loop_counter` validates with seeded
// KindAliases, then canonicalizes to `executor: <alias>` with `kind`
// cleared. Asserting on the canonicalized shape proves the two steps
// (validation + canonicalization) compose so the persisted spec is in
// normal form.
//
// The test uses the same kind name + alias string the loop_counter
// builtin package exports (`loop_counter.KindName` / `.ExecutorAlias`)
// so it would fail if those constants ever drifted. They are hard-coded
// here rather than imported to keep the test in graph/node (the layer
// that owns the validator + canonicalizer), avoiding a cyclic-layer
// pull on lib/runtime/executor/builtin/loop_counter.
const (
	e2eLoopCounterKind  = "loop_counter"
	e2eLoopCounterAlias = "rimsky.loop_counter"
)

func TestKindE2E_LoopCounterReferenceAcceptsAndCanonicalizes(t *testing.T) {
	aliases := NewKindAliasMap()
	if err := aliases.Register(e2eLoopCounterKind, e2eLoopCounterAlias); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	spec := &TemplateSpec{
		Name:                "loop-template",
		Version:             "1",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "counter",
			Kind: e2eLoopCounterKind,
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{KindAliases: aliases})
	if !res.Ok() {
		t.Fatalf("expected validation OK for kind: loop_counter, got errors=%+v", res.Errors)
	}

	CanonicalizeKindSugar(spec, aliases)

	got := spec.Nodes[0]
	if got.Kind != "" {
		t.Fatalf("expected Kind cleared after canonicalization, got %q", got.Kind)
	}
	if got.Executor != e2eLoopCounterAlias {
		t.Fatalf("expected Executor=%q after canonicalization, got %q", e2eLoopCounterAlias, got.Executor)
	}
}

func TestKindE2E_MixedKindAndExecutorRejected(t *testing.T) {
	aliases := NewKindAliasMap()
	if err := aliases.Register(e2eLoopCounterKind, e2eLoopCounterAlias); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	spec := &TemplateSpec{
		Name:                "bad-template",
		Version:             "1",
		FrameResolutionMode: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "n",
			Kind:     e2eLoopCounterKind,
			Executor: "some-other-executor",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{KindAliases: aliases})
	if res.Ok() {
		t.Fatalf("expected validation to fail for mixed kind+executor")
	}
	if !findErrorContains(res.Errors, "declares both") {
		t.Fatalf("expected error mentioning 'declares both', got %+v", res.Errors)
	}
}
