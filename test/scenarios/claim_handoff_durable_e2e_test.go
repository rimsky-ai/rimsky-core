// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: claim-handoff-durable
// @concept: claim-lifetime
// @concept: claim-handle
func TestClaimHandoff_Durable(t *testing.T) {
	t.Parallel()

	t.Run("A_CrossDispatchPersistence", testClaimHandoffDurable_CrossDispatchPersistence)
	t.Run("B_CrossDispatchHolds", testClaimHandoffDurable_CrossDispatchHolds)
	t.Run("C_ConflictDetectionIncludesCommittedDurable", testClaimHandoffDurable_ConflictDetection)
	t.Run("D_AssetReleasePath", testClaimHandoffDurable_AssetRelease)
	t.Run("E_InstanceTerminationRelease", testClaimHandoffDurable_InstanceTerminationRelease)
}

func testClaimHandoffDurable_CrossDispatchPersistence(t *testing.T) {

	h, acquirer, _ := startDurableHarness(t, durableOpts{
		instanceKey: "ck-claim-handoff-durable-A",
		selector:    "/durable-A",
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh in D1")

	requireDurableCommittedHandle(t, h, acquirer.ID)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(h.Ctx, h.Persist.ClaimHandles(), cfg,
		time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n,
		"retention sweep MUST NOT reap a committed-durable row")

	cfgAggressive := runtime.RetentionConfig{ClaimHandlesTrailing: 1 * time.Nanosecond}
	n2, err := runtime.SweepClaimHandleRetention(h.Ctx, h.Persist.ClaimHandles(), cfgAggressive,
		time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n2,
		"aggressive retention sweep MUST NOT reap a committed-durable row")

	row := readDurableClaimHandle(t, h, acquirer.ID)
	require.NotNil(t, row, "durable claim_handle row must survive the sweep")
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State)
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime)
}

func testClaimHandoffDurable_CrossDispatchHolds(t *testing.T) {

	h, acquirer, coHolder := startDurableHandoffHarness(t, durableHandoffOpts{
		instanceKey:  "ck-claim-handoff-durable-B",
		selector:     "/durable-B",
		alias:        "asset",
		coHolderType: "co-holder",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr": map[string]any{"type": "string", "source": "{{claim.asset.address}}"},
			},
			"required": []any{"held_addr"},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh in D1")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh in D1 (substitution resolves on the live held claim)")

	requireDurableCommittedHandle(t, h, acquirer.ID)

	d1Handle := readDurableClaimHandle(t, h, acquirer.ID)
	require.NotNil(t, d1Handle)
	d1Address := append(json.RawMessage(nil), d1Handle.Address...)

	d1RunID := latestRunIDForNode(t, h, coHolder.ID)

	h.PostInstanceMessage(coHolder.InstanceID,
		"test/wake/"+coHolder.NodeType, nil,
		fmt.Sprintf("test-wake-%s-1", t.Name()))

	requireSecondRun(t, h, coHolder.ID, d1RunID, 30*time.Second)
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh again on D2 (no terminal/error/template_resolution_failed)")

	d2RunID := latestRunIDForNode(t, h, coHolder.ID)
	require.NotEqual(t, d1RunID, d2RunID,
		"D2 run id must differ from D1 — the invalidate must have produced a fresh run")
	requireSubstitutedAddrEquals(t, h, d2RunID, "held_addr", d1Address,
		"D2 co-holder's substituted {{claim.asset.address}} bytes must equal D1 persisted durable Address")
}

