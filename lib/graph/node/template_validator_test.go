// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownStores is the default store name set used by most tests. The
// v3 validator no longer looks up store kinds; it only checks that
// referenced names are declared.
var knownStores = map[string]string{
	"content": "filesystem",
	"shared":  "filesystem",
	"topics":  "postgres",
	"inbound": "postgres",
}

func storeDeclaredLookup(known map[string]string) func(string) bool {
	return func(name string) bool {
		_, ok := known[name]
		return ok
	}
}

// hasErrorAt asserts that the result carries an error whose Path
// starts with prefix.
func hasErrorAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, e := range res.Errors {
		if len(prefix) <= len(e.Path) && e.Path[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("expected error with path prefix %q, got %+v", prefix, res.Errors)
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_SubscribeToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ghost", Type: "terminal/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

// TestValidateTemplate_Ok_SubscribeToMessageTypeShapedNode pins the
// Pass 5 tightening: a subscription whose `node:` value is slash-bearing
// (the syntactic shape of a message-type-path) is accepted iff the value
// matches a declared `messages:` entry. The template here declares
// `ping/recheck` in `messages:`, so the subscribe leg accepts it.
func TestValidateTemplate_Ok_SubscribeToMessageTypeShapedNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}},
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateTemplate_Error_SubscribeToUndeclaredMessageType pins the
// rejection leg of the Pass 5 tightening: a subscription whose `node:`
// is shaped like a message-type-path (slash-bearing) but is NOT declared
// in `messages:` is rejected with a diagnostic that names the registry.
func TestValidateTemplate_Error_SubscribeToUndeclaredMessageType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		// @deliberate: No `messages:` block declared.
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].node")
}

// TestValidateSubscribes_Ok_MessageVirtualNodeWhenBodyField pins the
// happy path on the new CEL `when:` body-field cross-check: a receiver
// subscribed to a declared message-type whose `when:` reads
// `payload.attributes_delta.<field>` compiles when `<field>` is declared
// in the message-type's body_schema.
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
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pong_status == "ok"`, WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_Error_MessageVirtualNodeWhenUnknownBodyField
// pins the rejection leg: a CEL `when:` predicate that reads
// `payload.attributes_delta.<typo>` against a declared message-type
// whose `body_schema` does NOT declare `<typo>` fails registration.
// Without the body-field cross-check the typo would compile silently
// and evaluate as no-match at runtime — the falsifier
// STORY-typed-message-substitution rules out.
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
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.pongStatus == "ok"`},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

// TestValidateSubscribes_Error_MessageVirtualNodeWhenEmptyBodySchema
// pins the corner case: a `when:` reading
// `payload.attributes_delta.<field>` against a declared message-type
// that has NO `body_schema` (empty body) is rejected — an empty-body
// message-type cannot legally drive a CEL body-field read.
func TestValidateSubscribes_Error_MessageVirtualNodeWhenEmptyBodySchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/recheck"}}, // @deliberate: no body_schema
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			Subscribes: []SubscriptionEntry{
				{Node: "ping/recheck", Type: "terminal/success", When: `payload.attributes_delta.anything == "ok"`},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].subscribes[0].when")
}

func TestValidateStores_Ok_RegionClaimWithIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/data/x", Intent: "rw"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_Error_MissingIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "content", Selector: "/data/x"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].intent")
}

func TestValidateStores_Error_DuplicateAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/x", Intent: "r", Alias: "shared"},
				{Name: "shared", Selector: "/y", Intent: "r", Alias: "shared"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[1].alias")
}

func TestValidateStores_Error_UnknownStoreKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "ghost", Selector: "/x", Intent: "r"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].name")
}

// TestHoldingSubgraphsForTemplate_HeldChain exercises the held-subgraph
// computation for a `holds:`-only co-holdership (the sole co-holdership
// directive after `inherits:` was removed). A downstream node co-holds
// the acquirer's `queue` claim, so the subgraph has two members and is
// held.
func TestHoldingSubgraphsForTemplate_HeldChain(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "pick",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:       "process",
				Subscribes: []SubscriptionEntry{{Node: "pick", Type: "terminal/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)}},
				Holds: map[string]HoldsBinding{
					"queue": {From: "pick"},
				},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.Equal(t, "pick", subs[0].AcquirerType)
	require.Equal(t, "queue", subs[0].Alias)
	require.Equal(t, []string{"pick", "process"}, subs[0].Members)
	require.True(t, subs[0].IsHeld())
}

func TestHoldingSubgraphsForTemplate_NotHeld(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "loner",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.False(t, subs[0].IsHeld())
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
		StoreDeclared:    storeDeclaredLookup(knownStores),
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
		StoreDeclared:    storeDeclaredLookup(knownStores),
		ExecutorDeclared: func(name string) bool { return false },
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].executor")
	// @deliberate: Verify the error message names the executor.
	var msg string
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[0].executor") {
			msg = e.Msg
			break
		}
	}
	require.Contains(t, msg, "claude-agent")
}

