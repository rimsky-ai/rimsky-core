// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	foundationspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @decision: pre-v1-pure-removal-for-retired-surfaces
func TestValidateErrorTypes_RejectsUnknown(t *testing.T) {
	retiredNames := []string{
		"invalidate",
		"resume_then_retry",
		"discard_then_retry",
		"discard_claims_then_retry",
		"foo",
	}
	for _, action := range retiredNames {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Action: action},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].error_types[some_error].action")
		})
	}
}

func TestValidateErrorTypes_AcceptsCanonical(t *testing.T) {
	for _, action := range []string{"pass", "give_up", "retry", "release_and_requeue"} {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Action: action},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			for _, e := range res.Errors {
				if e.Path == "nodes[1].error_types[some_error].action" {
					t.Fatalf("unexpected action-vocabulary error for %q: %s", action, e.Msg)
				}
			}
		})
	}
}

func TestValidateErrorTypes_AcceptsDeclaredHttpClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/timeout": {Action: "give_up"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

func TestValidateErrorTypes_AcceptsDeclaredWildcardClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/server_error/500": {Action: "retry"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/server_error/*"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

func TestValidateErrorTypes_AcceptsRuntimeSynthesizedExecutorSyncTimeout(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"executor_sync_timeout": {Action: "retry"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, w := range res.Warnings {
		if strings.HasPrefix(w.Path, "nodes[1].error_types") {
			t.Fatalf("executor_sync_timeout is runtime-synthesized (runner_dispatch.go) and must not warn as undeclared: %+v", w)
		}
	}
}

func TestIsRuntimeSynthesizedErrorClass_MatchesSharedList(t *testing.T) {
	for _, c := range foundationspec.RuntimeSynthesizedErrorClasses {
		if !isRuntimeSynthesizedErrorClass(c) {
			t.Errorf("expected %q (from foundationspec.RuntimeSynthesizedErrorClasses) to be recognized as runtime-synthesized", c)
		}
	}
	if !isRuntimeSynthesizedErrorClass("acquire/unavailable") {
		t.Error("expected acquire/* prefix family to be recognized as runtime-synthesized")
	}
	if isRuntimeSynthesizedErrorClass("http/timeout") {
		t.Error("expected an ordinary executor-declared class to NOT be recognized as runtime-synthesized")
	}
	if isRuntimeSynthesizedErrorClass("retry_loop_no_progress") {
		t.Error("retry_loop_no_progress was retired and must not be treated as runtime-synthesized")
	}
}

func TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Action: "give_up"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return nil, false },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path when hook unavailable: %+v", e)
		}
	}
}

func TestValidateErrorTypes_WarnsUndeclaredWhenHookAvailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Action: "give_up"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "unattributable error_types key must not hard-reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[foo]" {
			found = true
			if !strings.Contains(w.Msg, "not in any declared vocabulary") {
				t.Fatalf("warning must state no declared vocabulary contains the key; got %q", w.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("expected advisory warning at nodes[1].error_types[foo]; warnings: %+v", res.Warnings)
	}
}

func TestValidateErrorTypes_AcceptsProducerDeclaredClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http",
				ClaimProducers: []NodeClaimProducerRef{{Name: "items-store", Alias: "items", Intent: "rw", Selector: "items?category=alpha"}},
				ErrorTypes: map[string]ErrorTypePolicy{
					"pg/claim_unavailable": {Action: "retry"},
				}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		StoreDeclared:                func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		ClaimProducerDeclaredErrorClasses: func(name string) ([]string, bool) {
			if name == "items-store" {
				return []string{"pg/claim_unavailable", "pg/swap_failed"}, true
			}
			return nil, false
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "producer-declared key must validate; errors: %+v", res.Errors)
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			t.Fatalf("producer-declared key must be hard-valid, not a warning: %+v", w)
		}
	}
}

func TestValidateErrorTypes_ProducerClassUnreachableFromNode(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"pg/claim_unavailable": {Action: "retry"},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		ClaimProducerDeclaredErrorClasses: func(string) ([]string, bool) {
			return []string{"pg/claim_unavailable"}, true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "must warn, not reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for producer class unreachable from node; warnings: %+v", res.Warnings)
	}
}
