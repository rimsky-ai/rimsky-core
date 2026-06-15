// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end scenario for the lenient `?` substitution marker
// (story S-template-validation-lenient-marker-recovery-e2e).
//
// A lenient `{{nodes.upstream.attribute.maybe?}}` directive whose
// source is genuinely absent at dispatch must resolve to empty through
// the FULL real stack (control-api + scheduler + supervisor + the
// dispatch-time substitution pass) and the node must reach a terminal
// Complete verdict — it must NOT fail with ErrMissingSource. A
// companion node referencing the SAME absent source WITHOUT the `?`
// marker must fail the dispatch with a missing-source diagnostic
// (`template_resolution_failed`).
//
// This pins the lenient-recovery contract end to end, where prior
// coverage stopped at the in-process resolver unit tests
// (lib/graph/attribute/substitution_test.go). Targets blessed
// invariant 12 (attributes validate twice) — the lenient resolve must
// survive the PhaseDispatch validation gate the supervisor applies to
// the resolved bag.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestLenientMarkerRecoveryE2E drives a real stack with three worker
// nodes:
//
//   - upstream — produces attribute `present` but NOT `maybe`, so any
//     `{{nodes.upstream.attribute.maybe…}}` reference resolves against a
//     genuinely-absent source.
//   - lenient — sets `note: "{{nodes.upstream.attribute.maybe?}}"`. The
//     `?` marker makes the missing source recover to empty; the node
//     dispatches with `note == ""` and reaches a terminal Complete.
//   - strict — sets `note: "{{nodes.upstream.attribute.maybe}}"`. No
//     marker, so the missing source fails the dispatch via the
//     template_resolution_failed policy chain; the node never reaches a
//     clean terminal Complete.
//
// Both `lenient` and `strict` fire off `upstream`'s terminal via an
// explicit `terminal/*` subscription — the cascade trigger is decoupled
// from the implicit `attribute/maybe/changed` edge a `{{nodes.upstream.
// attribute.maybe…}}` directive would otherwise create, because `maybe`
// is never produced and that signal never emits. So each downstream
// node genuinely dispatches once `upstream` settles, with the
// (maybe-less) upstream bag in hand, which is where the absent-source
// recovery is observed.
func TestLenientMarkerRecoveryE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @constraint: upstream produces `present`, never `maybe`.
	h.Stub.WhenType("upstream").Success(map[string]any{"present": "yes"}, true, "ok")
	h.Stub.WhenType("lenient").Success(map[string]any{}, true, "ok")
	// @deliberate: strict's dispatch fails at substitution before the stub is ever
	// invoked; no script needs to model a terminal for it.
	h.Stub.WhenType("strict").Success(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lenient-marker-recovery", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"present": map[string]any{"type": "string"},
						// @constraint: `maybe` is DECLARED (so the downstream substitution
						// refs pass the registration cross-check) but NEVER
						// produced — it is optional and the upstream stub emits
						// only `present`. At dispatch the source is therefore
						// genuinely absent, which is precisely the missing-source
						// condition the `?` marker is meant to recover from.
						"maybe": map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "lenient", Executor: "stub"},
				// @constraint: Fire this node off upstream's terminal — the cascade
				// trigger is decoupled from the (deliberately absent) `maybe`
				// attribute, so the node genuinely dispatches and we observe
				// the lenient recovery at dispatch time rather than never
				// firing because `attribute/maybe/changed` never emits.
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					// @constraint: substitution coverage for
					// `{{nodes.upstream.attribute.maybe?}}` requires the
					// attribute/maybe/changed edge even though `maybe`
					// never emits — coverage is a registration-time check.
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// @deliberate: Lenient `?` marker — absent source recovers to empty.
						"note": map[string]any{
							"type":   "string",
							"source": "{{nodes.upstream.attribute.maybe?}}",
						},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "strict",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"template_resolution_failed": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "upstream", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
					// @constraint: substitution coverage for
					// `{{nodes.upstream.attribute.maybe}}` requires the
					// attribute/maybe/changed edge at registration time.
					node.SubscriptionEntry{Node: "upstream", Type: "attribute/maybe/changed", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// @deliberate: Strict — same absent source, no marker. Missing
						// source fails the dispatch.
						"note": map[string]any{
							"type":   "string",
							"source": "{{nodes.upstream.attribute.maybe}}",
						},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-lenient-recovery", map[string]any{})

	upN := h.FindNode(iid, "upstream")
	lenientN := h.FindNode(iid, "lenient")
	strictN := h.FindNode(iid, "strict")
	require.NotNil(t, upN)
	require.NotNil(t, lenientN)
	require.NotNil(t, strictN)

	// @deliberate: (a) Lenient node dispatches with the recovered-empty note and
	// reaches a terminal Complete (settles fresh with a terminal/success
	// event).
	require.True(t, h.WaitForNodeState(upN.ID, cascade.NodeStateFresh, 20*time.Second),
		"upstream should settle fresh")
	require.True(t, h.WaitForNodeState(lenientN.ID, cascade.NodeStateFresh, 20*time.Second),
		"lenient node should reach terminal Complete (fresh), not fail with ErrMissingSource")

	// @deliberate: The stub recorded the lenient node's dispatch with `note`
	// resolved to empty string (lenient recovery).
	var lenientNote any
	var sawLenientDispatch bool
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "lenient" {
			sawLenientDispatch = true
			lenientNote = obs.Attributes["note"]
		}
	}
	require.True(t, sawLenientDispatch, "lenient node should have dispatched to the stub")
	require.Equal(t, "", lenientNote,
		"lenient `?` directive over an absent source should resolve to empty string at dispatch")

	// @constraint: (b) Strict node fails the dispatch with a missing-source
	// diagnostic — it transitions to failed via the
	// template_resolution_failed give_up policy and never reaches a
	// clean terminal Complete.
	require.True(t, h.WaitForNodeState(strictN.ID, cascade.NodeStateFailed, 20*time.Second),
		"strict node should fail the dispatch on the absent source (no `?` marker)")
	require.False(t, h.WaitForEventKind(strictN.ID, "terminal/success", 2*time.Second),
		"strict node must NOT reach a clean terminal Complete")
}
