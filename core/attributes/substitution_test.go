package attributes

import (
	"strings"
	"testing"

	"github.com/fallguy/rimsky/core/store"
)

func TestSubstitute(t *testing.T) {
	t.Parallel()

	ctx := ResolveContext{
		Deps: map[string]map[string]any{
			"claim-topic": {
				"area":     "northwest",
				"subtopic": "sea-otters",
				"nested": map[string]any{
					"deep": "value",
				},
			},
			"scope": {
				"scope_notes": "focus on coastal habitats",
			},
		},
		Claims: map[string]store.ClaimResult{
			"topics-ring": {
				ClaimID: "row-7",
				Payload: map[string]any{
					"area":     "rocky-shore",
					"subtopic": "tidepools",
					"nested": map[string]any{
						"deep": "from-claim",
					},
				},
			},
			"empty-payload": {ClaimID: "row-8", Payload: nil},
		},
		Params: map[string]any{
			"customer_id": "cust-123",
			"flags": map[string]any{
				"verbose": true,
			},
		},
	}

	type tcase struct {
		name        string
		raw         string
		want        string
		wantMissing bool
		// missingSubstr is checked against err.Error() when wantMissing is true.
		missingSubstr string
	}
	cases := []tcase{
		// deps source
		{name: "deps simple", raw: "{{deps.claim-topic.area}}", want: "northwest"},
		{name: "deps nested path", raw: "{{deps.claim-topic.nested.deep}}", want: "value"},
		{name: "deps in template", raw: "items/{{deps.claim-topic.area}}/{{deps.claim-topic.subtopic}}.md",
			want: "items/northwest/sea-otters.md"},
		{name: "deps unknown node", raw: "{{deps.no-such-node.x}}", wantMissing: true,
			missingSubstr: "no upstream node"},
		{name: "deps missing field", raw: "{{deps.claim-topic.does_not_exist}}", wantMissing: true,
			missingSubstr: "field path not found"},

		// claim source
		{name: "claim payload simple", raw: "{{claim.topics-ring.payload.area}}", want: "rocky-shore"},
		{name: "claim payload nested", raw: "{{claim.topics-ring.payload.nested.deep}}", want: "from-claim"},
		{name: "claim unknown store", raw: "{{claim.no-store.payload.area}}", wantMissing: true,
			missingSubstr: "no claim for store"},
		{name: "claim missing field", raw: "{{claim.topics-ring.payload.no_field}}", wantMissing: true,
			missingSubstr: "payload field path not found"},
		// `{{claim.topics-ring.area}}` has only 2 segments after `claim.`,
		// trips the length check before the payload-prefix check; either
		// way it's an error. We assert the typed error class, not the
		// specific reason string.
		{name: "claim missing payload prefix", raw: "{{claim.topics-ring.area}}", wantMissing: true},
		// `{{claim.topics-ring.metadata.x}}` has 3 segments — exercises
		// the explicit "second segment must be 'payload'" branch.
		{name: "claim non-payload prefix", raw: "{{claim.topics-ring.metadata.x}}", wantMissing: true,
			missingSubstr: "second segment must be 'payload'"},
		{name: "claim nil payload", raw: "{{claim.empty-payload.payload.area}}", wantMissing: true,
			missingSubstr: "claim payload is nil"},

		// params source
		{name: "params simple", raw: "{{params.customer_id}}", want: "cust-123"},
		{name: "params nested map", raw: "{{params.flags.verbose}}", want: "true"},
		{name: "params missing key", raw: "{{params.no_key}}", wantMissing: true,
			missingSubstr: "param key not found"},

		// no-op / mixed
		{name: "no directives", raw: "literal text", want: "literal text"},
		{name: "empty input", raw: "", want: ""},

		// recursion not performed
		{name: "result containing braces is literal",
			// deps.with_braces returns the literal "{{not-resubstituted}}".
			// Substitute should not re-scan; the output should still contain `{{`.
			raw: "{{deps.recursive.template}}"},

		// unknown source kind
		{name: "unknown kind", raw: "{{userdata.foo}}", wantMissing: true,
			missingSubstr: "unknown source kind"},
		{name: "malformed empty directive", raw: "{{}}", wantMissing: true},

		// malformed directive shapes (length checks)
		{name: "deps too short", raw: "{{deps.x}}", wantMissing: true},
		{name: "claim too short", raw: "{{claim.x}}", wantMissing: true},
	}

	// Add a deps map entry so the recursion test can resolve.
	ctx.Deps["recursive"] = map[string]any{
		"template": "{{not-resubstituted}}",
	}

	for _, tc := range cases {
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
				// The single-pass rule: substitution puts the literal text
				// in place; the surrounding `{{not-resubstituted}}` is not
				// re-scanned.
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

	// Nil maps are tolerated — they're functionally empty. A directive
	// that needs them resolves to ErrMissingSource.
	got, err := Substitute("plain text", ResolveContext{})
	if err != nil {
		t.Fatalf("plain text with nil ctx: %v", err)
	}
	if got != "plain text" {
		t.Fatalf("want plain, got %q", got)
	}

	_, err = Substitute("{{deps.x.y}}", ResolveContext{})
	if !IsMissingSource(err) {
		t.Fatalf("expected ErrMissingSource for nil deps, got %v", err)
	}
}

func TestErrMissingSource_Format(t *testing.T) {
	t.Parallel()
	e := &ErrMissingSource{Directive: "deps.x.y", Reason: "no upstream node x"}
	if !strings.Contains(e.Error(), "{{deps.x.y}}") {
		t.Fatalf("error format should include directive in braces, got %q", e.Error())
	}
	if !strings.Contains(e.Error(), "no upstream node x") {
		t.Fatalf("error format should include reason, got %q", e.Error())
	}
}
