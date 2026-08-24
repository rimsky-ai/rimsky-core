// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTemplate_Error_NilSpec(t *testing.T) {
	res := ValidateTemplate(nil, RegistryHooks{})
	require.False(t, res.Ok())
	require.True(t, findErrorContains(res.Errors, "spec is nil"))
}

func TestValidateTemplate_Error_NameRequired(t *testing.T) {
	spec := &TemplateSpec{
		Version: "1",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "name")
	require.True(t, findErrorContains(res.Errors, "name is required"))
}

func TestValidateTemplate_Error_VersionRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name:  "demo",
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "version")
	require.True(t, findErrorContains(res.Errors, "version is required"))
}

func TestValidateTemplate_Error_AtLeastOneNode(t *testing.T) {
	spec := &TemplateSpec{Name: "demo", Version: "1"}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes")
	require.True(t, findErrorContains(res.Errors, "template must declare at least one node"))
}

func TestValidateTemplate_Error_NodeTypeRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes:   []TemplateNodeDef{{Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].type")
	require.True(t, findErrorContains(res.Errors, "type is required"))
}

func TestValidateMessages_Error_LeadingSlash(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "/ping"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	require.True(t, findErrorContains(res.Errors, "must not start or end with"))
}

func TestValidateMessages_Error_EmptySegment(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "a//b"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	require.True(t, findErrorContains(res.Errors, "has empty segment"))
}

func TestValidateMessages_Error_DotInSegment(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "a.b/c"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	require.True(t, findErrorContains(res.Errors, "must not contain"))
}

func TestValidateMessages_Error_CollidesWithNodeType(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "worker/a"}},
		Nodes: []TemplateNodeDef{
			{Type: "worker/a", Executor: "handler.a"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	require.True(t, findErrorContains(res.Errors, "collides with a declared node type"))
}

func TestValidateTemplate_Error_MessageQueueMode_InvalidEnum(t *testing.T) {
	spec := &TemplateSpec{
		Name:             "demo",
		Version:          "1",
		MessageQueueMode: "not-a-real-mode",
		Nodes:            []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "message_queue_mode")
	require.True(t, findErrorContains(res.Errors, "want one of backlog | coalesce"))
}

func TestValidateSubscribes_Error_EmptyNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
				{Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
			}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].subscribes[0]")
	require.True(t, findErrorContains(res.Errors, "must declare `node:`"))
}

func TestValidateSubscribes_Error_EmptyType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
				{Node: "a", ForceUpstreamRefresh: BoolPtr(false)},
			}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].subscribes[0].type")
	require.True(t, findErrorContains(res.Errors, "`type:` is required"))
}

func TestValidateClaimProducers_Error_EmptyStoreName(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "  ", Intent: "rw", Selector: "@x"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].name")
	require.True(t, findErrorContains(res.Errors, "store name is required"))
}

func TestValidateClaimProducers_Error_InvalidIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "q", Intent: "rwx", Selector: "@x"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: func(string) bool { return true }})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].intent")
	require.True(t, findErrorContains(res.Errors, `intent = "rwx" is not valid`))
}

func TestValidateClaimProducers_Error_EmptySelector(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ClaimProducers: []NodeClaimProducerRef{
				{Name: "q", Intent: "rw", Selector: "   "},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: func(string) bool { return true }})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_producers[0].selector")
	require.True(t, findErrorContains(res.Errors, "selector is required"))
}

func TestValidateLocks_Error_EmptyName(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks:    []NodeLockRef{{Name: "  "}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[0].name")
	require.True(t, findErrorContains(res.Errors, "lock name is required"))
}

func TestValidateLocks_Error_DuplicateName(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "db-migration"},
				{Name: "db-migration"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[1].name")
	require.True(t, findErrorContains(res.Errors, `duplicate lock name "db-migration"`))
}

func TestValidateDispatchDeadlines_Error_InvalidDuration(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:            "a",
			Executor:        "h",
			SyncRPCDeadline: "not-a-duration",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].sync_rpc_deadline")
	require.True(t, findErrorContains(res.Errors, "invalid duration"))
}

func TestValidateDispatchDeadlines_Error_NegativeDuration(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*TemplateNodeDef, string)
		path  string
	}{
		{"sync_rpc_deadline", func(n *TemplateNodeDef, v string) { n.SyncRPCDeadline = v }, "nodes[0].sync_rpc_deadline"},
		{"max_quiet_period", func(n *TemplateNodeDef, v string) { n.MaxQuietPeriod = v }, "nodes[0].max_quiet_period"},
		{"max_runtime", func(n *TemplateNodeDef, v string) { n.MaxRuntime = v }, "nodes[0].max_runtime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := TemplateNodeDef{Type: "a", Executor: "h"}
			tc.apply(&n, "-5s")
			spec := &TemplateSpec{Name: "demo", Version: "1", Nodes: []TemplateNodeDef{n}}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, tc.path)
			require.True(t, findErrorContains(res.Errors, "deadlines must be >= 0"))
		})
	}
}

func TestValidatePublishers_Error_NameRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Kind: "http", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[0].name")
	require.True(t, findErrorContains(res.Errors, "name is required"))
}

func TestValidatePublishers_Error_DuplicateName(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "http", MessageType: "ev/out"},
			{Name: "pub-a", Kind: "http", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[1].name")
	require.True(t, findErrorContains(res.Errors, `duplicate publisher name "pub-a"`))
}

func TestValidatePublishers_Error_KindRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[0].kind")
	require.True(t, findErrorContains(res.Errors, "kind is required"))
}

func TestValidatePublishers_Error_MessageTypeRequired(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "http"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[0].message_type")
	require.True(t, findErrorContains(res.Errors, "message_type is required"))
}

