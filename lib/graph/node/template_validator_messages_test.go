// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	foundationspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestValidateMessages_Ok_DeclaredTypeAndBodySchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}},
					"required": ["reason"]
				}`),
			},
			{Type: "alert/fire"},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// @decision: empty-message-as-root-trigger

func TestValidateMessages_Error_EmptyType(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: ""}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	foundReservation := false
	for _, e := range res.Errors {
		if e.Path == "messages[0].type" && strings.Contains(e.Msg, "reserved-for-runtime") {
			foundReservation = true
			break
		}
	}
	require.True(t, foundReservation, "expected reserved-for-runtime message; got %+v", res.Errors)
}

func TestValidateMessages_Ok_NoEmptyDeclaration(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "object"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateMessages_Error_DuplicateType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck"},
			{Type: "ping/recheck"},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[1].type")
}

func TestValidateMessages_Error_TypeWithWhitespace(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping recheck"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_TypeTrailingSlash(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_TypeMustBeSlashBearing(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "invalidate"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

func TestValidateMessages_Error_BodySchemaNotJSON(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{not-json`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessages_Error_BodySchemaScalar(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`"a-string"`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessages_Error_BodySchemaInvalidSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "not-a-real-json-schema-type"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

func TestValidateMessageSubstitutionRef_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{
				Type: "ping/recheck",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}, "pong_status": {"type": "string"}}
				}`),
			},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{
					Node:                 "ping/recheck",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: BoolPtr(false),
				},
			},
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
}

func TestValidateMessageSubstitutionRef_Ok_MessageTypeWhitespacePadded(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{
				Type: "  ping/recheck  ",
				BodySchema: []byte(`{
					"type": "object",
					"properties": {"reason": {"type": "string"}}
				}`),
			},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{
					Node:                 "ping/recheck",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: BoolPtr(false),
				},
			},
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "surrounding whitespace in messages[].type must not break body_schema field lookups; errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
}

func TestValidateMessageSubstitutionRef_Error_UnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.other/type.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateMessageSubstitutionRef_Error_UnknownField(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.no_such_field}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateMessageSubstitutionRef_Ok_BareWholeBodyPull(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{
					Node:                 "ping/recheck",
					Type:                 "terminal/success",
					ForceUpstreamRefresh: BoolPtr(false),
				},
			},
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"whole_body": map[string]any{
						"type":   "object",
						"source": "{{messages.ping/recheck}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
}

func TestValidateMessageSubstitutionRef_Error_NamedFieldAgainstEmptyBody(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck"},
		},
		Nodes: []TemplateNodeDef{{
			Type:     "receiver",
			Executor: "handler.a",
			Attributes: &NodeAttributesDef{Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":   "string",
						"source": "{{messages.ping/recheck.reason}}",
					},
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateTemplate_Ok_SendsMessage_ExactShape(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_UnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:         "bad-emitter",
			SendsMessage: "unknown/type",
			Attributes:   &NodeAttributesDef{Schema: map[string]any{}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].sends_message")
	if !findErrorContains(res.Errors, "unknown message type") {
		t.Fatalf("expected an 'unknown message type' diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_MutexWithExecutor(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Executor = "handler.a"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "sends_message and executor are mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error executor vs sends_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_DirectSendMessageExecutorWithoutSendsMessage(t *testing.T) {
	aliases := NewKindAliasMap()
	require.NoError(t, aliases.Register(foundationspec.SendMessageKindName, "rimsky.send_message"))
	tspec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "direct-send-message",
			Executor: "rimsky.send_message",
		}},
	}
	res := ValidateTemplate(tspec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		KindAliases:   aliases,
	})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "may only be reached via sends_message") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a direct-executor-without-sends_message diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Ok_SendMessageViaSendsMessageSugarBypassesDirectExecutorCheck(t *testing.T) {
	aliases := NewKindAliasMap()
	require.NoError(t, aliases.Register(foundationspec.SendMessageKindName, "rimsky.send_message"))
	tspec := sendsMessageOKSpec(t)
	res := ValidateTemplate(tspec, RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownClaimProducers),
		KindAliases:   aliases,
	})
	if !res.Ok() {
		t.Fatalf("expected ok for sends_message sugar even with kind aliases wired, got errors: %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_MutexWithDelegate(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Delegate = "sub"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "sends_message and delegate are mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error delegate vs sends_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeSuperset(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
			"extra_field": map[string]any{"type": "string"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "extra_field") {
		t.Fatalf("expected diagnostic naming the superset field 'extra_field', got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeSubset_MissingField(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "is missing field") {
		t.Fatalf("expected diagnostic about missing destination field, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_AttributeTypeMismatch(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "integer"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "types must match exactly") {
		t.Fatalf("expected per-property type-mismatch diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_SendsMessage_RequiredMismatch(t *testing.T) {
	spec := sendsMessageOKSpec(t)
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	require.False(t, res.Ok())
	if !findErrorContains(res.Errors, "required: sets must match exactly") {
		t.Fatalf("expected required-set mismatch diagnostic, got %+v", res.Errors)
	}
}

func TestValidateMessageQueueMode_Warning_CoalesceWithMultipleTypes(t *testing.T) {
	spec := coalesceWarningSpec("coalesce", "job/start", "job/stop")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "warning must not block registration; errors: %+v", res.Errors)
	warns := coalesceCrossTypeWarnings(res)
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Msg, "cancels ALL")
	assert.Contains(t, warns[0].Msg, "job/start")
	assert.Contains(t, warns[0].Msg, "job/stop")
}

func TestValidateMessageQueueMode_NoWarning_CoalesceSingleType(t *testing.T) {
	spec := coalesceWarningSpec("coalesce", "job/start")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, coalesceCrossTypeWarnings(res))
}

func TestValidateMessageQueueMode_NoWarning_BacklogWithMultipleTypes(t *testing.T) {
	spec := coalesceWarningSpec("backlog", "job/start", "job/stop")
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownClaimProducers)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, coalesceCrossTypeWarnings(res))
}

func sendsMessageOKSpec(t *testing.T) *TemplateSpec {
	t.Helper()
	return &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{{
			Type: "ping/recheck",
			BodySchema: []byte(`{
				"type": "object",
				"properties": {
					"pong_status": {"type": "string"}
				},
				"required": ["pong_status"]
			}`),
		}},
		Nodes: []TemplateNodeDef{{
			Type:         "ping-recheck-emitter",
			SendsMessage: "ping/recheck",
			Attributes: &NodeAttributesDef{
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pong_status": map[string]any{"type": "string"},
					},
					"required": []any{"pong_status"},
				},
			},
		}},
	}
}

func coalesceWarningSpec(mode string, types ...string) *TemplateSpec {
	msgs := make([]MessageSchema, 0, len(types))
	for _, tp := range types {
		msgs = append(msgs, MessageSchema{Type: tp})
	}
	return &TemplateSpec{
		Name:             "demo",
		Version:          "1.0.0",
		MessageQueueMode: mode,
		Messages:         msgs,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
}

func coalesceCrossTypeWarnings(res ValidationResult) []ValidationWarning {
	var out []ValidationWarning
	for _, w := range res.Warnings {
		if w.Path == "message_queue_mode" {
			out = append(out, w)
		}
	}
	return out
}
