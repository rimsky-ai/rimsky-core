// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-producer-class-routing executable proof: a producer that
// declares its own acquisition-failure vocabulary (here
// "pg/claim_unavailable", advertised via
// claim_producer.proto::CapabilitiesResponse.declared_error_classes and
// carried on the Unavailable arm's error_class) is routable from a
// template's error_types: block in BOTH documented shapes:
//
//   - template A keys the exact producer-declared class
//     (error_types: { "pg/claim_unavailable": retry... }) — must
//     REGISTER successfully (the validator range-checks error_types:
//     keys against the executor ∪ producer ∪ acquire/* union, and the
//     producer half comes from the observability handshake's discovery
//     cache) and route at runtime via the exact-key match;
//   - template B keys only the synthetic family
//     (error_types: { "acquire/unavailable": retry... }) — must still
//     catch the producer-classified failure via the documented
//     acquire/* prefix fallback (lookupPolicy's fallback order).
//
// In both shapes the configured retry action observably fires: the
// canonical transient/retry/1/pg/claim_unavailable signal (keyed on the
// PRODUCER class in both — the fallback affects policy lookup only,
// never the emitted class) lands on the event log, the node is held
// stale by the retry chain instead of failing fast, and after the
// queue is seeded a subsequent retry drives the node to fresh.
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// producerClassUnavailable is the producer-declared acquisition-failure
// class the stub producer advertises and names on its Unavailable arm
// in this proof. The value deliberately matches a real producer's leaf
// (the bundled postgres store's) so the proof exercises the same class
// shape operators will route.
const producerClassUnavailable = "pg/claim_unavailable"

// startClassifyingProducer boots a stub producer that (a) declares
// producerClassUnavailable in its capabilities vocabulary and (b) names
// that class on the Unavailable arm when its (initially empty) @queue
// pick-policy has no items. Returns the harness wired to it.
func startClassifyingProducer(t *testing.T) (*scenario.Harness, *stubstore.Store) {
	t.Helper()
	caps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		DeclaredErrorClasses:  []string{producerClassUnavailable},
	}
	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: caps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// @deliberate: Empty queue — Open returns Unavailable, classified
				// with the producer-declared class.
				UnavailableClass: producerClassUnavailable,
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: caps,
				},
			},
		},
	})
	return h, sub
}

// driveProducerClassifiedRetry deploys a single-worker template with
// the given error_types: block, creates an instance against the empty
// queue, and asserts the STORY acceptance: the producer-classified
// acquisition failure routes to the declared retry action (observed
// via the transient/retry/1/pg/claim_unavailable signal on the event
// log and the node held un-failed), then reaches fresh once the queue
// is seeded.
func driveProducerClassifiedRetry(
	t *testing.T, h *scenario.Harness, sub *stubstore.Store,
	templateName, contextKey string, errorTypes map[string]node.ErrorTypePolicy,
) {
	t.Helper()
	h.Stub.WhenType("worker").Success(map[string]any{"ok": 1}, true, "ran")

	// @constraint: Registration must SUCCEED (DeployTemplate fails the test on any
	// non-2xx registration response) AND — the falsifiable half, since
	// error_types: keys never hard-reject under the advisory-warning
	// semantics — the response's validation_warnings must carry no
	// finding naming the producer-declared class: a warning here would
	// mean the handshake → discovery-cache → validator plumbing failed
	// to surface the producer vocabulary the runtime routes.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: templateName, Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:       "worker",
					Executor:   "stub",
					ErrorTypes: errorTypes,
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	for _, w := range h.LastDeployWarnings {
		require.NotContains(t, w, producerClassUnavailable,
			"registration must not warn about the producer-declared class %q — "+
				"a warning means the producer vocabulary never reached the validator", producerClassUnavailable)
	}
	iid := h.CreateInstance(tid, contextKey, map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// @constraint: The configured retry action must FIRE against the
	// producer-classified failure: the run-disposition signal
	// transient/retry/<attempt>/<class> is keyed on the PRODUCER class
	// (pg/claim_unavailable) in both template shapes — the acquire/*
	// fallback affects policy lookup only, never the emitted class.
	require.True(t,
		h.WaitForEventKind(worker.ID, "transient/retry/1/"+producerClassUnavailable, 30*time.Second),
		"retry action must emit transient/retry/1/%s on the event log", producerClassUnavailable)

	// @deliberate: The retry chain holds the node — it must NOT have failed fast
	// (failed here would mean the policy did not match and the
	// unknown-class give_up default fired).
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotNil(t, wRow)
	require.NotEqual(t, cascade.NodeStateFailed, wRow.State,
		"retry policy must hold the node; failed means the producer class did not route")

	// @deliberate: Seed an item — a subsequent retry acquires it and the node runs
	// to fresh, proving the retries are live dispatch attempts.
	_, err := sub.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":1}`))
	require.NoError(t, err, "seed item")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh after seeding the queue under the retry chain")
}

// TestProducerClassRouting_ExactMatch — template A: error_types keys
// the exact producer-declared class. Registration succeeds (the class
// is in the producer's declared vocabulary, surfaced to the validator
// through the capabilities handshake) and the runtime routes the
// classified failure via the exact-key match.
func TestProducerClassRouting_ExactMatch(t *testing.T) {
	t.Parallel()
	h, sub := startClassifyingProducer(t)
	driveProducerClassifiedRetry(t, h, sub,
		"producer-class-exact", "ck-producer-class-exact",
		map[string]node.ErrorTypePolicy{
			producerClassUnavailable: {
				Policy: []node.PolicyAction{
					// @deliberate: Generous retry budget so the test has time to seed
					// the queue before chain exhaustion would fail the node.
					{Action: "retry", Count: 1000, BaseDelayMs: 100},
					{Action: "give_up"},
				},
			},
		})
}

// TestProducerClassRouting_PrefixFallback — template B: error_types
// declares ONLY the synthetic acquire/unavailable family. The
// producer-classified failure (pg/claim_unavailable) still matches via
// the documented acquire/* prefix fallback at policy lookup.
func TestProducerClassRouting_PrefixFallback(t *testing.T) {
	t.Parallel()
	h, sub := startClassifyingProducer(t)
	driveProducerClassifiedRetry(t, h, sub,
		"producer-class-fallback", "ck-producer-class-fallback",
		map[string]node.ErrorTypePolicy{
			"acquire/unavailable": {
				Policy: []node.PolicyAction{
					{Action: "retry", Count: 1000, BaseDelayMs: 100},
					{Action: "give_up"},
				},
			},
		})
}
