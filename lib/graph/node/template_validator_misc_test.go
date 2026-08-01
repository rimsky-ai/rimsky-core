// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCascadeMode_Ok(t *testing.T) {
	for _, mode := range []string{"", "most-recent", "sequenced", "idempotent-queue", "idempotent-settled"} {
		t.Run(mode, func(t *testing.T) {
			spec := &TemplateSpec{
				Name:    "demo",
				Version: "1.0.0",
				Nodes: []TemplateNodeDef{{
					Type: "a", Executor: "h", CascadeMode: mode,
				}},
			}
			res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
			assert.True(t, res.Ok(), "errors: %+v", res.Errors)
		})
	}
}

func TestValidateCascadeMode_Unknown(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", CascadeMode: "bogus",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].cascade_mode")
}

func TestTemplateValidator_DefaultsByExecutor(t *testing.T) {
	t.Run("unknown executor name is rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"unknown-executor": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, `defaults.attributes.by_executor["unknown-executor"]`)
	})

	t.Run("matching executor name is accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {"cli": map[string]any{"model": "claude-opus"}},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("fragment values are not inspected (only routing keys)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {
							"garbage_key": []any{"a", 1, true, nil, map[string]any{"k": "v"}},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

func TestTemplateValidator_Tags(t *testing.T) {
	t.Run("valid params reference accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			ParamsSchema: map[string]any{
				"properties": map[string]any{
					"domain": map[string]any{"type": "string"},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"setup", "domain:{{params.domain}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("unknown params key rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			ParamsSchema: map[string]any{
				"properties": map[string]any{
					"domain": map[string]any{"type": "string"},
				},
			},
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"{{params.unknown}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("unsupported kind in tag rejected", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"{{claim.staging.address}}"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].tags[0]")
	})

	t.Run("plain string tag accepted (no directives)", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Tags:     []string{"setup"},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("subscription When payload.tags literal not in sender's declared_tags rejects registration", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "a", Executor: "h"},
				{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: `"undeclared_tag" in payload.tags`, ForceUpstreamRefresh: BoolPtr(false)},
				}},
			},
		}
		hooks := RegistryHooks{
			ExecutorDeclaredTags: func(string) ([]string, bool) { return []string{"declared_tag"}, true },
		}
		res := ValidateTemplate(spec, hooks)
		found := false
		for _, e := range res.Errors {
			if e.Path == "nodes[1].subscribes[0].when" && strings.Contains(e.Msg, "undeclared_tag") {
				found = true
			}
		}
		require.True(t, found, "expected an error naming the undeclared tag at nodes[1].subscribes[0].when; errors: %+v", res.Errors)
		require.False(t, res.Ok(), "registration must reject an undeclared subscription tag, not just warn")
	})

	t.Run("subscription When payload.tags literal in sender's declared_tags does not reject", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "a", Executor: "h"},
				{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: `"declared_tag" in payload.tags`, ForceUpstreamRefresh: BoolPtr(false)},
				}},
			},
		}
		hooks := RegistryHooks{
			ExecutorDeclaredTags: func(string) ([]string, bool) { return []string{"declared_tag"}, true },
		}
		res := ValidateTemplate(spec, hooks)
		for _, e := range res.Errors {
			if e.Path == "nodes[1].subscribes[0].when" {
				t.Fatalf("unexpected undeclared-tag error for a declared literal: %+v", e)
			}
		}
	})
}

func TestValidateTemplate_ReferenceValidationIsUnconditionallyStrict(t *testing.T) {
	const notProvisioned = "ghost-executor"
	const provisionedConstrained = "constrained-executor"
	const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool {
			return name == provisionedConstrained
		},
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			if name == provisionedConstrained {
				return []byte(constrainedSchema), true
			}
			return nil, false
		},
	}

	notProvisionedNode := func() TemplateNodeDef {
		return TemplateNodeDef{Type: "ghost", Executor: notProvisioned}
	}

	invalidProvisionedNode := func() TemplateNodeDef {
		return TemplateNodeDef{
			Type:     "constrained",
			Executor: provisionedConstrained,
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":    "integer",
						"default": -1,
					},
				},
			}},
		}
	}

	specWith := func(nodes ...TemplateNodeDef) *TemplateSpec {
		return &TemplateSpec{
			Name:    "ref-mode-demo",
			Version: "1",
			Nodes:   nodes,
		}
	}

	t.Run("not-provisioned executor ref hard-fails", func(t *testing.T) {
		spec := specWith(notProvisionedNode())
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok(),
			"a reference to a not-yet-provisioned executor must be rejected; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].executor")
	})

	t.Run("provisioned but schema-invalid ref hard-fails", func(t *testing.T) {
		spec := specWith(invalidProvisionedNode())
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok(),
			"a genuinely-invalid provisioned ref must always be rejected; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].attributes")
	})

	t.Run("executor's expected_attributes_schema not visible at registration hard-fails", func(t *testing.T) {
		spec := specWith(TemplateNodeDef{
			Type:     "unseen",
			Executor: notProvisioned,
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
			}},
		})
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes")
	})
}

func TestValidateDispatchDeadlines_SubSecondPositiveRejected(t *testing.T) {
	var res ValidationResult
	validateDispatchDeadlines(TemplateNodeDef{MaxQuietPeriod: "500ms"}, "nodes.a", &res)
	hasErrorAt(t, res, "nodes.a.max_quiet_period")
}

func TestValidateDispatchDeadlines_ZeroAccepted(t *testing.T) {
	var res ValidationResult
	validateDispatchDeadlines(TemplateNodeDef{MaxRuntime: "0s"}, "nodes.a", &res)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateDispatchDeadlines_WholeSecondAccepted(t *testing.T) {
	var res ValidationResult
	validateDispatchDeadlines(TemplateNodeDef{SyncRPCDeadline: "1s"}, "nodes.a", &res)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateDispatchDeadlines_NegativeStillRejected(t *testing.T) {
	var res ValidationResult
	validateDispatchDeadlines(TemplateNodeDef{MaxRuntime: "-1s"}, "nodes.a", &res)
	hasErrorAt(t, res, "nodes.a.max_runtime")
}
