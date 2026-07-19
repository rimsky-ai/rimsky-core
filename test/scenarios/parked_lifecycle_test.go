// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestParkedLifecycleResumeOnDeadline(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("worker").
		Park(genv1.ParkReason_PARK_REASON_SNOOZE, "rate_limit", resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-deadline", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-deadline", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "resumed")

	var phase string
	var resumeAtStored *time.Time
	h.QueryRowSQL(
		`SELECT state, resume_at FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{worker.ID},
		&phase, &resumeAtStored,
	)
	require.Equal(t, "parked", phase, "node-run should be in parked state")
	require.NotNil(t, resumeAtStored, "resume_at should be persisted")
	t.Logf("parked row: phase=%s resume_at=%v (now=%v, resume_at-now=%v)",
		phase, *resumeAtStored, time.Now(), time.Until(*resumeAtStored))

	h.WaitForEventKind(worker.ID, "transient/park/snooze")

	var parkSettlingSignal string
	h.QueryRowSQL(
		`SELECT record->>'settling_signal_type' FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run' AND record->>'node_id' = $1
		 ORDER BY observed_at DESC LIMIT 1`,
		[]any{worker.ID.String()},
		&parkSettlingSignal,
	)
	require.Equal(t, "transient/park/snooze", parkSettlingSignal,
		"park leaf-run lineage row should carry settling_signal_type=transient/park/snooze")

	h.WaitForEventKind(worker.ID, "parked_resume_started")
	row := lastEventPayload(t, h, worker.ID, "parked_resume_started")
	require.Equal(t, "deadline_elapsed", row["resume_reason"],
		"deadline-elapsed wake must persist resume_reason=deadline_elapsed; "+
			"got %v", row["resume_reason"])
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
}

func TestParkedLifecycle_TagsLandInAuditEvent(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("worker").
		Park(genv1.ParkReason_PARK_REASON_SNOOZE, "rate_limit", resumeAt).
		Tags("park-tag-a", "park-tag-b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-tags-audit", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-tags-audit", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)
	h.WaitForEventKind(worker.ID, "transient/park/snooze")

	row := lastEventPayload(t, h, worker.ID, "transient/park/snooze")
	rawTags, ok := row["tags"].([]any)
	require.True(t, ok, "park audit event payload must carry a tags array; got %+v", row)
	gotTags := make([]string, 0, len(rawTags))
	for _, v := range rawTags {
		gotTags = append(gotTags, fmt.Sprint(v))
	}
	require.ElementsMatch(t, []string{"park-tag-a", "park-tag-b"}, gotTags,
		"park audit event tags must match the executor-emitted Park.tags")
}

func TestParkedLifecycleMaxParkDurationOverrun(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "waiting", time.Time{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-overrun", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:            "worker",
				Executor:        "stub",
				MaxParkDuration: "1s",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-overrun", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)

	h.WaitForEventKind(worker.ID, "park_timeout")
	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)
}

func TestParkedLifecycleHeldClaimRetentionAcrossPark(t *testing.T) {
	t.Parallel()
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
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
	resumeAt := time.Now().Add(10 * time.Second)
	h.Stub.WhenType("acquirer").
		Park(genv1.ParkReason_PARK_REASON_SNOOZE, "checkpoint", resumeAt)
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "inheritor-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-held-retention", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/held-A", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-held", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	h.WaitForNodeState(acq.ID, cascade.NodeStateParked)

	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "resumed")

	var phase string
	var parkedReason *string
	h.QueryRowSQL(
		`SELECT state, parked_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{acq.ID},
		&phase, &parkedReason,
	)
	require.Equal(t, "parked", phase, "node-run must be in parked state")
	require.NotNil(t, parkedReason, "parked_reason must survive parked transition")
	require.Equal(t, "snooze", *parkedReason,
		"parked_reason should store the enum form (snake_case); TIME_WAIT collapsed to SNOOZE per the 2026-05-22 ParkReason collapse")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.is_held = TRUE`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 1, lhCount,
		"held claim_handle row must survive across the active → parked transition")

	h.WaitForEventKind(acq.ID, "parked_resume_started")
	h.WaitForNodeState(acq.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(inh.ID, cascade.NodeStateFresh)

	h.WaitForAllRunsTerminal(acq.ID)
	h.WaitForAllRunsTerminal(inh.ID)

	var activeCount int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1 AND lh.state = 'active'`, uuid.UUID(iid),
		).Scan(&activeCount))
		if activeCount == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, 0, activeCount,
		"no active claim_handle rows must remain after auto-terminal Commit")
	var committedCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`, uuid.UUID(iid),
	).Scan(&committedCount))
	require.Greater(t, committedCount, 0,
		"at least one claim_handle row must be state=committed after auto-terminal Commit")
}

func TestParkedLifecycleParkTimeoutAbandonsHeldClaim(t *testing.T) {
	t.Parallel()
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
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
	h.Stub.WhenType("acquirer").Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "waiting_held", time.Time{})
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-timeout-held", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:            "acquirer",
					Executor:        "stub",
					MaxParkDuration: "1s",
				},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/held-T", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-timeout-held", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	h.WaitForNodeState(acq.ID, cascade.NodeStateParked)

	h.WaitForEventKind(acq.ID, "park_timeout")
	h.WaitForNodeState(acq.ID, cascade.NodeStateFailed)
	h.WaitForAllRunsTerminal(acq.ID)

	var abandonedCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'abandoned'`, uuid.UUID(iid),
	).Scan(&abandonedCount))
	require.Greater(t, abandonedCount, 0,
		"at least one claim_handle row must be state=abandoned after auto-terminal Abandon")
	var activeCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'active'`, uuid.UUID(iid),
	).Scan(&activeCount))
	require.Equal(t, 0, activeCount,
		"no active claim_handle rows must remain after auto-terminal Abandon")

	abandonSeen := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range store.Calls() {
			if c.Verb == "abandon" {
				abandonSeen = true
				break
			}
		}
		if abandonSeen {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, abandonSeen,
		"producer Abandon verb must fire on park-timeout for held claim")
}

func lastEventPayload(t *testing.T, h *scenario.Harness, nodeID shared.UUID, kind string) map[string]any {
	t.Helper()
	var rawJSON []byte
	h.QueryRowSQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind = $2 ORDER BY occurred_at DESC LIMIT 1`,
		[]any{nodeID, kind},
		&rawJSON,
	)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rawJSON, &out))
	return out
}

var _ = persistence.NodeRow{}
