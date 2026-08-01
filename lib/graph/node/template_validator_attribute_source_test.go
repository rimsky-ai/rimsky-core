// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateParamsSchema_Error_MalformedSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:         "demo",
		Version:      "1.0.0",
		ParamsSchema: map[string]any{"type": "not-a-real-json-schema-type"},
		Nodes:        []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "params_schema")
}

func TestValidateParamsSchema_Ok_ValidSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"domain": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestCheckAttributeSource_Error_UndeclaredParamsKeyWhenSchemaPresent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"properties": map[string]any{"domain": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "source": "{{params.unknown}}"},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "undeclared params key") {
			found = true
		}
	}
	require.True(t, found, "errors: %+v", res.Errors)
}

func TestCheckAttributeSource_Ok_UndeclaredParamsKeyExemptWithFallback(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"properties": map[string]any{"domain": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "source": `{{params.notes | "none"}}`},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "a fallback literal covers the missing-key case; schema declaration must not be required; errors: %+v", res.Errors)
}

func TestCheckAttributeSource_Ok_UndeclaredParamsKeyExemptWithLenientMarker(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"properties": map[string]any{"domain": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "source": "{{params.notes?}}"},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "the `?` lenient marker covers the missing-key case; schema declaration must not be required; errors: %+v", res.Errors)
}

func TestCheckAttributeSource_Ok_ParamsKeyUncheckedWhenNoSchemaDeclared(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "source": "{{params.anything}}"},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "no params_schema declared: source params.* refs must not be checked; errors: %+v", res.Errors)
}

func TestValidateClaimProducers_Error_UndeclaredParamsKeyInSelectorWhenSchemaPresent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"properties": map[string]any{"tenant": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Selector: "{{params.unknown}}", Intent: "r"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "undeclared params key") {
			found = true
		}
	}
	require.True(t, found, "errors: %+v", res.Errors)
}

func TestValidateLocks_Error_UndeclaredParamsKeyInLockNameWhenSchemaPresent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		ParamsSchema: map[string]any{
			"properties": map[string]any{"tenant": map[string]any{"type": "string"}},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "{{params.unknown}}"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "undeclared params key") {
			found = true
		}
	}
	require.True(t, found, "errors: %+v", res.Errors)
}

func TestCheckAttributeSource_BareFormPulls(t *testing.T) {
	t.Run("bare nodes attribute pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{
					Type:     "stage",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"row": map[string]any{
								"type":    "object",
								"default": map[string]any{},
							},
						},
					}},
				},
				{
					Type:     "verify",
					Executor: "h",
					Subscribes: []SubscriptionEntry{
						{Node: "stage", Type: "attribute/*", ForceUpstreamRefresh: BoolPtr(false)},
					},
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"upstream": map[string]any{
								"type":   "object",
								"source": "{{nodes.stage.attribute}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare claim payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "topics", Selector: "@q", Intent: "rw", Alias: "queue"},
				},
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"whole_payload": map[string]any{
							"type":   "object",
							"source": "{{claim.queue.payload}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare nodes event pull rejected (retired)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "emit", Executor: "h"},
				{
					Type:     "receive",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"evt": map[string]any{
								"type":   "object",
								"source": "{{nodes.emit.event.progress}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.False(t, res.Ok(), "expected validator to reject the retired event source-kind")
	})

	t.Run("empty trailing dot still rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "stage", Executor: "h"},
				{
					Type:     "verify",
					Executor: "h",
					Subscribes: []SubscriptionEntry{
						{Node: "stage", Type: "attribute/*", ForceUpstreamRefresh: BoolPtr(false)},
					},
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"bad": map[string]any{
								"type":   "object",
								"source": "{{nodes.stage.attribute.}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		if !findErrorContains(res.Errors, "empty trailing segment") {
			t.Fatalf("expected diagnostic naming the empty trailing segment, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
		}
	})
}

func TestCheckAttributeSource_RejectsDeepEmptySegments(t *testing.T) {
	specFor := func(source string, extra func(*TemplateSpec)) *TemplateSpec {
		s := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{
					Type:     "a",
					Executor: "h",
					ClaimProducers: []NodeClaimProducerRef{
						{Name: "content", Selector: "@x", Intent: "r", Alias: "queue"},
					},
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"bad": map[string]any{"type": "string", "source": source},
						},
					}},
				},
			},
		}
		if extra != nil {
			extra(s)
		}
		return s
	}

	t.Run("claim payload deep trailing segment rejected", func(t *testing.T) {
		spec := specFor("{{claim.queue.payload.x.}}", nil)
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.bad")
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e.Msg, "empty trailing segment") {
				found = true
			}
		}
		require.True(t, found, "errors: %+v", res.Errors)
	})

	t.Run("nodes attribute deep trailing segment rejected", func(t *testing.T) {
		spec := specFor("{{nodes.other.attribute.x.}}", func(s *TemplateSpec) {
			s.Nodes = append(s.Nodes, TemplateNodeDef{Type: "other", Executor: "h"})
		})
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e.Msg, "empty trailing segment") {
				found = true
			}
		}
		require.True(t, found, "errors: %+v", res.Errors)
	})

	t.Run("messages deep trailing segment rejected", func(t *testing.T) {
		spec := specFor("{{messages.t/type.f.g.}}", func(s *TemplateSpec) {
			s.Messages = []MessageSchema{
				{Type: "t/type", BodySchema: []byte(`{"type":"object","properties":{"f":{"type":"object"}}}`)},
			}
		})
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e.Msg, "empty trailing segment") {
				found = true
			}
		}
		require.True(t, found, "errors: %+v", res.Errors)
	})

	t.Run("params nested empty segment rejected", func(t *testing.T) {
		spec := specFor("{{params.key.}}", nil)
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e.Msg, "empty trailing segment") {
				found = true
			}
		}
		require.True(t, found, "errors: %+v", res.Errors)
	})
}

