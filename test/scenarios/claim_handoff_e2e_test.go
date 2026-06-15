// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-claim-handoff scenario proof.
//
// Five subcases pinning the spec's claim-handoff acceptance:
//
//	A — Regression close: a co-holder declaring `holds:` against an
//	    upstream acquirer reads `{{claim.<alias>.address}}` at dispatch
//	    and that substitution resolves to the held claim's actual bytes.
//	    The holding subgraph then Commits.
//	B — Per-field substitution kinds: `.address`, `.payload.<f>`, and
//	    `.claim_scope` each resolve to the held claim's corresponding
//	    persisted field.
//	C — Abandon path: a co-holder forced to settle terminal/error/<class>
//	    via error_types: give_up causes the held claim to transition to
//	    state=abandoned.
//	D — Multi-co-holder Commit: with TWO co-holders both declaring
//	    `holds:` and both reading `{{claim.<alias>.address}}`, the
//	    claim handle stays state=active across the first holder's
//	    settlement and only transitions to state=committed after the
//	    slower one also settles. Auto-terminal atomicity property.
//	E — Wire-payload parity: byte-equality between the acquirer's
//	    claim_handle.Address column and the bytes the co-holder
//	    consumes via the {{claim.<alias>.address}} substitution.
//
// Together these subcases close the GH-issue-#16 regression and pin
// `concept:claim-co-holdership` invariant "At dispatch, the co-holder's
// execution request carries the co-held claim's address (the same
// acquired result the original acquirer received)."
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestClaimHandoff_E2E table-drives the five STORY-claim-handoff
// subcases. Each subcase deploys a fresh template, drives the holding
// subgraph to terminal, and asserts the relevant property.
//
// Implemented as separate t.Run subtests rather than one big harness
// so each can boot its own stub claim producer with the right
// pick-policy seed (some subcases need a payload, others don't; the
// Abandon variant needs a co-holder that errors deliberately).
//
// @story: claim-handoff
// @concept: claim-co-holdership
func TestClaimHandoff_E2E(t *testing.T) {
	t.Parallel()

	t.Run("A_RegressionClose", testClaimHandoffRegressionClose)
	t.Run("B_PerFieldSubstitution", testClaimHandoffPerFieldSubstitution)
	t.Run("C_AbandonPath", testClaimHandoffAbandonPath)
	t.Run("D_MultiCoHolderCommit", testClaimHandoffMultiCoHolderCommit)
	t.Run("E_WirePayloadParity", testClaimHandoffWirePayloadParity)
}

// testClaimHandoffRegressionClose covers the GH-issue-#16 regression:
// a co-holder reading `{{claim.<alias>.address}}` dispatches with the
// substitution resolved to the held claim's bytes; the holding
// subgraph then Commits.
func testClaimHandoffRegressionClose(t *testing.T) {

	h, acquirer, coHolder := startHandoffHarness(t, handoffOpts{
		alias:        "schema",
		coHolderType: "co-holder",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr": map[string]any{
					"type":   "string",
					"source": "{{claim.schema.address}}",
				},
			},
			"required": []any{"held_addr"},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh (substitution must resolve)")

	// @constraint: Co-holder's substituted attributes should carry the held address.
	// The Falsifier names "the co-holder dispatches but receives
	// substituted bytes that don't equal the acquirer's bytes" as a
	// failure mode the proof must close — non-emptiness alone is not
	// enough. Read the acquirer's persisted claim_handle.Address and
	// compare byte-for-byte (after the stringifyRaw unwrap that the
	// `address` directive applies on the way out — matches subcase E's
	// shape).
	coHolderRunID := latestRunIDForNode(t, h, coHolder.ID)
	got := readSubstitutedAttribute(t, h, coHolderRunID, "held_addr")
	require.NotEmpty(t, got,
		"co-holder's substituted held_addr must not be empty (dispatch wired the claim)")
	handle := readSingleClaimHandle(t, h, acquirer.ID)
	require.Equal(t, unwrapJSONString(handle.Address), got,
		"co-holder's substituted held_addr must equal the acquirer's persisted claim_handle.Address bytes")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)
}

