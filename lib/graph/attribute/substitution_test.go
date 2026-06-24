// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return json.RawMessage(b)
}

func TestSubstitute(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"claim-topic": mustJSON(t, map[string]any{
				"area":     "northwest",
				"subtopic": "sea-otters",
				"nested":   map[string]any{"deep": "value"},
			}),
			"scope": mustJSON(t, map[string]any{
				"scope_notes": "focus on coastal habitats",
			}),
		},
		Claim: map[string]claimproducer.ClaimResult{
			"topics-ring": {
				Address:    mustJSON(t, "/data/topics/row-7"),
				ClaimScope: mustJSON(t, "row-7"),
				Payload: mustJSON(t, map[string]any{
					"area":     "rocky-shore",
					"subtopic": "tidepools",
					"nested":   map[string]any{"deep": "from-claim"},
				}),
			},
			"empty-payload": {
				Address:    mustJSON(t, "/data/none"),
				ClaimScope: mustJSON(t, "row-8"),
			},
		},
		Params: mustJSON(t, map[string]any{
			"customer_id": "cust-123",
			"flags":       map[string]any{"verbose": true},
		}),
	}

	type tcase struct {
		name          string
		raw           string
		want          string
		wantMissing   bool
		missingSubstr string
	}
	cases := []tcase{
		{name: "attribute simple", raw: "{{nodes.claim-topic.attribute.area}}", want: "northwest"},
		{name: "attribute nested path", raw: "{{nodes.claim-topic.attribute.nested.deep}}", want: "value"},
		{name: "attribute in template", raw: "items/{{nodes.claim-topic.attribute.area}}/{{nodes.claim-topic.attribute.subtopic}}.md",
			want: "items/northwest/sea-otters.md"},
		{name: "attribute unknown node", raw: "{{nodes.no-such-node.attribute.x}}", wantMissing: true,
			missingSubstr: "no upstream node"},
		{name: "attribute missing field", raw: "{{nodes.claim-topic.attribute.does_not_exist}}", wantMissing: true,
			missingSubstr: "attribute field path not found"},

		{name: "claim payload simple", raw: "{{claim.topics-ring.payload.area}}", want: "rocky-shore"},
		{name: "claim payload nested", raw: "{{claim.topics-ring.payload.nested.deep}}", want: "from-claim"},
		{name: "claim unknown alias", raw: "{{claim.no-store.payload.area}}", wantMissing: true,
			missingSubstr: "no claim for alias"},
		{name: "claim missing field", raw: "{{claim.topics-ring.payload.no_field}}", wantMissing: true,
			missingSubstr: "payload field path not found"},
		{name: "claim bare payload (whole-object pull, embedded mode)", raw: " before {{claim.topics-ring.payload}} after",
			want: ` before {"area":"rocky-shore","nested":{"deep":"from-claim"},"subtopic":"tidepools"} after`},
		{name: "claim invalid second segment", raw: "{{claim.topics-ring.metadata.x}}", wantMissing: true,
			missingSubstr: "second segment must be address|claim_scope|payload"},
		{name: "claim empty payload", raw: "{{claim.empty-payload.payload.area}}", wantMissing: true,
			missingSubstr: "claim payload is empty"},

		{name: "claim address", raw: "{{claim.topics-ring.address}}", want: "/data/topics/row-7"},
		{name: "claim address takes no field", raw: "{{claim.topics-ring.address.x}}", wantMissing: true,
			missingSubstr: "address takes no further field path"},

		{name: "claim claim_scope", raw: "{{claim.topics-ring.claim_scope}}", want: "row-7"},
		{name: "claim claim_scope takes no field", raw: "{{claim.topics-ring.claim_scope.x}}", wantMissing: true,
			missingSubstr: "claim_scope takes no further field path"},

		{name: "params simple", raw: "{{params.customer_id}}", want: "cust-123"},
		{name: "params nested map", raw: "{{params.flags.verbose}}", want: "true"},
		{name: "params missing key", raw: "{{params.no_key}}", wantMissing: true,
			missingSubstr: "param key not found"},

		{name: "no directives", raw: "literal text", want: "literal text"},
		{name: "empty input", raw: "", want: ""},

		{name: "result containing braces is literal", raw: "{{nodes.recursive.attribute.template}}"},

		{name: "unknown kind", raw: "{{userdata.foo}}", wantMissing: true,
			missingSubstr: "unknown source kind"},
		{name: "malformed empty directive", raw: "{{}}", wantMissing: true},
		{name: "nodes too short", raw: "{{nodes.x}}", wantMissing: true},
		{name: "claim too short", raw: "{{claim.x}}", wantMissing: true},
	}

	ctx.Deps["recursive"] = mustJSON(t, map[string]any{
		"template": "{{not-resubstituted}}",
	})

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := Substitute(tc.raw, ctx)
			if tc.wantMissing {
				if err == nil {
					t.Fatalf("expected ErrMissingSource, got result %q", got)
				}
				if !IsMissingSource(err) {
					t.Fatalf("expected ErrMissingSource type, got %T: %v", err, err)
				}
				if tc.missingSubstr != "" && !strings.Contains(err.Error(), tc.missingSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.missingSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.name == "result containing braces is literal" {
				want := "{{not-resubstituted}}"
				if got != want {
					t.Fatalf("recursion: want %q got %q", want, got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("want %q got %q", tc.want, got)
			}
		})
	}
}

