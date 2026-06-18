// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"
)

func newSeededAliases(t *testing.T, kind, alias string) *KindAliasMap {
	t.Helper()
	m := NewKindAliasMap()
	if err := m.Register(kind, alias); err != nil {
		t.Fatalf("seed alias %q→%q: %v", kind, alias, err)
	}
	return m
}

func TestKindAliasMap_DuplicateRegisterReturnsError(t *testing.T) {
	m := NewKindAliasMap()
	if err := m.Register("loop_counter", "rimsky.loop_counter"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := m.Register("loop_counter", "rimsky.something_else")
	if err == nil {
		t.Fatalf("expected duplicate registration to return an error")
	}
	if !strings.Contains(err.Error(), "duplicate registration") {
		t.Fatalf("expected duplicate-registration error, got %q", err.Error())
	}
	alias, ok := m.Resolve("loop_counter")
	if !ok || alias != "rimsky.loop_counter" {
		t.Fatalf("original mapping should be preserved, got alias=%q ok=%v", alias, ok)
	}
}

func TestKindAliasMap_RegisterAndResolve(t *testing.T) {
	m := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	alias, ok := m.Resolve("loop_counter")
	if !ok {
		t.Fatalf("expected loop_counter to resolve")
	}
	if alias != "rimsky.loop_counter" {
		t.Fatalf("expected alias rimsky.loop_counter, got %q", alias)
	}
	if _, ok := m.Resolve("unknown"); ok {
		t.Fatalf("expected unknown kind to not resolve")
	}
}

func TestValidateKindDeclaration_HappyPath(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "lc",
			Kind: "loop_counter",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{KindAliases: aliases})
	if !res.Ok() {
		t.Fatalf("expected validation OK, got errors=%+v", res.Errors)
	}
}

func TestValidateKindDeclaration_RejectsMixedKindAndExecutor(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "n",
			Kind:     "loop_counter",
			Executor: "foo",
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

func TestValidateKindDeclaration_RejectsUnknownKind(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "n",
			Kind: "unknown_kind",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{KindAliases: aliases})
	if res.Ok() {
		t.Fatalf("expected validation to fail for unknown kind")
	}
	if !findErrorContains(res.Errors, "is not registered") {
		t.Fatalf("expected error mentioning 'is not registered', got %+v", res.Errors)
	}
}

func TestValidateKindDeclaration_NilAliasesRejectsAnyKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "n",
			Kind: "loop_counter",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if res.Ok() {
		t.Fatalf("expected validation to fail when no KindAliases configured")
	}
	if !findErrorContains(res.Errors, "no kind aliases configured") {
		t.Fatalf("expected error mentioning 'no kind aliases configured', got %+v", res.Errors)
	}
}

func TestValidateKindDeclaration_NoKindNoExecutorIsLegal(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "pure",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	if !res.Ok() {
		t.Fatalf("expected pure-cascade node to validate OK, got %+v", res.Errors)
	}
}

func TestCanonicalizeKindSugar_RewritesKindToExecutor(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "lc",
			Kind: "loop_counter",
		}},
	}
	CanonicalizeKindSugar(spec, aliases)
	got := spec.Nodes[0]
	if got.Kind != "" {
		t.Fatalf("expected Kind cleared, got %q", got.Kind)
	}
	if got.Executor != "rimsky.loop_counter" {
		t.Fatalf("expected Executor rimsky.loop_counter, got %q", got.Executor)
	}
}

func TestCanonicalizeKindSugar_Idempotent(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "t",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "x",
			Executor: "rimsky.loop_counter",
		}},
	}
	CanonicalizeKindSugar(spec, aliases)
	if spec.Nodes[0].Executor != "rimsky.loop_counter" {
		t.Fatalf("expected Executor unchanged, got %q", spec.Nodes[0].Executor)
	}
	if spec.Nodes[0].Kind != "" {
		t.Fatalf("expected Kind unchanged (empty), got %q", spec.Nodes[0].Kind)
	}
}

func TestCanonicalizeKindSugar_NilSafe(t *testing.T) {
	CanonicalizeKindSugar(nil, nil)
	CanonicalizeKindSugar(&TemplateSpec{}, nil)
	aliases := newSeededAliases(t, "k", "alias")
	CanonicalizeKindSugar(nil, aliases)
}

func findErrorContains(errs []ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Msg, substr) {
			return true
		}
	}
	return false
}