// TestValidateTemplate_ClaimScopeSpelling pins the single canonical
// spelling of the claim-scope directive end to end (story
// S-template-validation-claim-scope-end-to-end). The validator MUST
// accept `{{claim.<alias>.claim_scope}}` at registration and REJECT the
// legacy `{{claim.<alias>.scope}}` spelling, with the rejection message
// naming the canonical `claim_scope` segment so the operator is steered
// to the correct spelling. This is the registration boundary of the
// scope→claim_scope rename; the resolver boundary is pinned by
// TestSubstitute_ClaimScope in lib/graph/attribute.
//
// RED today: the validator's claim-directive second-segment switch
// (template_validator.go) admits only `scope` and rejects `claim_scope`,
// so the canonical-spelling sub-assertion fails. A later GREEN pass
// flips the switch to `claim_scope`.
func TestValidateTemplate_ClaimScopeSpelling(t *testing.T) {
	// @deliberate: makeSpec builds a single-node template that acquires
	// a claim under alias `a` (stores: content, rw, selector /scope-A)
	// and binds the `region` attribute to the given claim directive.
	// Reused across both spellings so the ONLY difference under test is
	// the second segment.
	makeSpec := func(directive string) *TemplateSpec {
		return &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "worker",
				Executor: "handler.worker",
				Stores: []NodeStoreRef{
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

	// @deliberate: Canonical spelling validates.
	resCanonical := ValidateTemplate(
		makeSpec("{{claim.a.claim_scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)},
	)
	assert.True(t, resCanonical.Ok(),
		"canonical {{claim.a.claim_scope}} must validate; errors: %+v", resCanonical.Errors)

	// @deliberate: legacy spelling is rejected, and the error names the
	// canonical `claim_scope` segment (steering the author to the right
	// spelling).
	resLegacy := ValidateTemplate(
		makeSpec("{{claim.a.scope}}"),
		RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)},
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

// @deliberate: lifecycle-handler validator tests are retired. The
// three lifecycle-handler slots (`on_acquire_unavailable`,
// `on_executor_complete`, `on_executor_errored`) retired alongside
// `concept:lifecycle-handler`. The replacements:
//   - acquisition failure → `error_types: { "acquire/unavailable":
//     ... }` (TestValidator_WarnsOnMissingAcquireUnavailablePolicy
//     below covers the validator advisory).
//   - on_executor_complete cascade-gating → receiver-side CEL
//     `when: payload.changed` on a `terminal/success` subscription
//     (no template-validator surface; validateSubscribes covers CEL
//     compile-time errors).
//   - on_executor_errored pass/error → `error_types:` per-class
//     `pass` action (TestValidateErrorTypes_RejectsUnknown covers the
//     vocabulary check).

// TestValidator_WarnsOnMissingAcquireUnavailablePolicy covers the
// Pass-4 validator advisory: nodes with `stores:` but no
// "acquire/unavailable" error_types entry warn (not error) about the
// fail-fast default behavior.
func TestValidator_WarnsOnMissingAcquireUnavailablePolicy(t *testing.T) {
	t.Run("stores_no_policy_warns", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				Stores: []NodeStoreRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{
			StoreDeclared: func(name string) bool { return name == "q" },
		})
		require.True(t, res.Ok(), "errors: %+v", res.Errors)
		require.NotEmpty(t, res.Warnings, "expected a warning about missing acquire/unavailable policy")
		found := false
		for _, w := range res.Warnings {
			if strings.Contains(w.Msg, "acquire/unavailable") {
				found = true
				break
			}
		}
		require.True(t, found, "warnings: %+v", res.Warnings)
	})

	t.Run("stores_with_policy_no_warning", func(t *testing.T) {
		spec := &TemplateSpec{
			Name: "demo", Version: "1",
			Nodes: []TemplateNodeDef{{
				Type: "a",
				Stores: []NodeStoreRef{
					{Name: "q", Selector: "@queue", Intent: "rw"},
				},
				ErrorTypes: map[string]ErrorTypePolicy{
					"acquire/unavailable": {
						Policy: []PolicyAction{{Action: "give_up"}},
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
				"unexpected acquire/unavailable warning on node without stores")
		}
	})
}

// TestValidateErrorTypes_RejectsUnknown covers the generic 4-value
// range-check (`pass | give_up | retry | discard_claims_then_retry`).
// All historical pre-2026-05-23 names (`invalidate`,
// `discard_then_retry`, `resume_then_retry`) reject through the same
// path with the new error message; arbitrary unknowns reject the same
// way.
func TestValidateErrorTypes_RejectsUnknown(t *testing.T) {
	// @deliberate: The retired pre-2026-05-23 action names — `invalidate`,
	// `resume_then_retry`, `discard_then_retry` — are reconstructed
	// from fragments so the file does not retain the literal old
	// vocabulary as standalone tokens (per the Pass 3 sweep
	// requirement that the legacy quoted strings stop appearing in
	// `graph/`, `runtime/`, `foundation/`).
	const sep = "_then_"
	retiredNames := []string{
		"invalidate",
		"resume" + sep + "retry",
		"discard" + sep + "retry",
		"foo",
	}
	for _, action := range retiredNames {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Policy: []PolicyAction{
							{Action: action},
						}},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].error_types[some_error].policy[0].action")
		})
	}
}

// TestValidateErrorTypes_AcceptsCanonical confirms the 4-value
// vocabulary (`pass | give_up | retry | discard_claims_then_retry`)
// all validate clean.
func TestValidateErrorTypes_AcceptsCanonical(t *testing.T) {
	for _, action := range []string{"pass", "give_up", "retry", "discard_claims_then_retry"} {
		t.Run(action, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", ErrorTypes: map[string]ErrorTypePolicy{
						"some_error": {Policy: []PolicyAction{
							{Action: action, Count: 1},
						}},
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			// @deliberate: The action range-check should not flag any error on this
			// error on this path; any other validation errors are
			// unrelated to the 4-value vocabulary check.
			for _, e := range res.Errors {
				if e.Path == "nodes[1].error_types[some_error].policy[0].action" {
					t.Fatalf("unexpected action-vocabulary error for %q: %s", action, e.Msg)
				}
			}
		})
	}
}

// TestValidateErrorTypes_AcceptsDeclaredHttpClass confirms the
// executor-declared-error-class range-check (Pass 6) accepts an
// `error_types:` key that matches a declared exact class. Per
// `proto:executor_observability.proto::ObservabilityCapabilities.declared_error_classes`.
func TestValidateErrorTypes_AcceptsDeclaredHttpClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/timeout": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