func TestSubstitute_NilContext(t *testing.T) {
	t.Parallel()

	got, err := Substitute("plain text", ResolveContext{})
	if err != nil {
		t.Fatalf("plain text with nil ctx: %v", err)
	}
	if got != "plain text" {
		t.Fatalf("want plain, got %q", got)
	}
	_, err = Substitute("{{nodes.x.attribute.y}}", ResolveContext{})
	if !IsMissingSource(err) {
		t.Fatalf("expected ErrMissingSource for nil deps, got %v", err)
	}
}

func TestErrMissingSource_Format(t *testing.T) {
	t.Parallel()
	e := &ErrMissingSource{Directive: "nodes.x.attribute.y", Reason: "no upstream node x"}
	if !strings.Contains(e.Error(), "{{nodes.x.attribute.y}}") {
		t.Fatalf("error format should include directive in braces, got %q", e.Error())
	}
	if !strings.Contains(e.Error(), "no upstream node x") {
		t.Fatalf("error format should include reason, got %q", e.Error())
	}
}

func TestSubstitute_ErrorRedaction(t *testing.T) {
	t.Parallel()
	const sentinel = "SECRET_SENTINEL_DO_NOT_LOG"
	ctx := ResolveContext{
		Claim: map[string]claimproducer.ClaimResult{
			"alias": {
				Payload: mustJSON(t, map[string]any{"field": sentinel}),
			},
		},
	}
	_, err := Substitute("{{claim.alias.payload.no_such_field}}", ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error message LEAKED claim content: %q", err.Error())
	}
}

