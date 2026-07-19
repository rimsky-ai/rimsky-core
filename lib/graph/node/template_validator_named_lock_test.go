// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTemplate_NamedLockDeclared_Missing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Locks:    []NodeLockRef{{Name: "db-migration"}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:     storeDeclaredLookup(knownClaimProducers),
		NamedLockDeclared: func(name string) bool { return false },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok(), "undeclared named lock must be rejected")
	hasErrorAt(t, res, "nodes[0].locks[0].name")
	var msg string
	for _, e := range res.Errors {
		if e.Path == "nodes[0].locks[0].name" {
			msg = e.Msg
		}
	}
	require.Contains(t, msg, "db-migration")
	require.Contains(t, msg, "named_locks")
}

func TestValidateTemplate_NamedLockDeclared_OK(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Locks:    []NodeLockRef{{Name: "db-migration"}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared:     storeDeclaredLookup(knownClaimProducers),
		NamedLockDeclared: func(name string) bool { return name == "db-migration" },
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestSubstitutionCoverage_LockNameRefUncovered(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				Locks:    []NodeLockRef{{Name: "lock-{{nodes.foo.attribute.bar}}"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "validator must reject the uncovered ref embedded in a lock name")
	entry := findCoverageEntry(t, res, "rcv", "nodes.foo.attribute.bar")
	assertSuggestedEntryShape(t, entry, "foo", "attribute/bar/changed")
}

func TestSubstitutionCoverage_LockNameRefCoveredBySubscription(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "foo",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"bar": map[string]any{"type": "string"},
					},
				}},
			},
			{
				Type:     "rcv",
				Executor: "h",
				Subscribes: []SubscriptionEntry{
					{
						Node:                 "foo",
						Type:                 "attribute/bar/changed",
						ForceUpstreamRefresh: BoolPtr(false),
					},
				},
				Locks: []NodeLockRef{{Name: "lock-{{nodes.foo.attribute.bar}}"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestSubstitutionCoverage_ClaimProducerSelectorRefUncovered(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "foo", Executor: "h"},
			{
				Type:     "rcv",
				Executor: "h",
				ClaimProducers: []NodeClaimProducerRef{
					{Name: "content", Selector: "{{nodes.foo.attribute.bar}}", Intent: "rw"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
	})
	require.False(t, res.Ok(), "validator must reject the uncovered ref embedded in a claim-producer selector")
	entry := findCoverageEntry(t, res, "rcv", "nodes.foo.attribute.bar")
	assertSuggestedEntryShape(t, entry, "foo", "attribute/bar/changed")
}
