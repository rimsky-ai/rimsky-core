// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
		{name: "deps prefix retired", raw: "{{deps.claim-topic.area}}", wantMissing: true,
			missingSubstr: "retired"},

		{name: "claim payload simple", raw: "{{claim.topics-ring.payload.area}}", want: "rocky-shore"},
		{name: "claim payload nested", raw: "{{claim.topics-ring.payload.nested.deep}}", want: "from-claim"},
		{name: "claim unknown alias", raw: "{{claim.no-store.payload.area}}", wantMissing: true,
			missingSubstr: "no claim for alias"},
		{name: "claim missing field", raw: "{{claim.topics-ring.payload.no_field}}", wantMissing: true,
			missingSubstr: "payload field path not found"},
		// @deliberate: the bare `claim.<alias>.payload` form is admitted as
		// a whole-payload pull (spec §Item 3). Substitute is
		// string-returning so it stringifies the JSON object via
		// stringifyAny.
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

// TestSubstitute_ErrorRedaction (T27) — confirms substitution failures
// don't include claim content. We plant a sentinel string inside the
// claim payload and assert the error message does NOT contain it.
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

// TestSubstitute_TriggerMessage exercises the
// `{{trigger.message.payload.X}}` directive form added by spec §E14.
func TestSubstitute_TriggerMessage(t *testing.T) {
	t.Parallel()

	payload := mustJSON(t, map[string]any{
		"partition_request_override": map[string]any{
			"date_range": map[string]any{
				"start": "2024-01-01",
				"end":   "2024-09-30",
			},
		},
		"reason": "refresh-q3-2024",
	})

	ctx := ResolveContext{TriggerMessagePayload: payload}

	t.Run("ok-shallow-leaf", func(t *testing.T) {
		got, err := Substitute("{{trigger.message.payload.reason}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "refresh-q3-2024" {
			t.Fatalf("got %q, want backfill-q3-2024", got)
		}
	})

	t.Run("ok-deep-leaf", func(t *testing.T) {
		got, err := Substitute("{{trigger.message.payload.partition_request_override.date_range.start}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "2024-01-01" {
			t.Fatalf("got %q, want 2024-01-01", got)
		}
	})

	t.Run("missing-field", func(t *testing.T) {
		_, err := Substitute("{{trigger.message.payload.no_such_field}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("no-trigger-bound", func(t *testing.T) {
		_, err := Substitute("{{trigger.message.payload.reason}}", ResolveContext{})
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("malformed-shape", func(t *testing.T) {
		_, err := Substitute("{{trigger.message.reason}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})
}

// TestSubstitute_OneEngineTwoSurfaces — load-bearing property of the
// 2026-06-14 message-schema-layer Pass 6: the substitution engine
// services `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}` through the
// SAME resolver function (`resolveDirectiveValueRaw`). One substitution
// engine, two surfaces — the cheaper shape "fork a parallel resolver
// for messages" is what STORY-typed-message-substitution falsifier
// argues against.
//
// Strategy: invoke both directive shapes against a shared
// `ResolveContext`. Both resolve through the single dispatch arm in
// `resolveDirectiveValueRaw` (the `nodes` arm reads ctx.Deps; the
// `messages` arm reads ctx.TriggerMessageType +
// ctx.TriggerMessagePayload). The test confirms both succeed against
// the same context value AND probes the dispatch arm directly via
// `resolveDirectiveValueRaw` so a refactor that splits the resolver
// into two parallel functions (one per source kind) would fail this
// test.
//
// Belt-and-braces: a property test confirms typo'd field names in
// either directive shape surface as the same `*ErrMissingSource` error
// type. A separate resolver type would be free to surface a different
// error path; the shared-resolver invariant guarantees they don't.
//
// @concept: message-schema
// @story: typed-message-substitution
func TestSubstitute_OneEngineTwoSurfaces(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream": mustJSON(t, map[string]any{"reason": "from-attribute"}),
		},
		TriggerMessagePayload: mustJSON(t, map[string]any{"reason": "from-message"}),
		TriggerMessageType:    "ping/recheck",
	}

	// @deliberate: both directive shapes dispatch through
	// `resolveDirectiveValueRaw` — the same internal function — and both
	// succeed against the same shared context value. Calling them at the
	// test surface (Substitute) does not by itself prove the internal
	// function is the same; the resolveDirectiveValueRaw probes below
	// exercise the internal dispatch directly so the test fails if the
	// resolver is forked.
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

	// @deliberate: direct dispatch probe — confirms both source kinds
	// are routed by the same single function `resolveDirectiveValueRaw`.
	// A refactor that splits the resolver into separate per-kind public
	// entry points would force this call site to change shape, surfacing
	// the regression.
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

	// @deliberate: property — typo'd fields in either surface produce
	// the same error TYPE (*ErrMissingSource). A separate parallel
	// resolver would be free to surface a different error type for one
	// kind; the shared engine guarantees they don't.
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

	// @deliberate: property — bare-form whole-object pulls succeed for
	// both surfaces via SubstituteValue (the value-returning sibling,
	// also routed through the same engine).
	for _, c := range []struct {
		name      string
		directive string
		want      string // @deliberate: key the resulting map[string]any must contain
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

// TestSubstitution_SharedResolverServicesNodesAndMessages is the
// STORY-typed-message-substitution Task 53 acceptance-pass test. The
// existing `TestSubstitute_OneEngineTwoSurfaces` already pins the
// "same resolver function" property at the dispatch surface; this test
// extends the coverage at the COMBINED-DIRECTIVE level: a single
// attribute schema's `source:` directive concatenates a `{{nodes.X.
// attribute.Y}}` reference with a `{{messages.T.F}}` reference, and
// the engine resolves BOTH within one call. A forked resolver would
// either fail to resolve one side or produce inconsistent string-
// stringification — the combined-source property would fail.
//
// @story: typed-message-substitution
// @concept: message-schema
func TestSubstitution_SharedResolverServicesNodesAndMessages(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"upstream": mustJSON(t, map[string]any{"label": "alpha"}),
		},
		TriggerMessagePayload: mustJSON(t, map[string]any{
			"reason": "operator-triggered",
		}),
		TriggerMessageType: "ping/recheck",
	}

	// @deliberate: combined-directive — a single source string
	// concatenates both surfaces. If they were resolved through different
	// functions the engine would have to thread two separate contexts;
	// the SAME `resolveDirectiveValueRaw` invocation handles both inline.
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

	// @deliberate: bare-form pulls for both surfaces, asserting both
	// routed through SubstituteValue (the value-returning sibling)
	// without any surface-specific helper getting in the way. A separate
	// resolver for `messages.*` would necessarily live behind a separate
	// SubstituteValue branch; the shared engine guarantees both ride
	// through `resolveDirectiveValueRaw`.
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

// TestSubstitute_Messages exercises the `{{messages.<type>.<field>}}`
// directive form added by the 2026-06-14 message-schema-layer plan. The
// arm reads the frame's triggering message body addressed by the
// receiver's declared message-type — the same payload bytes the
// `{{trigger.message.payload.X}}` arm reads, but routed through the
// type-discriminator so a receiver's attribute schema names what it
// expects to read rather than relying on positional binding.
//
// One substitution engine, two surfaces (per the pass's load-bearing
// property): both arms walk `ctx.TriggerMessagePayload` via the same
// `walkPath` helper. The discriminator difference is the receiver-side
// directive shape; the resolver routing converges at walkPath.
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
		TriggerMessagePayload: payload,
		TriggerMessageType:    "ping/recheck",
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

	t.Run("type-mismatch-rejects", func(t *testing.T) {
		// @deliberate: the receiver names a type that is not the frame's
		// triggering type. Static auto-subscribe prevents this in the
		// common case; the runtime check is the dynamic defense.
		_, err := Substitute("{{messages.other-type.pong_status}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("no-trigger-type-bound", func(t *testing.T) {
		_, err := Substitute("{{messages.ping/recheck.pong_status}}", ResolveContext{})
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("empty-payload-with-type-bound", func(t *testing.T) {
		// @deliberate: type matches but the payload is empty (e.g. a
		// typed message with no body) — surfaces as ErrMissingSource
		// symmetrically with the trigger arm.
		emptyCtx := ResolveContext{TriggerMessageType: "ping/recheck"}
		_, err := Substitute("{{messages.ping/recheck.pong_status}}", emptyCtx)
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
		// @blessed-invariant: message-inertness — error messages must
		// not include the triggering payload bytes. We plant a sentinel
		// in the body and confirm the missing-field reason does not
		// echo it.
		sentinel := "SENTINEL-DO-NOT-LEAK"
		sentinelCtx := ResolveContext{
			TriggerMessagePayload: mustJSON(t, map[string]any{"value": sentinel}),
			TriggerMessageType:    "ping/recheck",
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

// TestSubstitute_ChildPartitionKey exercises the `{{child.partition_key}}`
// directive form added by spec §E14.
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

// TestSubstituteValue_WholeDirective covers the spec §Item 3 whole-
// directive lift: when the trimmed input is exactly one `{{...}}`
// directive, the resolved JSON value is returned verbatim — object,
// array, string, number, or bool.
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
		// @deliberate: Spec §Item 3 deliberately keeps "whole params" out of the
		// grammar. The universal len(parts) < 2 guard at resolveDirective
		// rejects it; consumers wrap in a top-level key
		// (params.config: {...}) and pull {{params.config}}.
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

// TestSubstituteValue_BareForm covers the spec §Item 3 empty-trailing-
// path bare-form pulls (whole attribute / claim payload / trigger
// payload / named-event payload).
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
		TriggerMessagePayload: mustJSON(t, map[string]any{"kind": "trigger"}),
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

	// @deliberate: nodes.X.event.<name> whole-payload form retired
	// alongside TD-collapse-named-event-to-tags; the bare-form test
	// covers attributes / claim / trigger arms.

	t.Run("trigger.message.payload (whole trigger payload)", func(t *testing.T) {
		got, err := SubstituteValue("{{trigger.message.payload}}", ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		m, _ := got.(map[string]any)
		if m["kind"] != "trigger" {
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

// TestSubstitute_LenientMarker — per spec 2026-05-21 userdata-collapse:
// the `?` marker opts a directive into lenient-on-missing resolution.
// Missing source with `?` returns empty string (embedded mode);
// missing source without `?` returns ErrMissingSource.
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

// TestSubstituteValue_LenientMarker — whole-directive mode null lift
// for the lenient marker.
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

// TestSubstitute_EmbeddedSourceWithMarkers — embedded sources may mix
// strict directives, lenient (`?`) directives, and literal text. The
// resolution stringifies each directive's value and concatenates.
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

// TestSubstitute_ClaimScope pins the runtime resolver boundary of the
// scope→claim_scope rename to a single canonical spelling (story
// S-template-validation-claim-scope-end-to-end). The resolver MUST
// resolve `{{claim.<alias>.claim_scope}}` to the live claim's
// claim-scope bytes (stringified) and MUST reject the legacy
// `{{claim.<alias>.scope}}` spelling as an *ErrMissingSource — `scope`
// is not a recognized second segment. The registration boundary of the
// same rename is pinned by TestValidateTemplate_ClaimScopeSpelling in
// lib/graph/node.
//
// The canonical-acceptance leg is already green (the resolver admits
// `claim_scope`); the load-bearing NEW assertion is the rejection leg —
// the legacy `scope` spelling MUST surface a concrete *ErrMissingSource
// (the resolver's default-arm error class), not silently resolve.
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

	// @deliberate: Legacy spelling is rejected as a concrete *ErrMissingSource — the
	// *ErrMissingSource — the second segment `scope` is not recognized.
	_, legacyErr := Substitute("{{claim.a.scope}}", ctx)
	if legacyErr == nil {
		t.Fatalf("{{claim.a.scope}}: expected *ErrMissingSource, got nil error")
	}
	var missing *ErrMissingSource
	if !errors.As(legacyErr, &missing) {
		t.Fatalf("{{claim.a.scope}}: expected *ErrMissingSource, got %T: %v", legacyErr, legacyErr)
	}
}

// headerCountWords maps the English count word the module header uses
// ("Five recognized source kinds:") to its integer value. Only the small
// range the enumeration could plausibly use is needed.
var headerCountWords = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
	"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// headerCountLinePattern matches the module header's count declaration,
// e.g. "// Five recognized source kinds:". Capture group 1 is the count
// word.
var headerCountLinePattern = regexp.MustCompile(`(?i)^//\s*([A-Za-z]+)\s+recognized source kinds:`)

// headerBulletPattern matches a header enumeration bullet and captures the
// source-kind prefix — the token before the first `.` inside the leading
// `{{...}}` example, e.g. `nodes` from
// "//   - {{nodes.<node>.attribute.<field>}} — ...". Only bullets whose
// example opens with `{{<kind>.` are admitted; a `deps`-retirement
// paragraph or any prose line is not a bullet.
var headerBulletPattern = regexp.MustCompile(`^//\s*-\s*\{\{([a-z_]+)\.`)

// liveResolverKinds is the set of source-kind prefixes
// resolveDirectiveValueRaw actually dispatches on, EXCLUDING the retired
// `deps` arm (which the resolver keeps only to return a migration-pointer
// rejection — it is not a live source kind). This is the ground truth the
// module header must enumerate. The probe loop below proves each member is
// a real resolver arm (not the unknown-kind default arm) and that `deps`
// is the rejected/retired form, so this list cannot silently drift from
// the resolver's switch.
var liveResolverKinds = []string{"claim", "params", "nodes", "trigger", "child", "messages"}

// TestSubstitutionDocstringMatchesResolver guards against doc-drift in the
// substitution module header (substitution.go#7-14): the header's
// "<N> recognized source kinds:" enumeration must (a) declare a count that
// matches its own bullet count and (b) enumerate exactly the set of source
// kinds the live resolver dispatches on — {claim, params, nodes, trigger,
// child}, the arms resolveDirectiveValueRaw switches on excluding the
// retired `deps` rejection arm.
//
// Story S-template-validation-source-kinds-docstring-accuracy. This is a
// RED gate authored before the header is corrected: today the header says
// "Five recognized source kinds:" over six bullets that omit `trigger` and
// `child`, so both the count assertion and the membership assertion fail.
func TestSubstitutionDocstringMatchesResolver(t *testing.T) {
	t.Parallel()

	// @deliberate: prove liveResolverKinds reflects the resolver's actual
	// dispatch arms, so the membership target below is coupled to the code
	// rather than a free-floating literal. Each declared live kind must
	// NOT route to the unknown-source-kind default arm; the retired `deps`
	// form MUST be rejected (and therefore is correctly excluded from the
	// live set).
	assertResolverArms(t)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate substitution.go")
	}
	srcPath := filepath.Join(filepath.Dir(thisFile), "substitution.go")
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}

	declaredCount, bulletKinds := parseHeaderSourceKinds(t, string(srcBytes))

	if declaredCount != len(bulletKinds) {
		t.Errorf("header declares %d recognized source kinds but lists %d bullets (%v): the count word and the bullet list disagree",
			declaredCount, len(bulletKinds), bulletKinds)
	}

	// @deliberate: the distinct kind-prefixes the header enumerates must
	// equal the live resolver kind set exactly — no recognized kind
	// omitted, no listed kind the resolver does not handle.
	gotSet := distinctSorted(bulletKinds)
	wantSet := distinctSorted(liveResolverKinds)
	if !equalStringSlices(gotSet, wantSet) {
		t.Errorf("header source-kind set %v does not equal live resolver kind set %v\n  missing from header: %v\n  not handled by resolver: %v",
			gotSet, wantSet, setDifference(wantSet, gotSet), setDifference(gotSet, wantSet))
	}
}

// assertResolverArms probes resolveDirectiveValueRaw to confirm
// liveResolverKinds names exactly the live dispatch arms: every declared
// live kind resolves to something other than the unknown-source-kind
// default arm, and the retired `deps` arm is rejected (so its exclusion
// from the live set is correct). A directive that is well-formed for the
// kind but unresolvable surfaces a kind-specific ErrMissingSource Reason —
// never the "unknown source kind" Reason the default arm emits — so a
// removed arm would flip its kind into the default arm and trip this probe.
func assertResolverArms(t *testing.T) {
	t.Helper()

	// @deliberate: Well-formed-but-unresolvable directive per kind: each exercises the
	// exercises the kind's own resolver arm against an empty context, so
	// the error Reason is the arm's own missing-source reason, not the
	// default arm's "unknown source kind" reason.
	probes := map[string]string{
		"claim":    "claim.a.payload",
		"params":   "params.k",
		"nodes":    "nodes.n.attribute.f",
		"trigger":  "trigger.message.payload",
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
			// @deliberate: an empty context cannot resolve any of these
			// probes; a nil error would mean the probe is wrong, not that
			// the arm is gone.
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

	// @deliberate: the retired `deps` form must be rejected by its own
	// migration-pointer arm (NOT the unknown-source-kind default arm),
	// confirming it is a recognized-but-retired form correctly excluded
	// from the live set.
	_, depsErr := resolveDirectiveValueRaw("deps.x.y", ResolveContext{})
	var depsMissing *ErrMissingSource
	if !errors.As(depsErr, &depsMissing) {
		t.Fatalf("`deps.x.y`: want *ErrMissingSource migration-pointer, got %T: %v", depsErr, depsErr)
	}
	if strings.Contains(depsMissing.Reason, "unknown source kind") {
		t.Fatalf("`deps` routed to the unknown-source-kind default arm; expected its dedicated retired-form rejection arm")
	}
}

// parseHeaderSourceKinds parses the module header's
// "<word> recognized source kinds:" block out of substitution.go's source
// text. It returns the declared integer count (from the English count
// word) and the ordered list of kind-prefixes extracted from the
// enumeration bullets. The block runs from the count line through the
// contiguous run of bullet lines that follow it; the first non-bullet,
// non-blank comment line ends the enumeration (e.g. the `deps`-retirement
// paragraph).
func parseHeaderSourceKinds(t *testing.T, src string) (declaredCount int, bulletKinds []string) {
	t.Helper()

	lines := strings.Split(src, "\n")
	countIdx := -1
	for i, line := range lines {
		m := headerCountLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		word := strings.ToLower(m[1])
		n, known := headerCountWords[word]
		if !known {
			t.Fatalf("header count word %q is not a recognized number word (line %d: %q)", m[1], i+1, line)
		}
		countIdx = i
		declaredCount = n
		break
	}
	if countIdx < 0 {
		t.Fatal(`could not find the "<word> recognized source kinds:" declaration in substitution.go header`)
	}

	sawBullet := false
	for _, line := range lines[countIdx+1:] {
		trimmed := strings.TrimSpace(line)
		if m := headerBulletPattern.FindStringSubmatch(line); m != nil {
			bulletKinds = append(bulletKinds, m[1])
			sawBullet = true
			continue
		}
		// @deliberate: a blank comment line ("//") inside the block
		// (between the count line and the bullets, or as a trailing
		// spacer) is not a terminator on its own; only end the enumeration
		// once at least one bullet has been seen and a non-bullet content
		// line appears.
		if trimmed == "//" || trimmed == "" {
			if sawBullet {
				break
			}
			continue
		}
		// @deliberate: A non-bullet comment line after the bullets (e.g. the
		// `deps`-retirement paragraph) ends the enumeration.
		if sawBullet {
			break
		}
		// @deliberate: Non-blank, non-bullet content before any bullet means
		// the header shape is not what this parser expects — fail
		// loudly rather than silently parse an empty enumeration.
		t.Fatalf("unexpected line inside source-kind header before any bullet: %q", line)
	}

	if len(bulletKinds) == 0 {
		t.Fatal("parsed zero enumeration bullets from the source-kind header block")
	}
	return declaredCount, bulletKinds
}

// distinctSorted returns the sorted set of distinct values in in.
func distinctSorted(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// equalStringSlices reports whether two already-sorted slices are equal.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setDifference returns the members of a not present in b (both treated as
// sets). Used only for diagnostic output.
func setDifference(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, v := range b {
		inB[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := inB[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}
