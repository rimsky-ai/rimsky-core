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

// TestExpectedAttributesSchemaResolver_BehavioralValidation drives the
// observability concept's `userdata_schema` (= `expected_attributes_schema`)
// surface end-to-end through BOTH enforcement points the concept claims:
// template registration AND dispatch. The schema bytes originate from the
// real discovery cache, are surfaced by the real resolver
// (NewExpectedAttributesSchemaResolver), and are then fed into the real
// validators (node.ValidateTemplate at registration, attributes.Validate at
// dispatch). A conforming payload passes; a schema-violating payload is
// rejected with a clear, attributable error at each point.
//
// This is the behavioral counterpart to the proto/shape smoke coverage of
// the handshake: it confirms the advertised schema is actually consulted at
// the validation gates, not merely parsed.
func TestExpectedAttributesSchemaResolver_BehavioralValidation(t *testing.T) {
	// @constraint: the executor advertises a closed schema — `model` must be a string,
	// and no other top-level properties are admitted. The closed-schema and
	// type-authority assertions below depend on this shape.
	const executorName = "agent"
	advertisedSchema := []byte(`{
		"type": "object",
		"properties": {
			"model": {"type": "string"}
		},
		"additionalProperties": false
	}`)

	// @constraint: populate the real discovery cache as the startup handshake would,
	// then build the real resolver over it.
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

	// @constraint: sanity: the resolver surfaces the advertised bytes for the known
	// executor and reports ok=false for an unknown one. The validators
	// below depend on this lookup behaving as the dispatch path expects.
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

	// @deliberate: registration-time enforcement —
	// The registration validator consults the advertised schema via the
	// ExecutorExpectedAttributesSchema hook (the same shape the control
	// API wires from the discovery cache). The executor is authoritative
	// on types, so an L2 redeclaration that conflicts must be rejected.
	hooks := node.RegistryHooks{
		ExecutorDeclared: func(name string) bool { return name == executorName },
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return resolver(name)
		},
	}

	validSpec := func(modelType string) *node.TemplateSpec {
		return &node.TemplateSpec{
			Name:                "demo",
			Version:             "1.0.0",
			FrameResolutionMode: node.FrameResolutionSerialQueue,
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
		// @constraint: L2 redeclares `model` as integer; the advertised schema says
		// string. The executor is authoritative — registration rejects.
		res := node.ValidateTemplate(validSpec("integer"), hooks)
		if res.Ok() {
			t.Fatal("type-conflicting template passed registration; advertised schema was not enforced")
		}
		if !anyErrorContains(res.Errors, "executor is authoritative on types") {
			t.Fatalf("registration error did not name the type-authority violation: %+v", res.Errors)
		}
	})

	t.Run("registration: undeclared property rejected under closed schema", func(t *testing.T) {
		// @constraint: the advertised schema is closed (additionalProperties: false);
		// an L2 property the executor does not enumerate is rejected.
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

	// @deliberate: dispatch-time enforcement —
	// At dispatch the merged effective schema is validated against the
	// resolved attribute bag (post-merge/post-substitution). The resolved
	// bytes are the same advertised schema; a bag whose `model` is the
	// wrong type must be rejected.
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

// anyErrorContains reports whether any validation error's message contains
// sub. Keeps the assertion legible without a per-call closure.
func anyErrorContains(errs []node.ValidationError, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Msg, sub) {
			return true
		}
	}
	return false
}

// mustParseSchema decodes advertised schema bytes into the map shape the
// dispatch validator consumes, mirroring runtime's effective-schema build
// (runtime/runner_dispatch.go::computeEffectiveAttributeSchema json.Unmarshals
// the resolver bytes the same way).
func mustParseSchema(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("parse advertised schema bytes: %v", err)
	}
	return schema
}
