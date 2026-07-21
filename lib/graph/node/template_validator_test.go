// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var knownClaimProducers = map[string]struct{}{
	"content": {},
	"shared":  {},
	"topics":  {},
	"inbound": {},
}

func storeDeclaredLookup(known map[string]struct{}) func(string) bool {
	return func(name string) bool {
		_, ok := known[name]
		return ok
	}
}

func hasErrorAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, prefix) {
			return
		}
	}
	t.Fatalf("expected error with path prefix %q, got %+v", prefix, res.Errors)
}

func findWarningContains(warns []ValidationWarning, substr string) bool {
	for _, w := range warns {
		if strings.Contains(w.Msg, substr) {
			return true
		}
	}
	return false
}

func TestValidateTemplate_FlattensGraphsIntoNodes_DocumentedSideEffect(t *testing.T) {
	newSpec := func() *TemplateSpec {
		return &TemplateSpec{
			Name:    "tmpl",
			Version: "1",
			Graphs: []GraphSpec{
				{Name: MainGraphName, Nodes: []TemplateNodeDef{{Type: "alpha", Delegate: "sub"}}},
				{
					Name:  "sub",
					Entry: "b",
					Exit:  "c",
					Nodes: []TemplateNodeDef{
						{Type: "b"},
						{Type: "c", Subscribes: []SubscriptionEntry{{Node: "b", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
					},
				},
			},
		}
	}

	specA := newSpec()
	resA := ValidateTemplate(specA, RegistryHooks{})
	require.True(t, resA.Ok(), "errors: %+v", resA.Errors)
	require.NotEmpty(t, specA.Nodes,
		"ValidateTemplate's canonicalizeGraphs step flattens graphs: into spec.Nodes as a documented, tested side effect — "+
			"callers that hash the spec (e.g. controlapi template registration) do so AFTER this call and rely on the flattened form")

	specB := newSpec()
	resB := ValidateTemplate(specB, RegistryHooks{})
	require.True(t, resB.Ok(), "errors: %+v", resB.Errors)

	require.Equal(t, specA.Nodes, specB.Nodes,
		"flattening must be deterministic for structurally identical graphs: input, since content hashing downstream of "+
			"ValidateTemplate depends on the flattened spec being stable across equivalent inputs")
}

func TestValidateTemplate_Ok_MinimalExecutorNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateClaimProducers_Ok_RegionClaimWithIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Selector: "/data/x", Intent: "rw"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateClaimProducers_Error_MissingIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:           "a",
			Executor:       "h",
			ClaimProducers: []NodeClaimProducerRef{{Name: "content", Selector: "/data/x"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].intent")
}

func TestValidateClaimProducers_Error_DuplicateAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Selector: "/x", Intent: "r", Alias: "shared"},
				{Name: "shared", Selector: "/y", Intent: "r", Alias: "shared"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[1].alias")
}

func TestValidateSubstitutionRef_ClaimProducerSelectorOriginNotMisreportedAsAttributesSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "content", Intent: "r", Selector: "{{nodes.ghost.attribute.x}}"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].selector (substitution ref)")
}

func TestValidateClaimProducers_Error_UnknownStoreKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:           "a",
			Executor:       "h",
			ClaimProducers: []NodeClaimProducerRef{{Name: "ghost", Selector: "/x", Intent: "r"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].name")
}

func TestValidateTemplate_ExecutorDeclared_OK(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool { return name == "handler.a" },
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_ExecutorDeclared_Missing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "claude-agent",
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:    storeDeclaredLookup(knownClaimProducers),
		ExecutorDeclared: func(name string) bool { return false },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].executor")
	var msg string
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].executor") {
			msg = e.Msg
			break
		}
	}
	require.Contains(t, msg, "claude-agent")
}

func TestValidateTemplate_ClaimScopeSpelling(t *testing.T) {
	makeSpec := func(directive string) *TemplateSpec {
		return &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "worker",
				Executor: "handler.worker",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Alias: "a", Intent: "rw", Selector: "/scope-A"},
				},
				Attributes: &NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"region": map[string]any{
								"type":   "string",
								"source": directive,
							},
						},
					},
				},
			}},
		}
	}

	resCanonical := ValidateTemplate(
		makeSpec("{{claim.a.claim_scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)},
	)
	assert.True(t, resCanonical.Ok(),
		"canonical {{claim.a.claim_scope}} must validate; errors: %+v", resCanonical.Errors)

	resLegacy := ValidateTemplate(
		makeSpec("{{claim.a.scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)},
	)
	require.False(t, resLegacy.Ok(),
		"legacy {{claim.a.scope}} must be rejected at registration")
	hasErrorAt(t, resLegacy, "nodes[0].attributes.schema.properties.region.source")

	var legacyMsg string
	for _, e := range resLegacy.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].attributes.schema.properties.region.source") {
			legacyMsg = e.Msg
			break
		}
	}
	require.Contains(t, legacyMsg, "claim_scope",
		"the legacy-spelling rejection must name the canonical claim_scope segment; got %q", legacyMsg)
}

func TestValidator_WarnsOnMissingAcquireUnavailablePolicy(t *testing.T) {
	t.Run("stores_no_policy_warns", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: func(name string) bool { return name == "q" },
		})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		require.NotEmpty(t, res.Warnings, "expected a warning about missing acquire/unavailable policy")
		require.True(t, findWarningContains(res.Warnings, "acquire/unavailable"), "warnings: %+v", res.Warnings)
	})

	t.Run("stores_with_policy_no_warning", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
				ErrorTypes: map[string]ErrorTypePolicy{
					"acquire/unavailable": {
						Action: "give_up",
					},
				},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: func(name string) bool { return name == "q" },
		})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		for _, w := range res.Warnings {
			require.NotContains(t, w.Msg, "acquire/unavailable",
				"unexpected acquire/unavailable warning when policy declared")
		}
	})

	t.Run("no_stores_no_warning", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{Type: "a"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		for _, w := range res.Warnings {
			require.NotContains(t, w.Msg, "acquire/unavailable",
				"unexpected acquire/unavailable warning on node without claim producers")
		}
	})
}