// testClaimHandoffPerFieldSubstitution exercises the three substitution
// shapes — `.address`, `.payload.<f>`, and `.claim_scope` — each
// resolving to the corresponding persisted field on the held claim.
func testClaimHandoffPerFieldSubstitution(t *testing.T) {

	// @deliberate: Pick policy seeds a payload with `region` so that
	// {{claim.schema.payload.region}} resolves to "us-east-1".
	itemPayload := json.RawMessage(`{"region":"us-east-1"}`)

	h, acquirer, coHolder := startHandoffHarness(t, handoffOpts{
		alias:        "schema",
		coHolderType: "co-holder",
		pickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit:     action.Action{Kind: action.Pop},
				OnGiveUp:     action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{itemPayload},
			},
		},
		acquirerSelector: "@queue",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr":   map[string]any{"type": "string", "source": "{{claim.schema.address}}"},
				"held_region": map[string]any{"type": "string", "source": "{{claim.schema.payload.region}}"},
				"held_scope":  map[string]any{"type": "string", "source": "{{claim.schema.claim_scope}}"},
			},
			"required": []any{"held_addr", "held_region", "held_scope"},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh (Open returned a queue item)")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh (all three substitutions must resolve)")

	coHolderRunID := latestRunIDForNode(t, h, coHolder.ID)

	// @deliberate: Read the acquirer's claim_handle row so we can compare per-field.
	handle := readSingleClaimHandle(t, h, acquirer.ID)

	gotAddr := readSubstitutedAttribute(t, h, coHolderRunID, "held_addr")
	gotRegion := readSubstitutedAttribute(t, h, coHolderRunID, "held_region")
	gotScope := readSubstitutedAttribute(t, h, coHolderRunID, "held_scope")

	// @deliberate: `address` and `claim_scope` flow through stringifyRaw (the
	// sanctioned shape-flattener for top-level address/claim-scope
	// directives) which unwraps a JSON-string into its plain string
	// form. The acquirer's Address column carries the JSON-encoded
	// bytes (`"stub-@queue-1"`); after unwrap both sides agree on the
	// bare string.
	require.Equal(t, unwrapJSONString(handle.Address), gotAddr,
		"co-holder's held_addr should equal the held claim's Address")
	require.Equal(t, unwrapJSONString(handle.ClaimScopeData), gotScope,
		"co-holder's held_scope should equal the held claim's ClaimScope")

	// @deliberate: `payload.<f>` is walked via walkPath, so a string leaf is
	// returned verbatim (no JSON re-quoting).
	require.Equal(t, "us-east-1", gotRegion,
		"co-holder's held_region should equal the seeded payload's region field")
}

// testClaimHandoffAbandonPath: co-holder forced to terminal/error/give_up
// causes the held claim to transition to state=abandoned.
func testClaimHandoffAbandonPath(t *testing.T) {

	h, acquirer, coHolder := startHandoffHarness(t, handoffOpts{
		alias:        "schema",
		coHolderType: "co-holder",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr": map[string]any{
					"type":   "string",
					"source": "{{claim.schema.address}}",
				},
			},
			"required": []any{"held_addr"},
		},
		// @deliberate: Co-holder's stub script is overridden to emit Error("forced",
		// nil); error_types maps `stub/forced` (the canonical hier-
		// archical class) to `give_up`.
		coHolderError: "forced",
		coHolderErrorTypes: map[string]node.ErrorTypePolicy{
			"stub/forced": {
				Policy: []node.PolicyAction{{Action: "give_up"}},
			},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFailed, 30*time.Second),
		"co-holder should settle failed (give_up on stub/forced)")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateAbandoned, true)
}

