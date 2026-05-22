// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/foundation/locks"
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
		Claim: map[string]locks.ClaimResult{
			"topics-ring": {
				Address: mustJSON(t, "/data/topics/row-7"),
				Scope:   mustJSON(t, "row-7"),
				Payload: mustJSON(t, map[string]any{
					"area":     "rocky-shore",
					"subtopic": "tidepools",
					"nested":   map[string]any{"deep": "from-claim"},
				}),
			},
			"empty-payload": {
				Address: mustJSON(t, "/data/none"),
				Scope:   mustJSON(t, "row-8"),
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
		// nodes.<X>.attribute.<...> source (post-2026-05-14)
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

		// claim payload
		{name: "claim payload simple", raw: "{{claim.topics-ring.payload.area}}", want: "rocky-shore"},
		{name: "claim payload nested", raw: "{{claim.topics-ring.payload.nested.deep}}", want: "from-claim"},
		{name: "claim unknown alias", raw: "{{claim.no-store.payload.area}}", wantMissing: true,
			missingSubstr: "no claim for alias"},
		{name: "claim missing field", raw: "{{claim.topics-ring.payload.no_field}}", wantMissing: true,
			missingSubstr: "payload field path not found"},
		// Post-spec 2026-05-19 Item 3: the bare `claim.<alias>.payload` form
		// is admitted as a whole-payload pull. Substitute (string-returning)
		// stringifies the JSON object via stringifyAny.
		{name: "claim bare payload (whole-object pull, embedded mode)", raw: " before {{claim.topics-ring.payload}} after",
			want: ` before {"area":"rocky-shore","nested":{"deep":"from-claim"},"subtopic":"tidepools"} after`},
		{name: "claim invalid second segment", raw: "{{claim.topics-ring.metadata.x}}", wantMissing: true,
			missingSubstr: "second segment must be address|scope|payload"},
		{name: "claim empty payload", raw: "{{claim.empty-payload.payload.area}}", wantMissing: true,
			missingSubstr: "claim payload is empty"},

		// claim address
		{name: "claim address", raw: "{{claim.topics-ring.address}}", want: "/data/topics/row-7"},
		{name: "claim address takes no field", raw: "{{claim.topics-ring.address.x}}", wantMissing: true,
			missingSubstr: "address takes no further field path"},

		// claim scope
		{name: "claim scope", raw: "{{claim.topics-ring.scope}}", want: "row-7"},
		{name: "claim scope takes no field", raw: "{{claim.topics-ring.scope.x}}", wantMissing: true,
			missingSubstr: "scope takes no further field path"},

		// params
		{name: "params simple", raw: "{{params.customer_id}}", want: "cust-123"},
		{name: "params nested map", raw: "{{params.flags.verbose}}", want: "true"},
		{name: "params missing key", raw: "{{params.no_key}}", wantMissing: true,
			missingSubstr: "param key not found"},

		// no-op / mixed
		{name: "no directives", raw: "literal text", want: "literal text"},
		{name: "empty input", raw: "", want: ""},

		// recursion not performed
		{name: "result containing braces is literal", raw: "{{nodes.recursive.attribute.template}}"},

		// unknown source kind
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
		Claim: map[string]locks.ClaimResult{
			"alias": {
				Payload: mustJSON(t, map[string]any{"field": sentinel}),
			},
		},
	}
	// Walk a path that doesn't exist.
	_, err := Substitute("{{claim.alias.payload.no_such_field}}", ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error message LEAKED claim content: %q", err.Error())
	}
}

// TestSubstitute_NodesEvent covers the new nodes.<emitter>.event.<name>.<path>
// substitution source kind (plan F4).
func TestSubstitute_NodesEvent(t *testing.T) {
	t.Parallel()

	emissions := map[string]json.RawMessage{
		"router|action_taken": mustJSON(t, map[string]any{
			"action": "approve",
			"score":  0.92,
		}),
	}
	ctx := ResolveContext{
		EventLookup: func(emitter, name string) (json.RawMessage, bool) {
			payload, ok := emissions[emitter+"|"+name]
			return payload, ok
		},
	}

	t.Run("ok-leaf", func(t *testing.T) {
		got, err := Substitute("{{nodes.router.event.action_taken.action}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "approve" {
			t.Fatalf("got %q, want approve", got)
		}
	})

	t.Run("missing-emitter", func(t *testing.T) {
		_, err := Substitute("{{nodes.unknown.event.action_taken.action}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("missing-field", func(t *testing.T) {
		_, err := Substitute("{{nodes.router.event.action_taken.no_such_field}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("nil-lookup-rejects", func(t *testing.T) {
		_, err := Substitute("{{nodes.router.event.action_taken.action}}", ResolveContext{})
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})

	t.Run("malformed-shape", func(t *testing.T) {
		_, err := Substitute("{{nodes.router.action_taken}}", ctx)
		if !IsMissingSource(err) {
			t.Fatalf("want ErrMissingSource, got %v", err)
		}
	})
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
		"reason": "backfill-q3-2024",
	})

	ctx := ResolveContext{TriggerMessagePayload: payload}

	t.Run("ok-shallow-leaf", func(t *testing.T) {
		got, err := Substitute("{{trigger.message.payload.reason}}", ctx)
		if err != nil {
			t.Fatalf("Substitute: %v", err)
		}
		if got != "backfill-q3-2024" {
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
		Claim: map[string]locks.ClaimResult{
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
		// Spec §Item 3 deliberately keeps "whole params" out of the
		// grammar. The universal len(parts) < 2 guard at
		// resolveDirective#202 rejects it; consumers wrap in a top-
		// level key (params.config: {...}) and pull {{params.config}}.
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
		Claim: map[string]locks.ClaimResult{
			"staging": {
				Payload: mustJSON(t, map[string]any{"items": float64(5)}),
			},
		},
		EventLookup: func(emitter, name string) (json.RawMessage, bool) {
			if emitter == "upstream" && name == "finished" {
				return mustJSON(t, map[string]any{"ok": true}), true
			}
			return nil, false
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

	t.Run("nodes.X.event.<name> (whole event payload)", func(t *testing.T) {
		got, err := SubstituteValue("{{nodes.upstream.event.finished}}", ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		m, _ := got.(map[string]any)
		if m["ok"] != true {
			t.Fatalf("got %v", got)
		}
	})

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