func testClaimHandoffDurable_ConflictDetection(t *testing.T) {

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
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("durable-acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType("competing-acquirer").Success(map[string]any{}, true, "should-not-run")

	const sharedSelector = "/durable-C-shared"

	durableTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-C-owner", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "durable-acquirer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	durableIID := h.CreateInstance(durableTid, "ck-claim-handoff-durable-C-owner", map[string]any{})
	durableAcq := h.FindNode(durableIID, "durable-acquirer")
	require.NotNil(t, durableAcq)
	require.True(t, h.WaitForNodeState(durableAcq.ID, cascade.NodeStateFresh, 30*time.Second),
		"durable acquirer should settle fresh and Promote its claim to committed-durable")
	requireDurableCommittedHandle(t, h, durableAcq.ID)

	competingTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-C-competitor", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "competing-acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "give_up",
						},
					},
				},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
				}),
			),
		},
	})
	competingIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-C-competitor", map[string]any{})
	competingAcq := h.FindNode(competingIID, "competing-acquirer")
	require.NotNil(t, competingAcq)

	if !h.WaitForNodeState(competingAcq.ID, cascade.NodeStateFailed, 30*time.Second) {
		var compLatest *persistence.NodeRunLatest
		var owners []persistence.ClaimHandleRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, competingAcq.ID)
			if err != nil {
				return err
			}
			compLatest = r
			rs, err := h.Persist.ClaimHandles().ListByProducerClaimScope(h.Ctx, "queue-store", tx)
			if err != nil {
				return err
			}
			owners = rs
			return nil
		}))
		owSummary := make([]string, len(owners))
		for i, ow := range owners {
			owSummary[i] = string(ow.State) + "/" + string(ow.Lifetime) +
				" scope=" + string(ow.ClaimScopeData)
		}
		var stateStr, sigStr string
		if compLatest != nil {
			stateStr = string(compLatest.State)
			if compLatest.SettlingSignalType != nil {
				sigStr = *compLatest.SettlingSignalType
			}
		}
		t.Fatalf("competing acquirer must settle failed via acquire/unavailable while durable row occupies the scope; "+
			"got state=%s settling_signal_type=%v; conflict-set holders=%v",
			stateStr, sigStr, owSummary)
	}

	requireSettlingSignalTypePrefix(t, h, competingAcq.ID, "terminal/error/acquire/unavailable")

	for _, obs := range h.Stub.Observed() {
		require.NotEqual(t, "competing-acquirer", obs.NodeType,
			"competing-acquirer must not be dispatched to the executor when acquire/unavailable fires")
	}
}

func testClaimHandoffDurable_AssetRelease(t *testing.T) {

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
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("durable-acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType("competing-acquirer").Success(map[string]any{}, true, "won")

	const sharedSelector = "/durable-D-shared"

	durableTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-D-owner", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "durable-acquirer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	durableIID := h.CreateInstance(durableTid, "ck-claim-handoff-durable-D-owner", map[string]any{})
	durableAcq := h.FindNode(durableIID, "durable-acquirer")
	require.NotNil(t, durableAcq)
	require.True(t, h.WaitForNodeState(durableAcq.ID, cascade.NodeStateFresh, 30*time.Second))
	requireDurableCommittedHandle(t, h, durableAcq.ID)

	competingTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-D-competitor", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "competing-acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Action: "give_up",
						},
					},
				},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
				}),
			),
		},
	})
	competingIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-D-competitor", map[string]any{})
	competingAcq := h.FindNode(competingIID, "competing-acquirer")
	require.NotNil(t, competingAcq)

	require.True(t, h.WaitForNodeState(competingAcq.ID, cascade.NodeStateFailed, 30*time.Second),
		"competing acquirer must initially fail with acquire/unavailable")

	deleteAsset(t, h, durableIID, "durable-acquirer.asset")

	requireClaimHandleAbsent(t, h, durableAcq.ID)

	thirdIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-D-third", map[string]any{})
	thirdAcq := h.FindNode(thirdIID, "competing-acquirer")
	require.NotNil(t, thirdAcq)

	if !h.WaitForNodeState(thirdAcq.ID, cascade.NodeStateFresh, 30*time.Second) {
		var thirdLatest *persistence.NodeRunLatest
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, thirdAcq.ID)
			thirdLatest = r
			return err
		}))
		var stateStr, sigStr string
		if thirdLatest != nil {
			stateStr = string(thirdLatest.State)
			if thirdLatest.SettlingSignalType != nil {
				sigStr = *thirdLatest.SettlingSignalType
			}
		}
		t.Fatalf("fresh subsequent acquirer must settle fresh after the durable owner's asset is released; "+
			"got state=%s settling_signal_type=%v", stateStr, sigStr)
	}
}

