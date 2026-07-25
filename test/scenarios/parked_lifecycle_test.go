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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestParkedLifecycleResumeOnDeadline(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("worker").Park(resumeAt)

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

	h.WaitForEventCount(worker.ID, "transient/park", 1)
	h.WaitForLeafRunLineageCount(worker.ID, 1)

	var parkLeafRunCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run' AND record->>'node_id' = $1
		   AND record->>'settling_signal_type' = 'transient/park'`,
		[]any{worker.ID.String()},
		&parkLeafRunCount,
	)
	require.GreaterOrEqual(t, parkLeafRunCount, 1,
		"a leaf-run lineage row with settling_signal_type=transient/park must exist for the parked run")

	h.WaitForEventCount(worker.ID, "parked_resume_started", 1)
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
		Park(resumeAt).
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
	h.WaitForEventCount(worker.ID, "transient/park", 1)

	row := lastEventPayload(t, h, worker.ID, "transient/park")
	rawTags, ok := row["tags"].([]any)
	require.True(t, ok, "park audit event payload must carry a tags array; got %+v", row)
	gotTags := make([]string, 0, len(rawTags))
	for _, v := range rawTags {
		gotTags = append(gotTags, fmt.Sprint(v))
	}
	require.ElementsMatch(t, []string{"park-tag-a", "park-tag-b"}, gotTags,
		"park audit event tags must match the executor-emitted Park.tags")
}

func TestParkWithoutResumeAtRejectedAsProtocolViolation(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Park(time.Time{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-missing-resume-at", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-missing-resume", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForEventCount(worker.ID, "terminal/error/executor_protocol_violation", 1)
	row := lastEventPayload(t, h, worker.ID, "terminal/error/executor_protocol_violation")
	payload, _ := row["error_payload"].(map[string]any)
	require.Equal(t, "park_missing_resume_at", payload["reason"],
		"a Park without resume_at must be rejected as park_missing_resume_at; got %+v", row)
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
	h.Stub.WhenType("acquirer").Park(resumeAt)
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
	h.QueryRowSQL(
		`SELECT state FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{acq.ID},
		&phase,
	)
	require.Equal(t, "parked", phase, "node-run must be in parked state")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.is_held = TRUE`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 1, lhCount,
		"held claim_handle row must survive across the active → parked transition")

	h.WaitForEventCount(acq.ID, "parked_resume_started", 1)
	h.WaitForNodeState(acq.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(inh.ID, cascade.NodeStateFresh)

	h.WaitForAllRunsTerminal(acq.ID)
	h.WaitForAllRunsTerminal(inh.ID)

	var activeCount int
	for {
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
	var committedCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`, uuid.UUID(iid),
	).Scan(&committedCount))
	require.Greater(t, committedCount, 0,
		"at least one claim_handle row must be state=committed after auto-terminal Commit")
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
