// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"encoding/json"
	"strings"
	"testing"

	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestExpectedAttributesSchemaResolver_BehavioralValidation(t *testing.T) {
	const executorName = "agent"
	advertisedSchema := []byte(`{
		"type": "object",
		"properties": {
			"model": {"type": "string"}
		},
		"additionalProperties": false
	}`)

	disc := NewDiscovery(nil)
	disc.SetExecutor(PeerEntry{
		Name:         executorName,
		Reachability: ReachabilityReachable,
		Capabilities: &ObservabilityCapabilities{
			ExpectedAttributesSchema: advertisedSchema,
		},
	})
	resolver := NewExpectedAttributesSchemaResolver(disc)
	if resolver == nil {
		t.Fatal("NewExpectedAttributesSchemaResolver returned nil for a non-nil discovery")
	}

	gotSchema, ok := resolver(executorName)
	if !ok {
		t.Fatalf("resolver(%q): ok=false, want true (executor is in the cache with a schema)", executorName)
	}
	if len(gotSchema) == 0 {
		t.Fatal("resolver returned empty schema bytes for a cached executor advertising a schema")
	}
	if _, ok := resolver("not-registered"); ok {
		t.Fatal("resolver returned ok=true for an executor absent from the cache")
	}

	hooks := node.RegistryHooks{
		ExecutorDeclared: func(name string) bool { return name == executorName },
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return resolver(name)
		},
	}

	validSpec := func(modelType string) *node.TemplateSpec {
		return &node.TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []node.TemplateNodeDef{{
				Type:     "a",
				Executor: executorName,
				Attributes: &node.NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":    modelType,
							"default": "claude-sonnet-4-5",
						},
					},
				}},
			}},
		}
	}

	t.Run("registration: conforming template passes", func(t *testing.T) {
		res := node.ValidateTemplate(validSpec("string"), hooks)
		if !res.Ok() {
			t.Fatalf("conforming template rejected at registration: %+v", res.Errors)
		}
	})

	t.Run("registration: type-conflicting template rejected", func(t *testing.T) {
		res := node.ValidateTemplate(validSpec("integer"), hooks)
		if res.Ok() {
			t.Fatal("type-conflicting template passed registration; advertised schema was not enforced")
		}
		if !anyErrorContains(res.Errors, "executor is authoritative on types") {
			t.Fatalf("registration error did not name the type-authority violation: %+v", res.Errors)
		}
	})

	t.Run("registration: undeclared property rejected under closed schema", func(t *testing.T) {
		spec := validSpec("string")
		props := spec.Nodes[0].Attributes.Schema["properties"].(map[string]any)
		props["unknown_field"] = map[string]any{"type": "string", "default": "x"}
		res := node.ValidateTemplate(spec, hooks)
		if res.Ok() {
			t.Fatal("undeclared property passed registration under a closed advertised schema")
		}
		if !anyErrorContains(res.Errors, "additionalProperties: false") {
			t.Fatalf("registration error did not name the closed-schema violation: %+v", res.Errors)
		}
	})

	dispatchSchemaBytes, ok := resolver(executorName)
	if !ok {
		t.Fatal("resolver(executorName) ok=false at dispatch stage")
	}
	dispatchSchema := mustParseSchema(t, dispatchSchemaBytes)

	t.Run("dispatch: conforming bag passes", func(t *testing.T) {
		bag := map[string]any{"model": "claude-sonnet-4-5"}
		if err := attributes.Validate(dispatchSchema, bag, attributes.PhaseDispatch); err != nil {
			t.Fatalf("conforming dispatch bag rejected against advertised schema: %v", err)
		}
	})

	t.Run("dispatch: type-violating bag rejected", func(t *testing.T) {
		bag := map[string]any{"model": 42}
		err := attributes.Validate(dispatchSchema, bag, attributes.PhaseDispatch)
		if err == nil {
			t.Fatal("type-violating dispatch bag passed; advertised schema was not enforced at dispatch")
		}
		if !strings.Contains(err.Error(), "model") {
			t.Fatalf("dispatch error did not locate the offending `model` property: %v", err)
		}
	})

	t.Run("dispatch: undeclared property rejected under closed schema", func(t *testing.T) {
		bag := map[string]any{"model": "ok", "unknown_field": "nope"}
		if err := attributes.Validate(dispatchSchema, bag, attributes.PhaseDispatch); err == nil {
			t.Fatal("undeclared property passed dispatch under a closed advertised schema")
		}
	})
}

func anyErrorContains(errs []node.ValidationError, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Msg, sub) {
			return true
		}
	}
	return false
}

func mustParseSchema(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("parse advertised schema bytes: %v", err)
	}
	return schema
}