func TestValidatePublishers_Error_MessageTypeNotDeclared(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "http", MessageType: "ev/ghost"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[0].message_type")
	require.True(t, findErrorContains(res.Errors, `message_type "ev/ghost" is not declared`))
}

func TestValidateSendsMessage_Error_WhitespaceOnly(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{
			{Type: "a", SendsMessage: "   "},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].sends_message")
	require.True(t, findErrorContains(res.Errors, "sends_message must not be whitespace-only"))
}

func TestValidateKindDeclaration_Error_KindAndDelegateMutex(t *testing.T) {
	aliases := newSeededAliases(t, "loop_counter", "rimsky.loop_counter")
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Graphs: []GraphSpec{
			{Name: MainGraphName, Nodes: []TemplateNodeDef{
				{Type: "caller", Kind: "loop_counter", Delegate: "sub"},
			}},
			{Name: "sub", Entry: "a", Exit: "b", Nodes: []TemplateNodeDef{
				{Type: "a"},
				{Type: "b", Subscribes: []SubscriptionEntry{{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: BoolPtr(false)}}},
			}},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{KindAliases: aliases})
	require.False(t, res.Ok())
	require.True(t, findErrorContains(res.Errors, "node declares both kind and delegate"))
}

func TestValidateSubscribes_Warning_TerminalErrorSenderVocabularyMismatch(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "h", Subscribes: []SubscriptionEntry{
				{Node: "a", Type: "terminal/error/pg/wrong_class", ForceUpstreamRefresh: BoolPtr(false)},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "a vocabulary mismatch is advisory only; errors: %+v", res.Errors)
	require.True(t, findWarningContains(res.Warnings, "is not in any vocabulary declared by sender"),
		"warnings: %+v", res.Warnings)
}

func TestValidateExecutorCoherence_Warning_PureCascadeNodeDeclaresAttributes(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type: "pure",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"x": map[string]any{"type": "string", "default": "v"}},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
	require.True(t, findWarningContains(res.Warnings, "pure-cascade node declares attributes"),
		"warnings: %+v", res.Warnings)
}

func TestValidateAttributesSchema_Error_SchemaDoesNotCompile(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"x": map[string]any{"type": "not-a-real-json-schema-type"}},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
	require.True(t, findErrorContains(res.Errors, "schema does not compile"))
}

func TestValidateAttributesSchema_Error_ArrayFormSourceRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{
						"type":   "string",
						"source": []any{"{{params.a}}", "{{params.b}}"},
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
	require.True(t, findErrorContains(res.Errors, "source must be a string (array-form"))
}

func TestValidateAttributesSchema_Error_ExecutorExpectedSchemaInvalidJSON(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}},
		}},
	}
	hooks := RegistryHooks{
		ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) { return []byte(`{not-json`), true },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes")
	require.True(t, findErrorContains(res.Errors, "expected_attributes_schema is not valid JSON"))
}

func TestCheckAttributeDirectiveBody_Error_InvalidFallbackLiteral(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody(`params.a | not-valid-literal`, "p", map[string]int{}, nil, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "fallback literal"))
}

func TestCheckAttributeDirectiveBody_Error_ChildMustBePartitionKey(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody("child.something_else", "p", map[string]int{}, nil, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "must be child.partition_key"))
}

func TestCheckAttributeDirectiveBody_Error_EnvInvalidVarName(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody("env.not valid!", "p", map[string]int{}, nil, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "must be env.<VAR_NAME>"))
}

func TestCheckAttributeDirectiveBody_Error_MessagesEmptyTrailingSegment(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody("messages.ev/foo.bar.", "p", map[string]int{}, nil, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "messages directive"))
	require.True(t, findErrorContains(res.Errors, "empty trailing segment"))
}

func TestCheckAttributeDirectiveBody_Error_ClaimAddressTakesNoFurtherPath(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody("claim.q.address.extra", "p", map[string]int{},
		map[string]struct{}{"q": {}}, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "takes no further field path"))
}

func TestCheckAttributeDirectiveBody_Error_UnrecognizedDirectivePrefix(t *testing.T) {
	res := &ValidationResult{}
	checkAttributeDirectiveBody("bogus.thing", "p", map[string]int{}, nil, nil, nil, res)
	require.True(t, findErrorContains(res.Errors, "must start with claim.|params.|nodes.|messages.|child.|env."))
}

// @concept: publisher
func TestValidatePublishers_Error_KindServiceDoesNotAdvertise(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "htpp", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		PublisherDeclaredKinds: func(name string) ([]string, bool) {
			if name != "pub-a" {
				return nil, false
			}
			return []string{"http", "webhook"}, true
		},
	})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "publishers[0].kind")
	require.True(t, findErrorContains(res.Errors, "publisher_unadvertised_kind"))
	require.True(t, findErrorContains(res.Errors, `[http webhook]`),
		"the refusal must name what the service does advertise; got: %+v", res.Errors)
}

// @concept: publisher
func TestValidatePublishers_AcceptsKindServiceAdvertises(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "http", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		PublisherDeclaredKinds: func(string) ([]string, bool) { return []string{"http", "webhook"}, true },
	})
	require.True(t, res.Ok(), "an advertised kind must register cleanly; got: %+v", res.Errors)
}

// @concept: publisher
func TestValidatePublishers_UnreachableServiceDoesNotBlockRegistration(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1",
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "h"}},
		Messages: []MessageSchema{{Type: "ev/out"}},
		Publishers: []PublisherSpec{
			{Name: "pub-a", Kind: "anything", MessageType: "ev/out"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{
		PublisherDeclaredKinds: func(string) ([]string, bool) { return nil, false },
	})
	require.True(t, res.Ok(),
		"a service whose capabilities could not be read must not turn into a kind refusal; got: %+v", res.Errors)
}
