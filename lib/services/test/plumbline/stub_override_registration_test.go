// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: expected-attributes-schema-closed
// @concept: conformance

package plumbline

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
	httpnode "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
	verifiershapechecks "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks"
)

func TestATemplateMaySetTheStubOverrideOnEveryBundledExecutor(t *testing.T) {
	cases := []struct {
		name   string
		schema []byte
		inputs map[string]any
	}{
		{
			name:   verifierhttp.ExecutorName,
			schema: verifierhttp.SchemaBytes(),
			inputs: map[string]any{
				"url": map[string]any{"type": "string", "default": "https://upstream.invalid/probe"},
			},
		},
		{
			name:   verifiershapechecks.ExecutorName,
			schema: verifiershapechecks.SchemaBytes(),
			inputs: map[string]any{
				"checks": map[string]any{"type": "array", "default": []any{map[string]any{"kind": "row_count"}}},
				"rows":   map[string]any{"type": "array", "default": []any{}},
			},
		},
		{
			name:   httpnode.ExecutorName,
			schema: httpnode.SchemaBytes(),
			inputs: map[string]any{},
		},
		{
			name:   claudeagent.ExecutorName,
			schema: claudeagent.SchemaBytes(),
			inputs: map[string]any{
				"system_prompt": map[string]any{"type": "string", "default": "probe"},
				"user_prompt":   map[string]any{"type": "string", "default": "probe"},
				"cli":           map[string]any{"type": "object", "default": map[string]any{}},
			},
		},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.name] = true
	}
	for _, e := range bundledExecutors() {
		if !covered[e.name] {
			t.Fatalf("%s ships as a bundled executor and this test drives no case for it. The stub-mode override belongs on every bundled executor.", e.name)
		}
	}

	for _, tc := range cases {
		schema := tc.schema
		hooks := node.RegistryHooks{
			ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) { return schema, true },
		}
		properties := map[string]any{
			"stub_response": map[string]any{"type": "object", "default": map[string]any{"answer": float64(42)}},
			"stub_tags":     map[string]any{"type": "array", "default": []any{"probe"}},
		}
		for name, prop := range tc.inputs {
			properties[name] = prop
		}
		spec := &node.TemplateSpec{
			Name:    "stub-override-probe",
			Version: "1",
			Nodes: []node.TemplateNodeDef{{
				Type:     "work",
				Executor: tc.name,
				Attributes: &node.NodeAttributesDef{Schema: map[string]any{
					"type":       "object",
					"properties": properties,
				}},
			}},
		}
		res := node.ValidateTemplate(spec, hooks)
		if !res.Ok() {
			t.Errorf("%s reads the stub-mode override, so a template may declare it; errors = %+v",
				tc.name, res.Errors)
		}

		silent := map[string]any{}
		for name, prop := range tc.inputs {
			silent[name] = prop
		}
		quiet := &node.TemplateSpec{
			Name:    "no-stub-probe",
			Version: "1",
			Nodes: []node.TemplateNodeDef{{
				Type:     "work",
				Executor: tc.name,
				Attributes: &node.NodeAttributesDef{Schema: map[string]any{
					"type":       "object",
					"properties": silent,
				}},
			}},
		}
		if res := node.ValidateTemplate(quiet, hooks); !res.Ok() {
			t.Errorf("%s: a template that does not name the stub-mode override still registers; errors = %+v",
				tc.name, res.Errors)
		}
	}
	t.Logf("checked all %d bundled executors under lib/services/executors", len(cases))
}