// @concept: message-schema
// @story: typed-message-substitution
func TestSubstitute_OneEngineTwoSurfaces(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream":     mustJSON(t, map[string]any{"reason": "from-attribute"}),
			"ping/recheck": mustJSON(t, map[string]any{"reason": "from-message"}),
		},
	}

	gotNodes, err := Substitute("{{nodes.upstream.attribute.reason}}", ctx)
	if err != nil {
		t.Fatalf("Substitute(nodes.*): %v", err)
	}
	if gotNodes != "from-attribute" {
		t.Fatalf("nodes.*: got %q, want from-attribute", gotNodes)
	}
	gotMessages, err := Substitute("{{messages.ping/recheck.reason}}", ctx)
	if err != nil {
		t.Fatalf("Substitute(messages.*): %v", err)
	}
	if gotMessages != "from-message" {
		t.Fatalf("messages.*: got %q, want from-message", gotMessages)
	}

	nodesVal, err := resolveDirectiveValueRaw("nodes.upstream.attribute.reason", ctx)
	if err != nil {
		t.Fatalf("resolveDirectiveValueRaw(nodes.*): %v", err)
	}
	if nodesVal != "from-attribute" {
		t.Fatalf("resolveDirectiveValueRaw(nodes.*): got %v, want from-attribute", nodesVal)
	}
	messagesVal, err := resolveDirectiveValueRaw("messages.ping/recheck.reason", ctx)
	if err != nil {
		t.Fatalf("resolveDirectiveValueRaw(messages.*): %v", err)
	}
	if messagesVal != "from-message" {
		t.Fatalf("resolveDirectiveValueRaw(messages.*): got %v, want from-message", messagesVal)
	}

	for _, directive := range []string{
		"nodes.upstream.attribute.no_such_field",
		"messages.ping/recheck.no_such_field",
	} {
		_, err := resolveDirectiveValueRaw(directive, ctx)
		if err == nil {
			t.Fatalf("%s: want ErrMissingSource, got nil", directive)
		}
		var missing *ErrMissingSource
		if !errors.As(err, &missing) {
			t.Fatalf("%s: want *ErrMissingSource, got %T: %v", directive, err, err)
		}
	}

	for _, c := range []struct {
		name      string
		directive string
		want      string
	}{
		{"nodes bare", "{{nodes.upstream.attribute}}", "reason"},
		{"messages bare", "{{messages.ping/recheck}}", "reason"},
	} {
		val, err := SubstituteValue(c.directive, ctx)
		if err != nil {
			t.Fatalf("%s: SubstituteValue: %v", c.name, err)
		}
		m, ok := val.(map[string]any)
		if !ok {
			t.Fatalf("%s: want map[string]any, got %T", c.name, val)
		}
		if _, ok := m[c.want]; !ok {
			t.Fatalf("%s: result missing key %q: %v", c.name, c.want, m)
		}
	}
}

// @story: typed-message-substitution
// @concept: message-schema
func TestSubstitution_SharedResolverServicesNodesAndMessages(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream":     mustJSON(t, map[string]any{"label": "alpha"}),
			"ping/recheck": mustJSON(t, map[string]any{"reason": "operator-triggered"}),
		},
	}

	got, err := Substitute(
		"label={{nodes.upstream.attribute.label}}+reason={{messages.ping/recheck.reason}}",
		ctx)
	if err != nil {
		t.Fatalf("Substitute combined: %v", err)
	}
	want := "label=alpha+reason=operator-triggered"
	if got != want {
		t.Fatalf("combined substitution: got %q, want %q", got, want)
	}

	nodesBare, err := SubstituteValue("{{nodes.upstream.attribute}}", ctx)
	if err != nil {
		t.Fatalf("SubstituteValue(nodes bare): %v", err)
	}
	nodesMap, ok := nodesBare.(map[string]any)
	if !ok {
		t.Fatalf("nodes bare: want map, got %T", nodesBare)
	}
	if nodesMap["label"] != "alpha" {
		t.Fatalf("nodes bare: got %v, want label=alpha", nodesMap)
	}
	messagesBare, err := SubstituteValue("{{messages.ping/recheck}}", ctx)
	if err != nil {
		t.Fatalf("SubstituteValue(messages bare): %v", err)
	}
	messagesMap, ok := messagesBare.(map[string]any)
	if !ok {
		t.Fatalf("messages bare: want map, got %T", messagesBare)
	}
	if messagesMap["reason"] != "operator-triggered" {
		t.Fatalf("messages bare: got %v, want reason=operator-triggered", messagesMap)
	}
}