// testClaimHandoffMultiCoHolderCommit: with two co-holders, the held
// claim stays state=active across the first holder's settlement and
// only transitions to state=committed once both settle. Auto-terminal
// atomicity property.
func testClaimHandoffMultiCoHolderCommit(t *testing.T) {

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("co-holder-fast").Success(map[string]any{}, true, "fast")
	// @constraint: The slow co-holder delays its terminal long enough that the fast
	// co-holder's settlement is observable while the slow one is still
	// active. The held claim must NOT transition to committed during
	// this gap.
	h.Stub.WhenType("co-holder-slow").Delay(2*time.Second).Success(map[string]any{}, true, "slow")

	holdAttrs := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"held_addr": map[string]any{"type": "string", "source": "{{claim.schema.address}}"},
		},
		"required": []any{"held_addr"},
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-multi", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/multi-region", "rw", "schema")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "co-holder-fast",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}),
				scenario.WithAttributes(holdAttrs),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "co-holder-slow",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}),
				scenario.WithAttributes(holdAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-claim-handoff-multi", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	fast := h.FindNode(iid, "co-holder-fast")
	slow := h.FindNode(iid, "co-holder-slow")
	require.NotNil(t, acquirer)
	require.NotNil(t, fast)
	require.NotNil(t, slow)

	// @deliberate: Drive the fast co-holder to fresh. The slow one is still in-flight
	// because of its 2s Delay.
	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")
	require.True(t, h.WaitForNodeState(fast.ID, cascade.NodeStateFresh, 30*time.Second),
		"fast co-holder should settle fresh first")

	// @constraint: While slow is still running, the held claim handle must remain
	// state=active. Auto-terminal atomicity: Commit only fires when
	// EVERY holder is non-active.
	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateActive, true)

	require.True(t, h.WaitForNodeState(slow.ID, cascade.NodeStateFresh, 30*time.Second),
		"slow co-holder should eventually settle fresh")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)
}

// testClaimHandoffWirePayloadParity asserts byte-equality between the
// acquirer's claim_handle.Address column and the bytes the co-holder
// consumes via the {{claim.schema.address}} substitution.
//
// This is the "wire-payload parity" property of the spec: the bytes
// the co-holder receives must equal the bytes the acquirer received.
// Read both inside one transaction so the two reads are coherent.
func testClaimHandoffWirePayloadParity(t *testing.T) {

	h, acquirer, coHolder := startHandoffHarness(t, handoffOpts{
		alias:        "schema",
		coHolderType: "co-holder",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr": map[string]any{"type": "string", "source": "{{claim.schema.address}}"},
			},
			"required": []any{"held_addr"},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh")

	coHolderRunID := latestRunIDForNode(t, h, coHolder.ID)

	// @deliberate: Read both the acquirer's claim_handle row and the co-holder's
	// substituted attribute row inside one transaction so we compare
	// the bytes as the persistence layer sees them.
	var acquirerAddr json.RawMessage
	var substituted any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirer.ID, tx)
		if err != nil {
			return err
		}
		require.Len(t, rows, 1, "exactly one claim_handle row should belong to the acquirer")
		handle, err := h.Persist.ClaimHandles().Get(h.Ctx, rows[0].ID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, handle, "claim_handle row must be readable through Get")
		acquirerAddr = handle.Address

		attrs, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, coHolderRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, attrs, "co-holder's NodeAttributes row must exist after dispatch")
		substituted = attrs.Data["held_addr"]
		return nil
	}))

	// @deliberate: The substitution engine emits the address as a Go string after
	// stringifyRaw unwraps the JSON-encoded RawMessage. Re-encode the
	// substituted value as a JSON string and compare byte-for-byte
	// with the claim_handle.Address column — wire-payload parity.
	gotStr, ok := substituted.(string)
	require.True(t, ok, "held_addr should land as a string after substitution; got %T", substituted)
	gotEncoded, err := json.Marshal(gotStr)
	require.NoError(t, err)
	require.Equal(t, []byte(acquirerAddr), gotEncoded,
		"co-holder's substituted address bytes must equal the acquirer's claim_handle.Address bytes")
}

type handoffOpts struct {
	alias              string
	coHolderType       string
	coHolderAttrs      map[string]any
	coHolderError      string // @deliberate: when set, co-holder script emits Error(<class>, nil) instead of Success
	coHolderErrorTypes map[string]node.ErrorTypePolicy
	pickPolicies       map[string]stubstore.PickPolicyConfig
	acquirerSelector   string // @deliberate: defaults to "/region-handoff" when no pick policies are configured
}