func testClaimHandoffDurable_InstanceTerminationRelease(t *testing.T) {

	h, acquirer, stub := startDurableHarness(t, durableOpts{
		instanceKey: "ck-claim-handoff-durable-E",
		selector:    "/durable-E",
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh and Promote its durable claim")
	requireDurableCommittedHandle(t, h, acquirer.ID)

	durableHandleID := readDurableClaimHandle(t, h, acquirer.ID).ID
	durableClaimID := durableHandleID.String()
	instanceID := acquirer.InstanceID

	terminateInstance(t, h, instanceID)
	require.True(t, waitForInstanceTerminatedDurable(t, h, instanceID, 15*time.Second),
		"POST /terminate must set terminated_at on the instance row")

	deleteInstance(t, h, instanceID)

	requireClaimHandleGoneByID(t, h, durableHandleID)

	requireInstanceGone(t, h, instanceID)

	requireProducerRelease(t, stub, durableClaimID)
}

func requireProducerRelease(t *testing.T, stub *stubstore.Store, claimID string) {
	t.Helper()
	calls := stub.Calls()
	for _, c := range calls {
		if c.Verb == "release" && c.ClaimID == claimID {
			return
		}
	}
	t.Fatalf("expected producer Release for claim_id=%s; recorded calls=%v", claimID, calls)
}

type durableOpts struct {
	instanceKey string
	selector    string
}

func startDurableHarness(t *testing.T, opts durableOpts) (*scenario.Harness, *persistence.NodeRow, *stubstore.Store) {
	t.Helper()
	endpoint, stub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "owned")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-" + opts.instanceKey, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: opts.selector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, opts.instanceKey, map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	require.NotNil(t, acquirer)
	return h, acquirer, stub
}

type durableHandoffOpts struct {
	instanceKey   string
	selector      string
	alias         string
	coHolderType  string
	coHolderAttrs map[string]any
}

func startDurableHandoffHarness(t *testing.T, opts durableHandoffOpts) (*scenario.Harness, *persistence.NodeRow, *persistence.NodeRow) {
	t.Helper()
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
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType(opts.coHolderType).Success(map[string]any{}, true, "co-held")

	wakeType := "test/wake/" + opts.coHolderType
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-" + opts.coHolderType, Version: "1",
		Messages: []spec.MessageSchema{
			{Type: wakeType},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "queue-store",
					Selector: opts.selector,
					Intent:   "rw",
					Alias:    opts.alias,
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     opts.coHolderType,
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						opts.alias: {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
					node.SubscriptionEntry{Node: wakeType, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				),
				scenario.WithAttributes(opts.coHolderAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, opts.instanceKey, map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coHolder := h.FindNode(iid, opts.coHolderType)
	require.NotNil(t, acquirer)
	require.NotNil(t, coHolder)
	return h, acquirer, coHolder
}

func requireDurableCommittedHandle(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *persistence.ClaimHandleRow
	for time.Now().Before(deadline) {
		row := readDurableClaimHandle(t, h, acquirerNodeID)
		if row != nil {
			last = row
			if row.State == spec.ClaimHandleStateCommitted && row.Lifetime == spec.ClaimLifetimeDurable {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		require.Fail(t, "no claim_handle row found for acquirer")
		return
	}
	require.Failf(t, "claim_handle did not reach committed+durable",
		"last seen state=%s lifetime=%s", last.State, last.Lifetime)
}

func readDurableClaimHandle(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) *persistence.ClaimHandleRow {
	t.Helper()
	var out *persistence.ClaimHandleRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirerNodeID, tx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		got, err := h.Persist.ClaimHandles().Get(h.Ctx, rows[0].ID, tx)
		if err != nil {
			return err
		}
		out = got
		return nil
	}))
	return out
}

func requireClaimHandleAbsent(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) {
	t.Helper()
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirerNodeID, tx)
		if err != nil {
			return err
		}
		require.Empty(t, rows,
			"expected no claim_handle rows for the acquirer after asset Release; have %d", len(rows))
		return nil
	}))
}

