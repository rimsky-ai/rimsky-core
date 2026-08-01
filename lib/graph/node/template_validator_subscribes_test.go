// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTemplate_Error_SubscribeToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ghost", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

func TestValidateTemplate_Ok_SubscribeToMessageTypeShapedNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_SubscribeToUndeclaredMessageType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

func TestValidateSubscribes_Ok_MessageVirtualNodeWhenBodyField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type:       "ping/recheck",
			BodySchema: []byte(`{"type":"object","properties":{"pong_status":{"type":"string"}}}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pong_status == "ok"`, ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_Error_MessageVirtualNodeUnreachableType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type: "ping/recheck",
		}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if e.Path == "nodes[0].subscribes[0].type" && strings.Contains(e.Msg, "subscription_message_type_unreachable") {
			found = true
		}
	}
	require.True(t, found, "expected an unreachable-type rejection for a message-type subscription; errors: %+v", res.Errors)
}

func TestValidateSubscribes_Ok_MessageVirtualNodeReachableTypes(t *testing.T) {
	for _, typ := range []string{"terminal/success", "terminal/*"} {
		t.Run(typ, func(t *testing.T) {
			spec := &TemplateSpec{
				Name:    "demo",
				Version: "1.0.0",
				Messages: []MessageSchema{{
					Type: "ping/recheck",
				}},
				Nodes: []TemplateNodeDef{{
					Type:     "a",
					Executor: "handler.a",
					Subscribes: []SubscriptionEntry{
						{Node: "ping/recheck", Type: typ, ForceUpstreamRefresh: BoolPtr(false)},
					},
				}},
			}
			res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
			for _, e := range res.Errors {
				if e.Path == "nodes[0].subscribes[0].type" {
					t.Fatalf("unexpected type rejection for reachable message-type subscription %q: %+v", typ, e)
				}
			}
		})
	}
}

func TestValidateSubscribes_Error_MessageVirtualNodeWhenUnknownBodyField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type:       "ping/recheck",
			BodySchema: []byte(`{"type":"object","properties":{"pong_status":{"type":"string"}}}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pongStatus == "ok"`, ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

func TestValidateSubscribes_Error_MessageVirtualNodeWhenEmptyBodySchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.anything == "ok"`, ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

func TestValidateSubscribes_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_SelfOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "drainer", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "drainer", Type: "terminal/success", When: "payload.changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_SelfBareOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateSubscribes_RejectsBareEvent(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "event", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsUnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "garbage/foo", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsTransientType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "transient/retry/*", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	require.True(t, findErrorContains(res.Errors, "transient/retry/*"),
		"expected validation error naming the rejected transient subscription type, got %+v", res.Errors)
}

func TestValidateSubscribes_RejectsMalformedCEL(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: "payload.foo &&&", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

func TestValidateSubscribes_RejectsMissingForceUpstreamRefresh(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "missing force_upstream_refresh must be rejected")
	found := false
	for _, e := range res.Errors {
		if strings.HasSuffix(e.Path, ".force_upstream_refresh") && strings.Contains(e.Msg, "required") {
			found = true
			break
		}
	}
	require.True(t, found, "expected an error whose path ends in .force_upstream_refresh with a required message; got %+v", res.Errors)
}

func TestValidateSubscribes_RejectsConflictingFlagsOnSameKey(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(true)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "conflicting cascade-shape flag values on the same subscription key must be rejected")
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "conflicting cascade-shape flags") &&
			strings.HasSuffix(e.Path, ".subscribes[1]") {
			found = true
			break
		}
	}
	require.True(t, found, "expected a conflicting-cascade-shape-flags error on subscribes[1]; got %+v", res.Errors)
}

func TestValidateSubscribes_AllowsExactDuplicateFlags(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
					{Node: "a", Type: "attribute/x/changed", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "conflicting cascade-shape flags") {
			t.Fatalf("exact-duplicate entries must not trigger the conflict check; got %+v", res.Errors)
		}
	}
}

func TestValidateSubscribes_ErrorClassVocabularyWarning_ResolvesKindAlias(t *testing.T) {
	aliases := NewKindAliasMap()
	if err := aliases.Register("worker", "worker.alias"); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Kind: "worker"},
			{Type: "b", Executor: "handler.b", Subscribes: []SubscriptionEntry{
				{Node: "a", Type: "terminal/error/unexpected_class", ForceUpstreamRefresh: BoolPtr(false)},
			}},
		},
	}
	hooks := RegistryHooks{
		KindAliases: aliases,
		ExecutorDeclaredErrorClasses: func(name string) ([]string, bool) {
			if name == "worker.alias" {
				return []string{"known_class"}, true
			}
			return nil, false
		},
	}
	res := ValidateTemplate(spec, hooks)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].subscribes[0].type" && strings.Contains(w.Msg, "unexpected_class") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected vocabulary warning for a sender declared via kind: (resolved through effectiveExecutor); warnings: %+v", res.Warnings)
	}
}

func TestValidateSubscriptionDeclaredTags_ResolvesKindAlias(t *testing.T) {
	aliases := NewKindAliasMap()
	if err := aliases.Register("worker", "worker.alias"); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Kind: "worker"},
			{Type: "b", Executor: "handler.b", Subscribes: []SubscriptionEntry{
				{Node: "a", Type: "terminal/success", When: `"undeclared_tag" in payload.tags`, ForceUpstreamRefresh: BoolPtr(false)},
			}},
		},
	}
	hooks := RegistryHooks{
		KindAliases: aliases,
		ExecutorDeclaredTags: func(name string) ([]string, bool) {
			if name == "worker.alias" {
				return []string{"declared_tag"}, true
			}
			return nil, false
		},
	}
	res := ValidateTemplate(spec, hooks)
	found := false
	for _, e := range res.Errors {
		if e.Path == "nodes[1].subscribes[0].when" && strings.Contains(e.Msg, "undeclared_tag") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected undeclared payload.tags rejection for a sender declared via kind: (resolved through effectiveExecutor); errors: %+v", res.Errors)
	}
}