// startHandoffHarness boots a two-node holding-subgraph template
// (acquirer + co-holder) against a fresh stub claim producer and
// returns the harness, the acquirer node row, and the co-holder node
// row. The co-holder declares `holds:` against the acquirer's alias
// and reads the attributes built by `opts.coHolderAttrs`.
//
// Property protected: tests use `terminal/success` (not
// `terminal/*`) as the cascade type-path so the Abandon-path subcase
// can deliberately starve the co-holder of an in-tree dispatch when
// the acquirer errors. Both of the in-tree co-holder paths here
// (Success / Error) follow the acquirer's `terminal/success`, so the
// type-path filter is consistent across subcases.
func startHandoffHarness(t *testing.T, opts handoffOpts) (*scenario.Harness, *persistence.NodeRow, *persistence.NodeRow) {
	t.Helper()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: opts.pickPolicies,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	if opts.coHolderError != "" {
		h.Stub.WhenType(opts.coHolderType).Error(opts.coHolderError, nil)
	} else {
		h.Stub.WhenType(opts.coHolderType).Success(map[string]any{}, true, "co-held")
	}

	acquirerSelector := opts.acquirerSelector
	if acquirerSelector == "" {
		acquirerSelector = "/region-handoff"
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-" + opts.coHolderType, Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", acquirerSelector, "rw", opts.alias)),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     opts.coHolderType,
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						opts.alias: {From: "acquirer"},
					},
					ErrorTypes: opts.coHolderErrorTypes,
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}),
				scenario.WithAttributes(opts.coHolderAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-claim-handoff-"+opts.coHolderType, map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coHolder := h.FindNode(iid, opts.coHolderType)
	require.NotNil(t, acquirer)
	require.NotNil(t, coHolder)

	return h, acquirer, coHolder
}

// latestRunIDForNode returns the most-recent rimsky_node_runs.id for
// the given node. Used to look up the NodeAttributes row keyed on the
// dispatch the co-holder actually ran in.
func latestRunIDForNode(t *testing.T, h *scenario.Harness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var runID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
		uuid.UUID(nodeID),
	).Scan(&runID))
	return shared.UUID(runID)
}

// readSubstitutedAttribute returns the substituted value of the named
// attribute key on the given run's NodeAttributes row.
func readSubstitutedAttribute(t *testing.T, h *scenario.Harness, runID shared.UUID, key string) string {
	t.Helper()
	var out string
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		row, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, runID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row, "NodeAttributes row must exist for run %s", runID)
		raw, ok := row.Data[key]
		require.True(t, ok, "attribute key %q must be present in resolved data; have %v", key, row.Data)
		s, ok := raw.(string)
		require.True(t, ok, "attribute %q should be a string after substitution; got %T", key, raw)
		out = s
		return nil
	}))
	return out
}

// readSingleClaimHandle returns the single claim_handle row whose
// holder_node_id equals nodeID. Fails when zero or more than one row
// is present.
func readSingleClaimHandle(t *testing.T, h *scenario.Harness, nodeID shared.UUID) *persistence.ClaimHandleRow {
	t.Helper()
	var out *persistence.ClaimHandleRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, nodeID, tx)
		if err != nil {
			return err
		}
		require.Len(t, rows, 1, "exactly one claim_handle row should belong to %s", nodeID)
		got, err := h.Persist.ClaimHandles().Get(h.Ctx, rows[0].ID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, got)
		out = got
		return nil
	}))
	return out
}

// requireClaimHandleState reads the single claim_handle for the
// acquirer and asserts both state and is_held. Used by every subcase
// that has an opinion about where the held claim ends up.
func requireClaimHandleState(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID, want spec.ClaimHandleState, wantHeld bool) {
	t.Helper()
	// @deliberate: Auto-terminal Promote fires asynchronously after the holding
	// subgraph completes (one supervisor tick after the last holder
	// settles). Poll the row's state until it matches the expectation
	// or we time out — this absorbs the in-tx Promote latency without
	// flaking when CI is slow.
	deadline := time.Now().Add(30 * time.Second)
	var lastState spec.ClaimHandleState
	var lastHeld bool
	for time.Now().Before(deadline) {
		row := readSingleClaimHandle(t, h, acquirerNodeID)
		lastState = row.State
		lastHeld = row.IsHeld
		if row.State == want && row.IsHeld == wantHeld {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Failf(t, "claim_handle did not reach expected state",
		"want state=%s is_held=%v; last seen state=%s is_held=%v",
		want, wantHeld, lastState, lastHeld)
}

// unwrapJSONString mirrors the substitution engine's `stringifyRaw`:
// if `raw` JSON-decodes as a string, return the unwrapped value;
// otherwise return the bytes verbatim as a string. Used by the
// per-field subcase to compare an acquirer's claim_handle column
// (JSON-encoded bytes) against the co-holder's substituted attribute
// value (already-unwrapped string).
func unwrapJSONString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