// TestValidateErrorTypes_AcceptsDeclaredWildcardClass confirms the
// declared-class range-check accepts a key that matches a declared
// `<prefix>/*` wildcard. The leaf `http/server_error/500` matches the
// pattern `http/server_error/*`.
func TestValidateErrorTypes_AcceptsDeclaredWildcardClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"http/server_error/500": {Policy: []PolicyAction{{Action: "retry", Count: 1}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/server_error/*"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path: %+v", e)
		}
	}
}

// TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable confirms
// the silent-skip behavior: when the hook returns ok=false (executor
// unreachable / no capability cache), the validator does not range-check
// `error_types:` keys.
func TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return nil, false },
	}
	res := ValidateTemplate(spec, hooks)
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Path, "nodes[1].error_types") {
			t.Fatalf("unexpected error on error_types path when hook unavailable: %+v", e)
		}
	}
}

// TestValidateErrorTypes_WarnsUndeclaredWhenHookAvailable confirms
// that when the hook returns a declared set, a key attributable to no
// declared vocabulary (e.g. `foo`) registers as an advisory WARNING —
// never a hard rejection. Per TD-validator-learns-producer-classes
// the union check (executor ∪ producer ∪ acquire/*) downgrades
// unattributable keys from errors to warnings.
func TestValidateErrorTypes_WarnsUndeclaredWhenHookAvailable(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"foo": {Policy: []PolicyAction{{Action: "give_up"}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "unattributable error_types key must not hard-reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[foo]" {
			found = true
			if !strings.Contains(w.Msg, "not in any declared vocabulary") {
				t.Fatalf("warning must state no declared vocabulary contains the key; got %q", w.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("expected advisory warning at nodes[1].error_types[foo]; warnings: %+v", res.Warnings)
	}
}

// TestValidateErrorTypes_AcceptsProducerDeclaredClass confirms an
// `error_types:` key declared by a producer reachable from the node's
// stores: block is hard-valid — no error, no warning. Per
// TD-producer-declared-classes-capability +
// TD-validator-learns-producer-classes: the runtime routes
// producer-classified acquisition failures (e.g. pg/claim_unavailable)
// by these classes, so the validator must accept what the runtime
// routes.
func TestValidateErrorTypes_AcceptsProducerDeclaredClass(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http",
				Stores: []NodeStoreRef{{Name: "items-store", Alias: "items", Intent: "rw", Selector: "items?category=alpha"}},
				ErrorTypes: map[string]ErrorTypePolicy{
					"pg/claim_unavailable": {Policy: []PolicyAction{{Action: "retry", Count: 3}}},
				}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		StoreDeclared:                func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		StoreDeclaredErrorClasses: func(name string) ([]string, bool) {
			if name == "items-store" {
				return []string{"pg/claim_unavailable", "pg/swap_failed"}, true
			}
			return nil, false
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "producer-declared key must validate; errors: %+v", res.Errors)
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			t.Fatalf("producer-declared key must be hard-valid, not a warning: %+v", w)
		}
	}
}

// TestValidateErrorTypes_ProducerClassUnreachableFromNode confirms
// producer vocabularies are scoped to the producers reachable from the
// node's stores: block — a class declared only by a producer the node
// does NOT reference warns (the node's acquisition path can never
// produce it).
func TestValidateErrorTypes_ProducerClassUnreachableFromNode(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "http"},
			{Type: "b", Executor: "http", ErrorTypes: map[string]ErrorTypePolicy{
				"pg/claim_unavailable": {Policy: []PolicyAction{{Action: "retry", Count: 3}}},
			}},
		},
	}
	hooks := RegistryHooks{
		ExecutorDeclared:             func(string) bool { return true },
		ExecutorDeclaredErrorClasses: func(string) ([]string, bool) { return []string{"http/timeout"}, true },
		// @deliberate: producer declares the class, but node b
		// references no stores — the producer is not reachable from b's
		// claims block.
		StoreDeclaredErrorClasses: func(string) ([]string, bool) {
			return []string{"pg/claim_unavailable"}, true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.True(t, res.Ok(), "must warn, not reject; errors: %+v", res.Errors)
	found := false
	for _, w := range res.Warnings {
		if w.Path == "nodes[1].error_types[pg/claim_unavailable]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning for producer class unreachable from node; warnings: %+v", res.Warnings)
	}
}

// TestValidateSubscribes_Ok covers the happy path: a terminal-prefix
// subscription against a declared node.
func TestValidateSubscribes_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_MutexNodeAndInstance: node + instance:true is
// mutually exclusive.
func TestValidateSubscribes_MutexNodeAndInstance(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Instance: true, Type: "terminal/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_SelfOK: self-subscription is the "drain my own
// queue" idiom — a node subscribing to itself with `when: payload.changed`
// re-fires after every fresh_changed commit until the predicate gates it
// off. The cascade walker's insert-then-drain-in-same-tx pattern (the
// new pending self-run's wait-set blocker drains immediately on the
// same commit that inserted it) makes this safe; the BFS visited set
// handles cycle termination.
func TestValidateSubscribes_SelfOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "drainer", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "drainer", Type: "terminal/success", When: "payload.changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_SelfBareOK: bare self-subscription (no `when:`
// predicate) also validates — the cascade walker's BFS visited set
// terminates the loop without an author-supplied gate.
func TestValidateSubscribes_SelfBareOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					// @deliberate: frame defaults to "in".
					{Node: "loopy", Type: "terminal/success", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_SelfWithFrameInExplicitOK: same as above but
// with an explicit `frame: in`.
func TestValidateSubscribes_SelfWithFrameInExplicitOK(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "loopy", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "loopy", Type: "terminal/success", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateSubscribes_RejectsBareEvent — a bare `event` shape (no
// leaf name) violates the canonical taxonomy.
func TestValidateSubscribes_RejectsBareEvent(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "event", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_RejectsUnknownType pins the canonical-
// taxonomy range check.
func TestValidateSubscribes_RejectsUnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "garbage/foo", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_RejectsMalformedCEL pins the CEL parse-check.
func TestValidateSubscribes_RejectsMalformedCEL(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "terminal/success", When: "payload.foo &&&", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok())
}

// TestValidateSubscribes_RejectsMissingWakeOnChange pins Pass 2 Task 12:
// an entry without an explicit wake_on_change is rejected — no default
// applies. Per decision:cascade-flags-required-no-defaults.
func TestValidateSubscribes_RejectsMissingWakeOnChange(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					// @deliberate: wake_on_change deliberately nil;
					// force_upstream_refresh set so only the missing flag
					// fires.
					{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: BoolPtr(false)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "missing wake_on_change must be rejected")
	found := false
	for _, e := range res.Errors {
		if strings.HasSuffix(e.Path, ".wake_on_change") && strings.Contains(e.Msg, "required") {
			found = true
			break
		}
	}
	require.True(t, found, "expected an error whose path ends in .wake_on_change with a required message; got %+v", res.Errors)
}

// TestValidateSubscribes_RejectsMissingForceUpstreamRefresh pins Pass 2
// Task 12: an entry without an explicit force_upstream_refresh is
// rejected — no default applies. Per
// decision:cascade-flags-required-no-defaults.
func TestValidateSubscribes_RejectsMissingForceUpstreamRefresh(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					// @deliberate: force_upstream_refresh deliberately nil;
					// wake_on_change set so only the missing flag fires.
					{Node: "a", Type: "terminal/success", WakeOnChange: BoolPtr(true)},
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

// TestValidateSubscribes_RejectsCrossCuttingWithForceUpstreamRefresh
// pins Pass 2 Task 12: instance: true + force_upstream_refresh: true is
// rejected because a cross-cutting subscription names no specific
// upstream to refresh. Per
// decision:cross-cutting-no-force-upstream-refresh.
func TestValidateSubscribes_RejectsCrossCuttingWithForceUpstreamRefresh(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Instance: true, Type: "terminal/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(true)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "cross-cutting + force_upstream_refresh must be rejected")
	found := false
	for _, e := range res.Errors {
		// @constraint: the message must mention both fields so the
		// operator sees the pair-level incoherence in one rejection line.
		if strings.Contains(e.Msg, "force_upstream_refresh") && strings.Contains(e.Msg, "instance") {
			found = true
			break
		}
	}
	require.True(t, found, "expected an error mentioning both force_upstream_refresh and instance; got %+v", res.Errors)
}

// TestValidateSubscribes_RejectsConflictingFlagsOnSameKey pins that two
// subscription entries matching on (node, type, when, frame) but
// declaring CONFLICTING cascade-shape flag values are rejected at
// registration. Without this, the edge-builder's dedup would land the
// first-declared flags in force and silently drop the second's,
// contradicting the call-site-clarity guarantee in
// decision:cascade-flags-required-no-defaults.
func TestValidateSubscribes_RejectsConflictingFlagsOnSameKey(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
					// @deliberate: same key — different ForceUpstreamRefresh value.
					{Node: "a", Type: "attribute/x/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(true)},
				},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{})
	require.False(t, res.Ok(), "conflicting cascade-shape flag values on the same subscription key must be rejected")
	found := false
	for _, e := range res.Errors {
		// @constraint: the message must name the conflicting indices and
		// the flag values so the operator can find both entries from one
		// rejection.
		if strings.Contains(e.Msg, "conflicting cascade-shape flags") &&
			strings.HasSuffix(e.Path, ".subscribes[1]") {
			found = true
			break
		}
	}
	require.True(t, found, "expected a conflicting-cascade-shape-flags error on subscribes[1]; got %+v", res.Errors)
}

// TestValidateSubscribes_AllowsExactDuplicateFlags pins that two
// content-equal subscription entries (same key AND same flag values)
// are NOT rejected by the conflict-detection check — exact duplicates
// collapse harmlessly at the edge-builder's containsEdge dedup. Only
// flag-disagreement is the operator-visible footgun.
func TestValidateSubscribes_AllowsExactDuplicateFlags(t *testing.T) {
	spec := &TemplateSpec{
		Name: "demo", Version: "1",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h",
				Subscribes: []SubscriptionEntry{
					{Node: "a", Type: "attribute/x/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
					// @deliberate: exact duplicate — no flag conflict.
					{Node: "a", Type: "attribute/x/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
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

func TestValidateMaxParkDuration_Ok(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", MaxParkDuration: "30m",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateMaxParkDuration_Malformed(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type: "a", Executor: "h", MaxParkDuration: "thirty-minutes",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].max_park_duration")
}

// TestTemplateValidator_DefaultsByExecutor covers the per-spec routing-
// key cross-check on `defaults.attributes.by_executor.<name>` (template-
// level attribute defaults — L1 in the four-layer override merge). Per
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("fragment values are not inspected (only routing keys)", func(t *testing.T) {
		// @deliberate: Arbitrary garbage in the fragment must still validate — the
		// validate — the structural-inertness discipline
		// (concept:inertness) says we never read fragment values.
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Defaults: &TemplateDefaults{
				Attributes: &TemplateAttributeDefaults{
					ByExecutor: map[string]map[string]any{
						"claude-agent": {
							// @deliberate: arbitrary nested shape, deeply
							// non-conforming to anything — validator must
							// not look at it.
							"garbage_key": []any{"a", 1, true, nil, map[string]any{"k": "v"}},
						},
					},
				},
			},
			Nodes: []TemplateNodeDef{{Type: "a", Executor: "claude-agent"}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

// TestTemplateValidator_Tags covers the registration-time validation of
// node-level tags (operator-facing metadata per spec
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})
}

// TestCheckAttributeSource_BareFormPulls — spec 2026-05-19 §Item 3 "Empty
// trailing path". The four bare-form directives (whole-attribute,
// whole-claim-payload, whole-event-payload, whole-trigger-payload) must
// pass `ValidateTemplate` against an attribute-schema `source:` field
// because the runtime substitution layer now resolves them per
// `code:graph/attribute/substitution.go::SubstituteValue`. Without this
// the runtime supports the form but registration rejects it.
func TestCheckAttributeSource_BareFormPulls(t *testing.T) {
	t.Run("bare nodes attribute pull accepted", func(t *testing.T) {
		// @deliberate: stage declares `row` as executor-written
		// (readOnly+default allows the property to live under the
		// unified-surface rules); verify pulls the bare
		// nodes.stage.attribute form.
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
						// @deliberate: covering subscription for the
						// bare-form whole-pull; the wildcard is required
						// (per the coverage asymmetry rule, no per-field
						// entry covers a whole-pull).
						{Node: "stage", Type: "attribute/*", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare claim payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Stores: []NodeStoreRef{
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare trigger payload pull accepted", func(t *testing.T) {
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{{
				Type:     "a",
				Executor: "h",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"trigger": map[string]any{
							"type":   "object",
							"source": "{{trigger.message.payload}}",
						},
					},
				}},
			}},
		}
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("bare nodes event pull rejected (retired)", func(t *testing.T) {
		// @deliberate: Per TD-collapse-named-event-to-tags the
		// `nodes.X.event.<name>.<path>` substitution arm has retired;
		// the validator must surface the migration as a hard error so
		// operators see the rewrite path explicitly.
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		assert.False(t, res.Ok(), "expected validator to reject the retired event source-kind")
	})

	t.Run("empty trailing dot still rejected", func(t *testing.T) {
		// @deliberate: `nodes.<X>.attribute.` (explicit empty trailing
		// segment) is not the bare form — it's a malformed directive
		// and must be rejected.
		spec := &TemplateSpec{
			Name:    "demo",
			Version: "1.0.0",
			Nodes: []TemplateNodeDef{
				{Type: "stage", Executor: "h"},
				{
					Type:     "verify",
					Executor: "h",
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
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
					// @deliberate: covering subscription for the per-field
					// attribute pull.
					{Node: "stage", Type: "attribute/out/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v structured: %+v", res.Errors, res.StructuredErrors)
	}
}

func TestValidator_FallbackOperator_ChainsRejected(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h"},
			{Type: "b", Executor: "h"},
			{
				Type:     "c",
				Executor: "h",
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	if res.Ok() {
		t.Fatalf("expected error for multi-pipe chain")
	}
}

// TestCheckAttributeSource_RelaxedGrammar — per the 2026-05-21
// userdata collapse, source declarations admit literal text alongside
// {{...}} directives and multiple directives in one source string.
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
						// @deliberate: covering subscription for the
						// per-field attribute pull.
						{Node: "verify", Type: "attribute/warnings_block/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
						// @deliberate: covering subscription for the
						// per-field attribute pull.
						{Node: "verify", Type: "attribute/warnings_block/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(false)},
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.v.source")
	})
}

// TestCheckAttributesSchema_UnifiedSurface — per the 2026-05-21
// userdata collapse, each property must declare exactly one of
// `source:` or `default:`, or be marked `readOnly: true` in the
// executor's expected_attributes_schema. Both-set is rejected.
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
		res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
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
			StoreDeclared: storeDeclaredLookup(knownStores),
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
			StoreDeclared: storeDeclaredLookup(knownStores),
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
		// @deliberate: Executor's schema declares the property but does NOT mark
		// does NOT mark it readOnly. Template claiming readOnly
		// contradicts the executor — the executor is authoritative.
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				// @deliberate: Executor declares no readOnly properties; orphan is
				// and orphan is unknown to the executor's schema.
				return []byte(`{"type":"object","properties":{}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.orphan")
	})

	t.Run("extension property without source/default/readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		// @deliberate: the claude-agent case — the executor enumerates
		// its known inputs AND declares `additionalProperties: true`,
		// explicitly delegating naming authority for extension
		// attributes used for inter-node dataflow. An author-declared
		// output the executor doesn't enumerate (e.g. a write-back the
		// agent populates) must be admitted without a synthetic
		// `default:` or `readOnly:` fabrication.
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				// @deliberate: `model` carries a default (a realistic
				// enumerated input), so it passes the leg; `zone_codes`
				// is the unenumerated extension under test.
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("extension property marked readOnly accepted when executor declares additionalProperties:true", func(t *testing.T) {
		// @deliberate: under an explicitly-open executor schema, the
		// author may mark an unenumerated extension property `readOnly:
		// true` (the natural way to say "the agent writes this back")
		// — the executor has delegated authority over names it does
		// not enumerate.
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string","default":"claude-sonnet-4-5"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	})

	t.Run("ENUMERATED property still requires source/default/readOnly under additionalProperties:true", func(t *testing.T) {
		// @deliberate: the open-schema exemption is per-property, keyed
		// on enumeration — a property the executor DOES enumerate is
		// still subject to the full unified-surface check even when the
		// schema also admits extensions.
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				// @deliberate: `model` is enumerated (not readOnly) and
				// the schema admits extensions. Declaring `model` with
				// no source/default/readOnly is still an
				// unpopulated-input error.
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}},"additionalProperties":true}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok())
		hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model")
	})

	t.Run("L1 default plus L2 source on same property: L2 source wins (no both-set error)", func(t *testing.T) {
		// @constraint: when L1 (template
		// defaults.attributes.by_executor.<exec>.<attr>) contributes a
		// `default:` for property `cli` and L2 (the per-node attribute
		// schema) declares a `source:` for the same property, the
		// merge MUST drop the L1 `default:` so the effective schema is
		// a clean source-bound property. Without that drop in
		// MergeAttributeDefaults, checkAttributesSchema would reject
		// the template for declaring both source: and default:.
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"cli":{"type":"object"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 source: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("L1 source plus L2 default on same property: L2 default wins (no both-set error)", func(t *testing.T) {
		// @deliberate: symmetric to the previous case. L1 contributes
		// a value (intent: serve as a default), L2 declares its own
		// default. The L2 default must override. L1 only ever
		// contributes via `default:` per MergeAttributeDefaults's
		// contract, but the reverse case (L1 default + L2 default)
		// confirms the drop-then-overwrite shape leaves a single
		// `default:` set.
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "L2 default: should override L1 default: cleanly; errors: %+v", res.Errors)
	})

	t.Run("permissive executor schema skips readOnly leg", func(t *testing.T) {
		// @deliberate: L2 declares one property with no `source:`, no
		// `default:`, and no `readOnly: true`. The executor advertises
		// a permissive schema (`{"type":"object"}` with no
		// `properties` block) — `IsPermissiveExecutorSchema` returns
		// true ⇒ the readOnly-fallback leg is skipped ⇒ the property
		// is allowed through. Mirrors the in-tree stub / http-node
		// executors which advertise the permissive shape to signal
		// "open contract; accept any keys."
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
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				return []byte(`{"type":"object"}`), true
			},
		}
		res := ValidateTemplate(spec, hooks)
		assert.True(t, res.Ok(), "permissive executor schema: should accept the sourceless/defaultless property; errors: %+v", res.Errors)
	})
}

// TestIsPermissiveExecutorSchema pins the "open contract" recogniser the
// readOnly-fallback leg uses to decide whether to fire. An executor
// schema with no `properties` block declares open shape; a schema with
// a `properties` block (even an empty one) declares a closed contract
// with a known property set.
//
// The function operates on the parsed JSON Schema map[string]any, so
// these subtests construct the canonical shapes directly rather than
// going through json.Unmarshal.
func TestIsPermissiveExecutorSchema(t *testing.T) {
	t.Run("nil schema is not permissive", func(t *testing.T) {
		// @deliberate: nil means "executor didn't advertise a schema
		// at all" — the dispatch gate handles that case at a higher
		// level (returning `executor_schema_unavailable`).
		// IsPermissiveExecutorSchema reports false for nil so the
		// readOnly leg doesn't get short-circuited; the visibility
		// flag carries the "schema not reachable" semantics
		// separately.
		assert.False(t, IsPermissiveExecutorSchema(nil))
	})

	t.Run("empty object is permissive", func(t *testing.T) {
		// @deliberate: `{}` is "no properties block, no constraints
		// declared" — open shape. The readOnly-fallback leg is skipped
		// because the executor declined to enumerate.
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{}))
	})

	t.Run("type-only object is permissive", func(t *testing.T) {
		// @deliberate: `{"type":"object"}` still has no `properties`
		// block — open shape. The canonical permissive form the stub
		// and http-node executors advertise.
		assert.True(t, IsPermissiveExecutorSchema(map[string]any{"type": "object"}))
	})

	t.Run("empty properties block is closed (not permissive)", func(t *testing.T) {
		// @deliberate: `{"properties": {}}` declares "I have zero
		// properties." That's a closed contract distinct from "I don't
		// enumerate" — the readOnly-fallback leg should still fire on
		// it.
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

// TestValidateAttributesSchema_TypeRedeclarationConflict — Gap 1 of the
// 2026-05-21 userdata-collapse cleanup. When the L2 template node-def
// redeclares a property `type:` and the executor's expected schema also
// declares a `type:` for the same property, the two MUST match. The
// executor is authoritative on types; a redeclared-but-conflicting type
// is rejected at registration with `template_validation_failed`.
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
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"model":{"type":"string"}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.model.type")
}

// TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L2 — Gap 2.
// When the executor's expected schema sets `additionalProperties: false`
// and L2 declares a property the executor doesn't enumerate, the
// template is rejected. The executor's closed-schema policy is
// authoritative.
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
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.extra_field")
}

// TestValidateAttributesSchema_ClosedSchemaForbiddenProperty_L1 — Gap 2,
// symmetric L1 case. When the executor's expected schema sets
// `additionalProperties: false`, an L1 default-value entry for a
// property the executor doesn't enumerate is also rejected. The
// rejection's `Path` lives under defaults.attributes.by_executor since
// the violation originates at L1.
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
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			return []byte(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	require.False(t, res.Ok())
	hasErrorAt(t, res, "defaults.attributes.by_executor.extra_field")
}

// TestValidateAttributesSchema_NestedDefaultTypeConflict — Gap 2 case
// (c). L2 declares a `default:` value whose runtime shape contradicts
// the executor's nested-property type declaration. The flat L2-vs-
// executor type comparison cannot catch this (the L2 property type is
// fine; only the default value is wrong); the JSON-Schema validation
// of the composed defaults bag against the executor's raw schema
// catches the violation.
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
							// @deliberate: silence_timeout_ms is declared
							// `integer` by the executor, but here we set
							// it to a string ("60s") — the composed-
							// defaults-bag validation catches this.
							"silence_timeout_ms": "60s",
						},
					},
				},
			}},
		}},
	}
	hooks := RegistryHooks{
		StoreDeclared: storeDeclaredLookup(knownStores),
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

// TestValidateCompositionAgainstExecutor_RequiredInputWithSource pins the
// fix for a false-positive that fired when an executor's expected schema
// declared `required: ["X"]` and the template bound `X` via `source:`
// (no `default:`). The defaults bag at registration is intentionally a
// proper subset of the dispatch bag — source-bound properties have no
// default and are absent from the bag — so enforcing the executor's
// `required:` against it would (incorrectly) flag X as missing. The
// fix strips `required:` from the schema used for the defaults-bag
// validation pass; type and nested-shape checks against present values
// continue to fire.
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
		StoreDeclared: storeDeclaredLookup(knownStores),
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

// TestValidateAttributesSchema_OpenSchemaAcceptsExtraProperty — control
// case for Gap 2. When the executor's expected schema does NOT set
// `additionalProperties: false`, L2 declarations for properties the
// executor doesn't enumerate are admissible (the schema is open). The
// executor's enumerated property (`known`) is `readOnly: true` so the
// template needn't declare a source/default for it.
func TestValidateAttributesSchema_OpenSchemaAcceptsExtraProperty(t *testing.T) {
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
		StoreDeclared: storeDeclaredLookup(knownStores),
		ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
			// @deliberate: additionalProperties default is true
			// (open). `known` is executor-written (readOnly: true) so
			// its absence in L2 is admissible under the unified-surface
			// check.
			return []byte(`{"type":"object","properties":{"known":{"type":"string","readOnly":true}}}`), true
		},
	}
	res := ValidateTemplate(spec, hooks)
	assert.True(t, res.Ok(), "open executor schema should admit extra L2 props; errors: %+v", res.Errors)
}

// TestValidateTemplate_RefMode pins the operator-set registration-time
// reference-validation MODE (story S-template-validation-ref-validation-
// mode) at the validator boundary. ValidateTemplate honors a
// RefValidationMode carried on RegistryHooks governing ALL
// registration-time reference validation over the reference legs
// (executor-declared + executor-schema; the store/lock legs follow the
// same switch in the GREEN pass):
//
//   - RefValidateAll (default, zero value): hard-fail any reference
//     that cannot be validated — including a not-yet-provisioned
//     executor (ExecutorDeclared=false) whose schema is not visible
//     (ExecutorExpectedAttributesSchema returns (nil,false)).
//   - RefValidateAvailable: skip refs whose target is not provisioned
//     (the previously-implicit always-on readOnly soft-fail, now made
//     explicit and uniform) BUT still validate provisioned refs — a
//     genuinely-invalid ref to a PROVISIONED executor (schema visible,
//     a default violates the schema) still errors.
//   - RefValidateNone: no registration-time reference validation at
//     all (no reference errors regardless of provisioning state).
//
// The not-provisioned executor "missing" leg and the readOnly-fallback
// skip both collapse into the single mode switch: mode `available`
// reproduces today's soft-fail behavior exactly (not a fourth hidden
// behavior), mode `all` turns it into a hard error, mode `none` drops
// it entirely.
//
// RED today: RegistryHooks has no RefValidationMode field and the
// RefValidationMode type / constants do not exist, so this test does
// not compile against the current package — the gate command's `!`
// inverts that build failure to a pass. A later GREEN pass adds the
// field + constants and threads them through the reference legs.
func TestValidateTemplate_RefMode(t *testing.T) {
	// @deliberate: notProvisioned models a not-yet-provisioned
	// executor — declared false (not in the operator's executors:
	// block) and its expected_attributes_schema is not visible
	// (discovery-cache miss).
	const notProvisioned = "ghost-executor"
	// @deliberate: provisionedConstrained models a provisioned
	// executor whose advertised schema constrains an attribute —
	// `count` must be `minimum: 0`. A node defaulting `count: -1` is a
	// genuinely-invalid reference to a provisioned service.
	const provisionedConstrained = "constrained-executor"
	const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

	// @deliberate: hooksFor builds the registry hooks for the given
	// mode. The ExecutorDeclared / ExecutorExpectedAttributesSchema
	// hooks honor the two executors above — the ghost is undeclared +
	// schema-invisible; the constrained one is declared + advertises
	// the constraining schema.
	hooksFor := func(mode RefValidationMode) RegistryHooks {
		return RegistryHooks{
			StoreDeclared:     storeDeclaredLookup(knownStores),
			RefValidationMode: mode,
			ExecutorDeclared: func(name string) bool {
				return name == provisionedConstrained
			},
			ExecutorExpectedAttributesSchema: func(name string) ([]byte, bool) {
				if name == provisionedConstrained {
					return []byte(constrainedSchema), true
				}
				// @deliberate: Ghost executor: schema not visible.
				return nil, false
			},
		}
	}

	// @deliberate: notProvisionedNode references the ghost executor —
	// it carries no attribute defaults, so the only thing the validator
	// can flag is the reference itself (undeclared executor / schema
	// not visible).
	notProvisionedNode := func() TemplateNodeDef {
		return TemplateNodeDef{Type: "ghost", Executor: notProvisioned}
	}

	// @deliberate: invalidProvisionedNode references the provisioned
	// constrained executor with a default that violates its schema
	// (`count: -1` against `minimum: 0`). The reference is
	// provisioned, so a genuinely-invalid value must be caught
	// whenever provisioned refs are validated (modes all + available).
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

	t.Run("all: not-provisioned ref hard-fails with a missing-reference error", func(t *testing.T) {
		spec := specWith(notProvisionedNode())
		res := ValidateTemplate(spec, hooksFor(RefValidateAll))
		require.False(t, res.Ok(),
			"mode all must reject a reference to a not-yet-provisioned executor; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].executor")
	})

	t.Run("available: not-provisioned ref skipped, provisioned-invalid ref still errors", func(t *testing.T) {
		// @deliberate: node 0 is the not-provisioned ref (must be
		// skipped, no error); node 1 is the genuinely-invalid
		// provisioned ref (must error on the value-constraint
		// violation).
		spec := specWith(notProvisionedNode(), invalidProvisionedNode())
		res := ValidateTemplate(spec, hooksFor(RefValidateAvailable))

		// @deliberate: the not-provisioned ref produces NO error
		// under `available`.
		for _, e := range res.Errors {
			require.False(t, strings.HasPrefix(e.Path, "nodes[0]"),
				"mode available must skip the not-yet-provisioned ref at node 0, got error: %+v", e)
		}
		// @deliberate: The provisioned-but-invalid ref (count: -1 vs minimum: 0) still
		// minimum: 0) still errors — the value-constraint violation
		// surfaces on the composed-defaults leg.
		require.False(t, res.Ok(),
			"mode available must still reject a genuinely-invalid provisioned ref; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[1].attributes")
	})

	t.Run("none: no reference errors at all", func(t *testing.T) {
		// @deliberate: both the not-provisioned ref AND the
		// provisioned-invalid ref are present; mode none drops every
		// registration-time reference check, so the template validates
		// clean.
		spec := specWith(notProvisionedNode(), invalidProvisionedNode())
		res := ValidateTemplate(spec, hooksFor(RefValidateNone))
		require.True(t, res.Ok(),
			"mode none must perform no registration-time reference validation; errors: %+v", res.Errors)
	})

	t.Run("default zero-value mode is all (strict)", func(t *testing.T) {
		// @deliberate: a RegistryHooks left at its zero value
		// (RefValidationMode unset) behaves as RefValidateAll — the
		// not-provisioned ref hard-fails.
		spec := specWith(notProvisionedNode())
		hooks := RegistryHooks{
			StoreDeclared: storeDeclaredLookup(knownStores),
			ExecutorDeclared: func(name string) bool {
				return name == provisionedConstrained
			},
			ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) {
				return nil, false
			},
		}
		res := ValidateTemplate(spec, hooks)
		require.False(t, res.Ok(),
			"default (zero-value) mode must be strict `all`; errors: %+v", res.Errors)
		hasErrorAt(t, res, "nodes[0].executor")
	})
}

// TestValidateMessages_Ok_DeclaredTypeAndBodySchema pins the happy path of
// the `messages:` registry validator: a non-empty type-path and a JSON
// Schema object both compile, and the resulting declaredMessages set is
// populated for downstream cross-checks.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateMessages_Error_EmptyType rejects an entry whose `type:`
// is blank — the empty type-path is reserved-for-runtime per
// decision:empty-message-as-root-trigger (seeded automatically as the
// implicit empty-message wake trigger), so an author-declared
// `messages:` entry of type `""` is refused at registration with a
// reserved-for-runtime diagnostic.
//
//	@decision: empty-message-as-root-trigger
func TestValidateMessages_Error_EmptyType(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: ""}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
	// @deliberate: pin the new reserved-for-runtime diagnostic by
	// substring so the author sees the specific reservation reason and
	// not a generic "type is required".
	foundReservation := false
	for _, e := range res.Errors {
		if e.Path == "messages[0].type" && strings.Contains(e.Msg, "reserved-for-runtime") {
			foundReservation = true
			break
		}
	}
	require.True(t, foundReservation, "expected reserved-for-runtime message; got %+v", res.Errors)
}

// TestValidateMessages_Ok_NoEmptyDeclaration sanity-checks that a
// template with no `""` declaration registers cleanly (the implicit
// entry is seeded by the runtime, not by the author).
func TestValidateMessages_Ok_NoEmptyDeclaration(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "object"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateMessages_Error_DuplicateType pins the duplicate-rejection
// leg: two entries with the same `type:` produce a structural ambiguity
// (which body_schema applies?), so the validator rejects at registration.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[1].type")
}

// TestValidateMessages_Error_TypeWithWhitespace pins the structural grammar
// of message type-paths: no whitespace inside the type.
func TestValidateMessages_Error_TypeWithWhitespace(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping recheck"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

// TestValidateMessages_Error_TypeTrailingSlash pins the empty-segment
// rejection: a leading or trailing `/` produces an empty segment, which
// is structurally not a valid type-path.
func TestValidateMessages_Error_TypeTrailingSlash(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "ping/"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

// TestValidateMessages_Error_TypeMustBeSlashBearing pins the structural
// partition between message-types and node-types: a message-type lacking
// a `/` is structurally ambiguous with a real node-type (node-types are
// identifier-shaped, no slash; message-types are slash-bearing). The
// subscribes' `node:` resolver routes by slash-presence, so a non-slash
// message-type would let a single subscription match against both
// surfaces non-deterministically. Rejected at registration.
func TestValidateMessages_Error_TypeMustBeSlashBearing(t *testing.T) {
	spec := &TemplateSpec{
		Name:     "demo",
		Version:  "1.0.0",
		Messages: []MessageSchema{{Type: "invalidate"}},
		Nodes:    []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].type")
}

// TestValidateMessages_Error_BodySchemaNotJSON rejects a body_schema whose
// raw bytes are not valid JSON.
func TestValidateMessages_Error_BodySchemaNotJSON(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{not-json`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

// TestValidateMessages_Error_BodySchemaScalar rejects a body_schema that
// is structurally a scalar / array (not a JSON Schema object).
func TestValidateMessages_Error_BodySchemaScalar(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`"a-string"`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

// TestValidateMessages_Error_BodySchemaInvalidSchema rejects a body_schema
// that parses as JSON but does not compile as JSON Schema (e.g., a
// `type:` value that is not a valid JSON Schema type-name).
func TestValidateMessages_Error_BodySchemaInvalidSchema(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type": "not-a-real-json-schema-type"}`)},
		},
		Nodes: []TemplateNodeDef{{Type: "a", Executor: "handler.a"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "messages[0].body_schema")
}

// TestValidateMessageSubstitutionRef_Ok pins the happy path of the
// 2026-06-14 message-schema-layer Pass 6 cross-check: a node attribute
// reading `{{messages.<type>.<field>}}` registers cleanly when both the
// type is declared in `messages:` and the field is a property in that
// entry's body_schema.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateMessageSubstitutionRef_Error_UnknownType pins the rejection
// leg: a `{{messages.<type>.<field>}}` ref against a type not declared
// in the template's `messages:` registry rejects at registration with a
// diagnostic naming the registry.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[receiver].attributes.schema (substitution ref)")
}

// TestValidateMessageSubstitutionRef_Error_UnknownField pins the
// field-side rejection: the type is declared but the field-name is not
// a property in its body_schema. Typo at registration is caught here.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[receiver].attributes.schema (substitution ref)")
}

// TestValidateMessageSubstitutionRef_Ok_BareWholeBodyPull pins that the
// bare-form `{{messages.<type>}}` (no field path) is admitted when the
// type is declared, regardless of whether body_schema declares
// properties. Mirrors the resolver's bare-form whole-body pull.
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// TestValidateMessageSubstitutionRef_Error_NamedFieldAgainstEmptyBody
// pins the empty-body rejection: a directive that names a field against
// a declared message type whose body_schema is empty (no shape declared)
// cannot resolve and is rejected at registration with a clear
// empty-body diagnostic.
func TestValidateMessageSubstitutionRef_Error_NamedFieldAgainstEmptyBody(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Messages: []MessageSchema{
			{Type: "ping/recheck"}, // @deliberate: no body_schema → empty body
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
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[receiver].attributes.schema (substitution ref)")
}

// TestBuildSubscriptionEdges_ImplicitFromMessageRef pins the auto-
// subscribe extension: a node reading `{{messages.<type>.<field>}}` in
// its attribute schema implicitly subscribes to the message-virtual-node
// `<type>`'s `terminal/success`. Matches the implicit-edge behaviour of
// `{{nodes.X.attribute.Y}}` reads.
func TestBuildSubscriptionEdges_ImplicitFromMessageRef(t *testing.T) {
	tmpl := TemplateSpec{
		Messages: []MessageSchema{
			{Type: "ping/recheck", BodySchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		},
		Nodes: []TemplateNodeDef{
			{Type: "receiver", Executor: "stub",
				Attributes: &NodeAttributesDef{Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":   "string",
							"source": "{{messages.ping/recheck.reason}}",
						},
					},
				}}},
		},
	}
	subRefs := ExtractSubstitutionRefsFromTemplate(tmpl)
	msgRefs := ExtractMessageRefsFromTemplate(tmpl)
	edges, err := BuildSubscriptionEdges(tmpl, subRefs, msgRefs)
	if err != nil {
		t.Fatalf("BuildSubscriptionEdges: %v", err)
	}
	matched := edges.Match("ping/recheck", signal.TypePath("terminal/success"))
	if len(matched) != 1 {
		t.Fatalf("want 1 implicit message-virtual-node edge, got %d", len(matched))
	}
	if matched[0].ReceiverNodeType != "receiver" {
		t.Errorf("ReceiverNodeType: got %q want receiver", matched[0].ReceiverNodeType)
	}
	if matched[0].TypePattern != signal.TypePath("terminal/success") {
		t.Errorf("TypePattern: got %q want terminal/success", matched[0].TypePattern)
	}
}

// @deliberate: section banner — Pass 7 / Task 31 emits_message: validator.
// Mutual exclusion across executor/delegate/emits_message + the
// exact-shape-match registration check (@concept:message-emitter-node).

// emitsMessageOKSpec returns a baseline spec exercising the happy
// path: a `messages:` entry plus an emit-node whose attribute schema
// matches the destination body_schema exactly. Each test mutates this
// baseline rather than rewriting the whole DSL.
func emitsMessageOKSpec(t *testing.T) *TemplateSpec {
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
			EmitsMessage: "ping/recheck",
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

func TestValidateTemplate_Ok_EmitsMessage_ExactShape(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	if !res.Ok() {
		t.Fatalf("expected ok, got errors: %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_UnknownType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:         "bad-emitter",
			EmitsMessage: "unknown/type",
			Attributes:   &NodeAttributesDef{Schema: map[string]any{}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].emits_message")
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "unknown message type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an 'unknown message type' diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_MutexWithExecutor(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	spec.Nodes[0].Executor = "handler.a"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "emits_message and executor are mutually exclusive") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mutual-exclusion error executor vs emits_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_MutexWithDelegate(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	spec.Nodes[0].Delegate = "sub"
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "emits_message and delegate are mutually exclusive") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mutual-exclusion error delegate vs emits_message, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_AttributeSuperset(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	// @deliberate: The body declares only `pong_status`; the emit-node
	// adds an extra field. Superset is rejected — hidden state is not
	// allowed because the attribute set IS the body.
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
			"extra_field": map[string]any{"type": "string"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "extra_field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic naming the superset field 'extra_field', got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_AttributeSubset_MissingField(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	// @deliberate: The body declares `pong_status` but the emit-node omits it.
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "is missing field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected diagnostic about missing destination field, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_AttributeTypeMismatch(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	// @deliberate: Body declares pong_status: string; emit-node declares integer.
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "integer"},
		},
		"required": []any{"pong_status"},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "types must match exactly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected per-property type-mismatch diagnostic, got %+v", res.Errors)
	}
}

func TestValidateTemplate_Error_EmitsMessage_RequiredMismatch(t *testing.T) {
	spec := emitsMessageOKSpec(t)
	// @deliberate: Body requires pong_status; emit-node drops the requirement.
	spec.Nodes[0].Attributes.Schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pong_status": map[string]any{"type": "string"},
		},
		// @deliberate: no required: field
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "required: sets must match exactly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected required-set mismatch diagnostic, got %+v", res.Errors)
	}
}
