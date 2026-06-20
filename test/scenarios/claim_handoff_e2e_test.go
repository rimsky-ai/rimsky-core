// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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

	coHolderRunID := latestRunIDForNode(t, h, coHolder.ID)
	got := readSubstitutedAttribute(t, h, coHolderRunID, "held_addr")
	require.NotEmpty(t, got,
		"co-holder's substituted held_addr must not be empty (dispatch wired the claim)")
	handle := readSingleClaimHandle(t, h, acquirer.ID)
	require.Equal(t, unwrapJSONString(handle.Address), got,
		"co-holder's substituted held_addr must equal the acquirer's persisted claim_handle.Address bytes")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)
}

func testClaimHandoffPerFieldSubstitution(t *testing.T) {

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

	handle := readSingleClaimHandle(t, h, acquirer.ID)

	gotAddr := readSubstitutedAttribute(t, h, coHolderRunID, "held_addr")
	gotRegion := readSubstitutedAttribute(t, h, coHolderRunID, "held_region")
	gotScope := readSubstitutedAttribute(t, h, coHolderRunID, "held_scope")

	require.Equal(t, unwrapJSONString(handle.Address), gotAddr,
		"co-holder's held_addr should equal the held claim's Address")
	require.Equal(t, unwrapJSONString(handle.ClaimScopeData), gotScope,
		"co-holder's held_scope should equal the held claim's ClaimScope")

	require.Equal(t, "us-east-1", gotRegion,
		"co-holder's held_region should equal the seeded payload's region field")
}

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

func testClaimHandoffMultiCoHolderCommit(t *testing.T) {

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("co-holder-fast").Success(map[string]any{}, true, "fast")
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
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/multi-region", "rw", "schema")),
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

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")
	require.True(t, h.WaitForNodeState(fast.ID, cascade.NodeStateFresh, 30*time.Second),
		"fast co-holder should settle fresh first")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateActive, true)

	require.True(t, h.WaitForNodeState(slow.ID, cascade.NodeStateFresh, 30*time.Second),
		"slow co-holder should eventually settle fresh")

	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)
}

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
	coHolderError      string
	coHolderErrorTypes map[string]node.ErrorTypePolicy
	pickPolicies       map[string]stubstore.PickPolicyConfig
	acquirerSelector   string
}

func startHandoffHarness(t *testing.T, opts handoffOpts) (*scenario.Harness, *persistence.NodeRow, *persistence.NodeRow) {
	t.Helper()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: opts.pickPolicies,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
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
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", acquirerSelector, "rw", opts.alias)),
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

func latestRunIDForNode(t *testing.T, h *scenario.Harness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var runID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
		uuid.UUID(nodeID),
	).Scan(&runID))
	return shared.UUID(runID)
}

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

func requireClaimHandleState(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID, want spec.ClaimHandleState, wantHeld bool) {
	t.Helper()
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

func unwrapJSONString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