func TestSubstitute_Messages(t *testing.T) {
	t.Parallel()

	payload := mustJSON(t, map[string]any{
		"pong_status": "ok",
		"depth": map[string]any{
			"sequence": float64(42),
			"nested":   map[string]any{"deep": "leaf"},
		},
	})

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"ping/recheck": payload,
		},
	}

	t.Run("ok-shallow-leaf", func(t *testing.T) {
		got, err := Substitute("{{messages.ping/recheck.pong_status}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "ok" {
			t.Fatalf("got %q, want ok", got)
		}
	})

	t.Run("ok-deep-leaf", func(t *testing.T) {
		got, err := Substitute("{{messages.ping/recheck.depth.nested.deep}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "leaf" {
			t.Fatalf("got %q, want leaf", got)
		}
	})

	t.Run("ok-bare-form-whole-body", func(t *testing.T) {
		val, err := SubstituteValue("{{messages.ping/recheck}}", ctx)
		if err != nil {
			t.Fatalf("SubstituteValue: %v", err)
		}
		obj, ok := val.(map[string]any)
		if !ok {
			t.Fatalf("want map[string]any, got %T", val)
		}
		if obj["pong_status"] != "ok" {
			t.Fatalf("want pong_status=ok, got %v", obj["pong_status"])
		}
	})

	t.Run("missing-field", func(t *testing.T) {
		_, err := Substitute("{{messages.ping/recheck.no_such_field}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("type-not-bound", func(t *testing.T) {
		_, err := Substitute("{{messages.other-type.pong_status}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("no-deps-at-all", func(t *testing.T) {
		_, err := Substitute("{{messages.ping/recheck.pong_status}}", ResolveContext{})
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("malformed-empty-type", func(t *testing.T) {
		_, err := Substitute("{{messages..pong_status}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("error-message-does-not-leak-payload", func(t *testing.T) {
		sentinel := "SENTINEL-DO-NOT-LEAK"
		sentinelCtx := ResolveContext{
			Deps: map[string]json.RawMessage{
				"ping/recheck": mustJSON(t, map[string]any{"value": sentinel}),
			},
		}
		_, err := Substitute("{{messages.ping/recheck.no_such_field}}", sentinelCtx)
		if err == nil {
			t.Fatalf("want error, got nil")
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error message leaked payload sentinel: %q", err.Error())
		}
	})
}

// @story: typed-message-substitution
// @concept: message-schema
func TestResolveDirective_MessagesIsSugarForNodes(t *testing.T) {
	t.Parallel()

	body := mustJSON(t, map[string]any{
		"body_field": "value-alpha",
		"nested":     map[string]any{"deep": "value-beta"},
	})

	const declaredType = "foo"

	ctxNodes := ResolveContext{
		Deps: map[string]json.RawMessage{declaredType: body},
	}
	ctxMessages := ResolveContext{
		Deps: map[string]json.RawMessage{declaredType: body},
	}

	for _, leaf := range []string{"body_field", "nested.deep"} {
		nodesDir := "{{nodes." + declaredType + ".attribute." + leaf + "}}"
		messagesDir := "{{messages." + declaredType + "." + leaf + "}}"
		gotNodes, errN := Substitute(nodesDir, ctxNodes)
		if errN != nil {
			t.Fatalf("nodes substitution for leaf %s: %v", leaf, errN)
		}
		gotMessages, errM := Substitute(messagesDir, ctxMessages)
		if errM != nil {
			t.Fatalf("messages substitution for leaf %s: %v", leaf, errM)
		}
		if gotNodes != gotMessages {
			t.Fatalf("messages.<type>.%s is NOT byte-equal to nodes.<type>.attribute.%s: nodes=%q messages=%q",
				leaf, leaf, gotNodes, gotMessages)
		}
	}

	bareNodes, err := SubstituteValue("{{nodes."+declaredType+".attribute}}", ctxNodes)
	if err != nil {
		t.Fatalf("bare nodes: %v", err)
	}
	bareMessages, err := SubstituteValue("{{messages."+declaredType+"}}", ctxMessages)
	if err != nil {
		t.Fatalf("bare messages: %v", err)
	}
	bareNodesJSON, _ := json.Marshal(bareNodes)
	bareMessagesJSON, _ := json.Marshal(bareMessages)
	if string(bareNodesJSON) != string(bareMessagesJSON) {
		t.Fatalf("bare lift differs: nodes=%s messages=%s", bareNodesJSON, bareMessagesJSON)
	}
}

func TestSubstitute_ChildPartitionKey(t *testing.T) {
	t.Parallel()

	t.Run("ok-bound", func(t *testing.T) {
		ctx := ResolveContext{ChildPartitionKey: "2024-Q3"}
		got, err := Substitute("{{child.partition_key}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "2024-Q3" {
			t.Fatalf("got %q, want 2024-Q3", got)
		}
	})

	t.Run("not-bound", func(t *testing.T) {
		_, err := Substitute("{{child.partition_key}}", ResolveContext{})
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("only-partition_key-segment-recognized", func(t *testing.T) {
		ctx := ResolveContext{ChildPartitionKey: "x"}
		_, err := Substitute("{{child.something_else}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})
}

func TestSubstitute_Env(t *testing.T) {
	t.Parallel()

	envMap := map[string]string{
		"ZONEBASE_AGENT_MCP_TOKEN": "shhh-bearer",
		"PUBLIC_API_URL":           "https://api.example.com",
		"EMPTY_BUT_SET":            "",
	}
	ctxWithEnv := ResolveContext{
		EnvLookup: func(name string) (string, bool) {
			v, ok := envMap[name]
			return v, ok
		},
	}

	t.Run("whole-directive resolves to env value", func(t *testing.T) {
		got, err := Substitute("{{env.ZONEBASE_AGENT_MCP_TOKEN}}", ctxWithEnv)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "shhh-bearer" {
			t.Fatalf("got %q, want shhh-bearer", got)
		}
	})

	t.Run("embedded form concatenates", func(t *testing.T) {
		got, err := Substitute("Bearer {{env.ZONEBASE_AGENT_MCP_TOKEN}}", ctxWithEnv)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "Bearer shhh-bearer" {
			t.Fatalf("got %q, want %q", got, "Bearer shhh-bearer")
		}
	})

	t.Run("empty-but-set value resolves to empty string", func(t *testing.T) {
		got, err := Substitute("{{env.EMPTY_BUT_SET}}", ctxWithEnv)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})

	t.Run("unset var is ErrMissingSource", func(t *testing.T) {
		_, err := Substitute("{{env.NOT_SET}}", ctxWithEnv)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
		var missing *ErrMissingSource
		if !errors.As(err, &missing) {
			t.Fatalf("want ErrMissingSource type, got %T", err)
		}
		if !strings.Contains(missing.Reason, "NOT_SET") {
			t.Fatalf("reason should name the var; got %q", missing.Reason)
		}
	})

	t.Run("lenient marker yields empty in embedded mode", func(t *testing.T) {
		got, err := Substitute("prefix={{env.NOT_SET?}}/suffix", ctxWithEnv)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "prefix=/suffix" {
			t.Fatalf("got %q, want prefix=/suffix", got)
		}
	})

	t.Run("literal fallback resolves to literal on missing", func(t *testing.T) {
		got, err := Substitute(`{{env.NOT_SET | "default-token"}}`, ctxWithEnv)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "default-token" {
			t.Fatalf("got %q, want default-token", got)
		}
	})

	t.Run("malformed env directive — no name", func(t *testing.T) {
		_, err := Substitute("{{env.}}", ctxWithEnv)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("malformed env directive — invalid name", func(t *testing.T) {
		_, err := Substitute("{{env.has-dash}}", ctxWithEnv)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

}

func TestSubstitute_Env_NilLookupFallsBackToOsLookupEnv(t *testing.T) {
	t.Setenv("RIMSKY_SUBSTITUTION_ENV_TEST", "from-os")
	got, err := Substitute("{{env.RIMSKY_SUBSTITUTION_ENV_TEST}}", ResolveContext{})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if got != "from-os" {
		t.Fatalf("got %q, want from-os", got)
	}
}

func TestSubstituteValue_WholeDirective(t *testing.T) {
	t.Parallel()
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream": mustJSON(t, map[string]any{
				"a":    float64(1),
				"b":    []any{float64(2), float64(3)},
				"list": []any{"x", "y", "z"},
			}),
		},
		Claim: map[string]claimproducer.ClaimResult{
			"staging": {
				Payload: mustJSON(t, map[string]any{"items": []any{"a", "b"}}),
			},
		},
		Params: mustJSON(t, map[string]any{
			"region":  "us-west",
			"count":   float64(42),
			"enabled": true,
			"meta":    map[string]any{"k": "v"},
		}),
	}

	t.Run("object lift via nodes.X.attribute", func(t *testing.T) {
		got, err := SubstituteValue("{{nodes.upstream.attribute}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", got)
		}
		if m["a"] != float64(1) {
			t.Fatalf("expected a=1, got %v", m["a"])
		}
	})

	t.Run("array lift", func(t *testing.T) {
		got, err := SubstituteValue("{{nodes.upstream.attribute.list}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		a, ok := got.([]any)
		if !ok {
			t.Fatalf("expected []any, got %T", got)
		}
		if len(a) != 3 || a[0] != "x" {
			t.Fatalf("unexpected array: %v", a)
		}
	})

	t.Run("string lift", func(t *testing.T) {
		got, err := SubstituteValue("{{params.region}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "us-west" {
			t.Fatalf("got %v want us-west", got)
		}
	})

	t.Run("number lift (was a string in pre-spec behavior)", func(t *testing.T) {
		got, err := SubstituteValue("{{params.count}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != float64(42) {
			t.Fatalf("got %v (%T) want 42 (float64)", got, got)
		}
	})

	t.Run("bool lift", func(t *testing.T) {
		got, err := SubstituteValue("{{params.enabled}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != true {
			t.Fatalf("got %v want true", got)
		}
	})

	t.Run("whitespace around the directive is tolerated", func(t *testing.T) {
		got, err := SubstituteValue("   {{params.region}}\n", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "us-west" {
			t.Fatalf("got %v want us-west (whitespace not tolerated)", got)
		}
	})

	t.Run("embedded mode falls through to stringify-and-concat", func(t *testing.T) {
		got, err := SubstituteValue("prefix-{{params.region}}-suffix", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "prefix-us-west-suffix" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("multiple directives → embedded mode (string)", func(t *testing.T) {
		got, err := SubstituteValue("{{params.region}}{{params.count}}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "us-west42" {
			t.Fatalf("got %v want us-west42", got)
		}
	})

	t.Run("bare {{params}} is NOT admitted (universal len(parts)<2 guard)", func(t *testing.T) {
		_, err := SubstituteValue("{{params}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("JSON null along the path is missing (existing walkPath behavior)", func(t *testing.T) {
		nctx := ResolveContext{
			Params: mustJSON(t, map[string]any{"k": nil}),
		}
		_, err := SubstituteValue("{{params.k}}", nctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})
}

func TestSubstituteValue_BareForm(t *testing.T) {
	t.Parallel()
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream": mustJSON(t, map[string]any{"a": "v"}),
		},
		Claim: map[string]claimproducer.ClaimResult{
			"staging": {
				Payload: mustJSON(t, map[string]any{"items": float64(5)}),
			},
		},
	}

	t.Run("nodes.X.attribute (whole attribute object)", func(t *testing.T) {
		got, err := SubstituteValue("{{nodes.upstream.attribute}}", ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		m, _ := got.(map[string]any)
		if m["a"] != "v" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("claim.<alias>.payload (whole payload)", func(t *testing.T) {
		got, err := SubstituteValue("{{claim.staging.payload}}", ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		m, _ := got.(map[string]any)
		if m["items"] != float64(5) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("params bare form (NOT admitted)", func(t *testing.T) {
		_, err := SubstituteValue("{{params}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})
}

func TestSubstitute_LenientMarker(t *testing.T) {
	t.Run("strict missing source raises ErrMissingSource", func(t *testing.T) {
		_, err := Substitute("{{nodes.x.attribute.y}}", ResolveContext{Deps: map[string]json.RawMessage{}})
		if !IsMissingSource(err) {
			t.Fatalf("strict missing source: want ErrMissingSource, got %v", err)
		}
	})

	t.Run("lenient missing source resolves to empty string", func(t *testing.T) {
		s, err := Substitute("{{nodes.x.attribute.y?}}", ResolveContext{Deps: map[string]json.RawMessage{}})
		if err != nil {
			t.Fatalf("lenient missing source: want nil error, got %v", err)
		}
		if s != "" {
			t.Fatalf("lenient missing source: want empty string, got %q", s)
		}
	})

	t.Run("lenient with present value returns the value", func(t *testing.T) {
		deps := map[string]json.RawMessage{"x": json.RawMessage(`{"y": "hello"}`)}
		s, err := Substitute("{{nodes.x.attribute.y?}}", ResolveContext{Deps: deps})
		if err != nil || s != "hello" {
			t.Fatalf("lenient present source: want %q nil, got %q %v", "hello", s, err)
		}
	})
}

func TestSubstituteValue_LenientMarker(t *testing.T) {
	t.Run("missing source with ? returns nil (JSON null)", func(t *testing.T) {
		v, err := SubstituteValue("{{nodes.x.attribute.y?}}", ResolveContext{Deps: map[string]json.RawMessage{}})
		if err != nil {
			t.Fatalf("lenient missing: want nil error, got %v", err)
		}
		if v != nil {
			t.Fatalf("lenient missing: want nil, got %v", v)
		}
	})
}

func TestSubstitute_EmbeddedSourceWithMarkers(t *testing.T) {
	deps := map[string]json.RawMessage{"x": json.RawMessage(`{"y": "hello"}`)}
	s, err := Substitute("greeting: {{nodes.x.attribute.y}}, optional: {{nodes.z.attribute.q?}}", ResolveContext{Deps: deps})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if s != "greeting: hello, optional: " {
		t.Fatalf("want %q, got %q", "greeting: hello, optional: ", s)
	}
}

func TestSubstitute_ClaimScope(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Claim: map[string]claimproducer.ClaimResult{
			"a": {ClaimScope: json.RawMessage([]byte("\"/scope-A\""))},
		},
	}

	got, err := Substitute("{{claim.a.claim_scope}}", ctx)
	if err != nil {
		t.Fatalf("{{claim.a.claim_scope}}: unexpected error: %v", err)
	}
	if got != "/scope-A" {
		t.Fatalf("{{claim.a.claim_scope}}: want %q, got %q", "/scope-A", got)
	}

	_, legacyErr := Substitute("{{claim.a.scope}}", ctx)
	if legacyErr == nil {
		t.Fatalf("{{claim.a.scope}}: expected *ErrMissingSource, got nil error")
	}
	var missing *ErrMissingSource
	if !errors.As(legacyErr, &missing) {
		t.Fatalf("{{claim.a.scope}}: expected *ErrMissingSource, got %T: %v", legacyErr, legacyErr)
	}
}

var liveResolverKinds = []string{"claim", "params", "nodes", "child", "messages"}

func TestSubstitutionResolverArms(t *testing.T) {
	t.Parallel()

	probes := map[string]string{
		"claim":    "claim.a.payload",
		"params":   "params.k",
		"nodes":    "nodes.n.attribute.f",
		"child":    "child.partition_key",
		"messages": "messages.some-type.f",
	}
	for _, kind := range liveResolverKinds {
		probe, ok := probes[kind]
		if !ok {
			t.Fatalf("no resolver probe defined for declared live kind %q", kind)
		}
		_, err := resolveDirectiveValueRaw(probe, ResolveContext{})
		if err == nil {
			t.Fatalf("probe %q for kind %q resolved against an empty context; tighten the probe", probe, kind)
		}
		var missing *ErrMissingSource
		if !errors.As(err, &missing) {
			t.Fatalf("probe %q for kind %q: want *ErrMissingSource, got %T: %v", probe, kind, err, err)
		}
		if strings.Contains(missing.Reason, "unknown source kind") {
			t.Fatalf("kind %q routed to the unknown-source-kind default arm (reason %q); it is not a live resolver arm — fix liveResolverKinds or restore the arm",
				kind, missing.Reason)
		}
	}
}
