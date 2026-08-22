// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckAttributesSchema_UnifiedSurface(t *testing.T) {
	t.Run("property with source: and no default: accepted", func(t *testing.T) {
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
							"source": "{{params.x}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with default: and no source: accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":    "string",
							"default": "claude-sonnet-4-5",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("property with both source: and default: rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"both": map[string]any{
							"type":    "string",
							"source":  "{{params.x}}",
							"default": "fallback",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"both":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.both")
	})

	t.Run("readOnly property without source/default accepted when executor declares readOnly", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"summary":{"type":"string","readOnly":true}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("template readOnly without executor readOnly rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"summary":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.summary")
	})

	t.Run("property with neither source/default/readOnly rejected when executor schema is visible", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"orphan": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.orphan")
	})

	t.Run("extension property without source/default/readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"zone_codes": map[string]any{
							"type": "array",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("extension property marked readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"notes": map[string]any{
							"type":     "string",
							"readOnly": true,
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("ENUMERATED property still requires source/default/readOnly under additionalProperties:true", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model")
	})

	t.Run("L1 default plus L2 source on same property: L2 source wins (no both-set error)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			ParamsSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"override_cli": map[string]any{"type": "object"},
				},
			},
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"h": {
							"cli": map[string]any{"silence_timeout_ms": 60000},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{
							"type":   "object",
							"source": "{{params.override_cli}}",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"cli":{"type":"object"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 source: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("L1 source plus L2 default on same property: L2 default wins (no both-set error)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"h": {
							"model": "claude-sonnet-4-5",
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":    "string",
							"default": "claude-opus-4-7",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 default: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("permissive executor schema skips readOnly leg", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"freeform": map[string]any{
							"type": "string",
						},
					},
				}},
			}},
		}
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownClaimProducers),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object"}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "permissive executor schema: should accept the sourceless/defaultless property; errors: %+v", res.Errors)
	})
}

func TestIsPermissiveExecutorSchema(t *testing.T) {
	t.Run("nil schema is not permissive", func(t *testing.T) {
		assert.False(t, IsPermissiveExecutorSchema(nil))
	})

	t.Run("empty object is permissive", func(t *testing.T) {
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{}))
	})

	t.Run("type-only object is permissive", func(t *testing.T) {
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{"type": "object"}))
	})

	t.Run("empty properties block is closed (not permissive)", func(t *testing.T) {
		assert.False(t, IsPermissiveExecutorSchema(map[string]any{
			"properties": map[string]any{},
		}))
	})

	t.Run("populated properties block is closed", func(t *testing.T) {
		assert.False(t, IsPermissiveExecutorSchema(map[string]any{
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		}))
	})
}

func TestValidateAttributesSchema_TypeRedeclarationConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":    "integer",
						"default": 42,
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model.type")
}

func TestValidateAttributesSchema_NestedSourceGrammarValidated(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"nested_field": map[string]any{
								"type":   "string",
								"source": "claim.unknown_alias.payload",
							},
						},
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok(), "nested source directive with an unresolvable claim alias must be rejected at registration, not just at dispatch")
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.config.properties.nested_field.source")
}

func TestValidateAttributesSchema_NestedSourceNonStringRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"nested_field": map[string]any{
								"type":   "string",
								"source": []any{"{{params.a}}", "{{params.b}}"},
							},
						},
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.config.properties.nested_field.source")
}

