// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestValidateSubscriptionDeclaredTags(t *testing.T) {
	t.Parallel()

	declaredHook := func(string) ([]string, bool) { return []string{"declared_tag"}, true }

	t.Run("undeclared tag rejected", func(t *testing.T) {
		t.Parallel()
		res := &ValidationResult{}
		validateSubscriptionDeclaredTags(
			spec.SubscriptionEntry{Node: "a", When: `"undeclared_tag" in payload.tags`},
			"nodes[1].subscribes[0]",
			TemplateNodeDef{Executor: "h"},
			RegistryHooks{ExecutorDeclaredTags: declaredHook},
			res,
		)
		if len(res.Errors) != 1 {
			t.Fatalf("expected exactly one error, got %+v", res.Errors)
		}
		if res.Errors[0].Path != "nodes[1].subscribes[0].when" {
			t.Fatalf("unexpected error path: %+v", res.Errors[0])
		}
	})

	t.Run("declared tag not rejected", func(t *testing.T) {
		t.Parallel()
		res := &ValidationResult{}
		validateSubscriptionDeclaredTags(
			spec.SubscriptionEntry{Node: "a", When: `"declared_tag" in payload.tags`},
			"nodes[1].subscribes[0]",
			TemplateNodeDef{Executor: "h"},
			RegistryHooks{ExecutorDeclaredTags: declaredHook},
			res,
		)
		if len(res.Errors) != 0 {
			t.Fatalf("expected no errors, got %+v", res.Errors)
		}
	})

	t.Run("no ExecutorDeclaredTags hook is a no-op", func(t *testing.T) {
		t.Parallel()
		res := &ValidationResult{}
		validateSubscriptionDeclaredTags(
			spec.SubscriptionEntry{Node: "a", When: `"whatever" in payload.tags`},
			"nodes[1].subscribes[0]",
			TemplateNodeDef{Executor: "h"},
			RegistryHooks{},
			res,
		)
		if len(res.Errors) != 0 {
			t.Fatalf("expected no errors without a declared-tags hook, got %+v", res.Errors)
		}
	})

	t.Run("vocabulary unknown for executor is a no-op", func(t *testing.T) {
		t.Parallel()
		res := &ValidationResult{}
		validateSubscriptionDeclaredTags(
			spec.SubscriptionEntry{Node: "a", When: `"whatever" in payload.tags`},
			"nodes[1].subscribes[0]",
			TemplateNodeDef{Executor: "h"},
			RegistryHooks{ExecutorDeclaredTags: func(string) ([]string, bool) { return nil, false }},
			res,
		)
		if len(res.Errors) != 0 {
			t.Fatalf("expected no errors when the executor's tag vocabulary is unknown, got %+v", res.Errors)
		}
	})
}