func requireClaimHandleGoneByID(t *testing.T, h *scenario.Harness, id shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var got *persistence.ClaimHandleRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.ClaimHandles().Get(h.Ctx, id, tx)
			got = r
			return err
		}))
		if got == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Fail(t, "claim_handle row was not deleted after DELETE /v1/instances/{id}")
}

func requireInstanceGone(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		row, err := h.Persist.Instances().Get(h.Ctx, instanceID, tx)
		if err != nil {
			return err
		}
		require.Nil(t, row, "expected the instance row to be deleted after DELETE")
		return nil
	}))
}

func requireSecondRun(t *testing.T, h *scenario.Harness, nodeID, prevRunID shared.UUID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND id <> $2`,
			uuid.UUID(nodeID), uuid.UUID(prevRunID),
		).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Fail(t, "no second run row appeared for node after invalidate")
}

func requireSubstitutedAddrEquals(t *testing.T, h *scenario.Harness, runID shared.UUID, key string, wantAddress json.RawMessage, msg string) {
	t.Helper()
	var substituted any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		attrs, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, runID, tx)
		if err != nil {
			return err
		}
		require.NotNilf(t, attrs, "NodeAttributes row must exist for run %s", runID)
		substituted = attrs.Data[key]
		return nil
	}))
	gotStr, ok := substituted.(string)
	require.Truef(t, ok, "%s: substituted value should be a string; got %T", msg, substituted)
	gotEncoded, err := json.Marshal(gotStr)
	require.NoError(t, err)
	require.Equalf(t, []byte(wantAddress), gotEncoded, "%s", msg)
}

func requireSettlingSignalTypePrefix(t *testing.T, h *scenario.Harness, nodeID shared.UUID, prefix string) {
	t.Helper()
	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, nodeID)
		latest = r
		return err
	}))
	require.NotNil(t, latest, "node %s must have at least one run", nodeID)
	require.NotNil(t, latest.SettlingSignalType,
		"node %s must have a settling_signal_type after terminal", nodeID)
	require.True(t,
		len(*latest.SettlingSignalType) >= len(prefix) &&
			(*latest.SettlingSignalType)[:len(prefix)] == prefix,
		"node %s settling_signal_type=%q must start with %q",
		nodeID, *latest.SettlingSignalType, prefix)
}

func deleteAsset(t *testing.T, h *scenario.Harness, instanceID shared.UUID, assetAlias string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete,
		h.ControlBase+"/v1/instances/"+instanceID.String()+"/assets/"+assetAlias, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"DELETE asset must return 200: %s", string(body))
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["deleted"], "DELETE response must report deleted=true")
}

func terminateInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	resp, err := http.Post(h.ControlBase+"/v1/instances/"+instanceID.String()+"/terminate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"POST /terminate must return 200: %s", string(body))
}

func deleteInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete,
		h.ControlBase+"/v1/instances/"+instanceID.String(), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"DELETE /v1/instances must return 200: %s", string(body))
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["deleted"],
		"DELETE response must report deleted=true")
}

func waitForInstanceTerminatedDurable(t *testing.T, h *scenario.Harness, iid shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var terminatedAt *time.Time
		h.QueryRowSQL(
			`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
			[]any{iid}, &terminatedAt)
		if terminatedAt != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