// @decision: expected-attributes-schema-closed
func TestValidateAttributesSchema_UndeclaredPropertyRejectedWithoutAnExplicitClosedFlag(t *testing.T) {
	for name, execSchema := range map[string]string{
		"schema saying nothing about extensions": `{"type":"object","properties":{"known":{"type":"string"}}}`,
		"schema closing extensions explicitly":   `{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			spec := &TemplateSpec{
				Name:    "demo",
				Version: "1.0.0",
				Nodes: []TemplateNodeDef{{
					Type:     "a",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"known":    map[string]any{"type": "string", "default": "x"},
							"misspelt": map[string]any{"type": "string", "default": "hi"},
						},
					}},
				}},
			}
			hooks := RegistryHooks{
				StoreDeclared: storeDeclaredLookup(knownClaimProducers),
				ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) {
					return []byte(execSchema), true
				},
			}
			res := ValidateTemplate(spec, hooks)
			require.False(t, res.Ok(),
				"an executor schema declares every attribute the executor reads, so a node "+
					"attribute the schema does not declare is a misspelling and registration refuses it")
			hasErrorAt(t, res, "nodes[0].attributes.schema.properties.misspelt")
		})
	}
}

func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L2(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"extra_field": map[string]any{
						"type":    "string",
						"default": "hi",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.extra_field")
}

func TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L1(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Defaults: &TemplateDefaults{
			Attributes: &TemplateAttributeDefaults{
				ByExecutor: map[string]map[string]any{
					"h": {
						"extra_field": "hi",
					},
				},
			},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"known": map[string]any{
						"type":    "string",
						"default": "x",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "defaults.attributes.by_executor.extra_field")
}

func TestValidateAttributesSchema_NestedDefaultTypeConflict(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cli": map[string]any{
						"type": "object",
						"default": map[string]any{
							"silence_timeout_ms": "60s",
						},
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"type":"object",
				"properties":{
					"cli":{
						"type":"object",
						"properties":{
							"silence_timeout_ms":{"type":"integer"}
						}
					}
				}
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.defaults")
}

func TestValidateCompositionAgainstExecutor_RequiredInputWithSource(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"system_prompt": map[string]any{
						"type":   "string",
						"source": "{{params.x}}",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{
				"type":"object",
				"properties":{
					"system_prompt":{"type":"string"}
				},
				"required":["system_prompt"]
			}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(),
		"executor required + template source: registration must not fire false-positive `required:`; errors: %+v",
		res.Errors)
}

// @decision: expected-attributes-schema-closed
func TestValidateAttributesSchema_ExplicitlyOpenSchemaAcceptsExtraProperty(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"extra_field": map[string]any{
						"type":    "string",
						"default": "hi",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string","readOnly":true}},"additionalProperties":true}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(),
		"an executor that declares additionalProperties:true produces node-declared outputs, "+
			"so a property it does not enumerate stands; errors: %+v", res.Errors)
}

// @concept: inertness
// @concept: attribute
func TestValidateTemplate_DefaultsSchemaViolationNamesTheConstraintWithoutTheValue(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token": map[string]any{
						"type":    "string",
						"default": "supplied-secret",
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"token":{"type":"string","const":"expected-secret"}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())

	var msg string
	for _, e := range res.Errors {
		if e.Path == "nodes[0].attributes.schema.defaults" {
			msg = e.Msg
		}
	}
	require.NotEmpty(t, msg, "expected a defaults error, got: %+v", res.Errors)
	assert.NotContains(t, msg, "supplied-secret", "the defaults error must not embed the declared value")
	assert.NotContains(t, msg, "expected-secret", "the defaults error must not embed the executor's permitted value")
	assert.Contains(t, msg, "/token", "the defaults error must name the failing path")
	assert.Contains(t, msg, "const", "the defaults error must name the constraint")
}

// @decision: expected-attributes-schema-closed
func TestValidateAttributesSchema_ReadOnlyExtensionsAdmitATemplateNamedOutput(t *testing.T) {
	const execSchema = `{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":{"readOnly":true}}`

	for name, tc := range map[string]struct {
		property map[string]any
		wantOk   bool
	}{
		"output the template marks read-only": {
			property: map[string]any{"type": "string", "readOnly": true},
			wantOk:   true,
		},
		"writable property the executor never reads": {
			property: map[string]any{"type": "string", "default": "hi"},
			wantOk:   false,
		},
		"property with neither a source nor a default": {
			property: map[string]any{"type": "string"},
			wantOk:   false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec := &TemplateSpec{
				Name:    "demo",
				Version: "1.0.0",
				Nodes: []TemplateNodeDef{{
					Type:     "a",
					Executor: "h",
					Attributes: &NodeAttributesDef{Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"known":    map[string]any{"type": "string", "default": "x"},
							"produced": tc.property,
						},
					}},
				}},
			}
			hooks := RegistryHooks{
				StoreDeclared: storeDeclaredLookup(knownClaimProducers),
				ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) {
					return []byte(execSchema), true
				},
			}
			res := ValidateTemplate(spec, hooks)
			if tc.wantOk {
				require.True(t, res.Ok(),
					"an executor that leaves its outputs open admits a property the template marks read-only; errors = %+v",
					res.Errors)
				return
			}
			require.False(t, res.Ok(),
				"an executor that leaves its outputs open still declares every attribute it reads, so an "+
					"undeclared writable property is a misspelling")
			hasErrorAt(t, res, "nodes[0].attributes.schema.properties.produced")
		})
	}
}

// @decision: expected-attributes-schema-closed
func TestExecutorSchemaAllowsExtensions_ReadOnlyExtensionsDoNotOpenTheInputBag(t *testing.T) {
	readOnlyExtensions := map[string]any{"additionalProperties": map[string]any{"readOnly": true}}
	require.False(t, executorSchemaAllowsExtensions(readOnlyExtensions),
		"a schema whose extensions must be read-only accepts no undeclared input")
	require.True(t, executorSchemaAllowsReadOnlyExtensions(readOnlyExtensions))

	typedExtensions := map[string]any{"additionalProperties": map[string]any{"type": "string"}}
	require.True(t, executorSchemaAllowsExtensions(typedExtensions),
		"a schema that admits an undeclared string property has an open input bag")
	require.False(t, executorSchemaAllowsReadOnlyExtensions(typedExtensions))

	closed := map[string]any{"additionalProperties": false}
	require.False(t, executorSchemaAllowsExtensions(closed))
	require.False(t, executorSchemaAllowsReadOnlyExtensions(closed))
}