func TestValidator_FallbackOperator_Valid(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "stage", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"out": map[string]any{"type": "string", "default": ""},
					},
				}},
			},
			{
				Type:     "verify",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "stage", Type: "attribute/out/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{nodes.stage.attribute.out | "default"}}`,
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
	}
}

func TestValidator_FallbackOperator_ChainsRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "default": ""},
					},
				}},
			},
			{Type: "b", Executor: "h"},
			{
				Type:     "c",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{nodes.a.attribute.x | nodes.b.attribute.y | "default"}}`,
						},
					},
				}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "expected error for multi-pipe chain")
	if !findErrorContains(res.Errors, "multi-pipe fallback chain") {
		t.Fatalf("expected diagnostic naming the multi-pipe fallback chain, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
	}
}

func TestCheckAttributeSource_RelaxedGrammar(t *testing.T) {
	t.Run("literal text + one directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": "Generate config for {{params.domain}}.",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("multiple directives separated by text accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":   "string",
							"source": "Hello {{params.x}}, world {{params.y}}.",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on a single directive accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{
					Type: "verify", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"warnings_block": map[string]any{
								"type":    "string",
								"default": "",
							},
						},
					}},
				},
				{
					Type: "generate", Executor: "h",
					Subscribes: []SubscriptionEntry{
						{Node: "verify", Type: "attribute/warnings_block/changed", ForceUpstreamRefresh: BoolPtr(false)},
					},
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"prompt": map[string]any{
								"type":   "string",
								"source": "{{nodes.verify.attribute.warnings_block?}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? marker on directive in embedded source accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{
					Type: "verify", Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"warnings_block": map[string]any{
								"type":    "string",
								"default": "",
							},
						},
					}},
				},
				{
					Type: "generate", Executor: "h",
					Subscribes: []SubscriptionEntry{
						{Node: "verify", Type: "attribute/warnings_block/changed", ForceUpstreamRefresh: BoolPtr(false)},
					},
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"prompt": map[string]any{
								"type":   "string",
								"source": "warnings: {{nodes.verify.attribute.warnings_block?}}",
							},
						},
					}},
				},
			},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("? + | on the same directive rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type: "a", Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"v": map[string]any{
							"type":   "string",
							"source": `{{params.x? | "y"}}`,
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.v.source")
	})
}
