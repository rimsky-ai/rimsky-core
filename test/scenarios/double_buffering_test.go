// Scenario 7 — double-buffering: first commit succeeds, second commit's
// payload fails a quality rule; the quality_rule_failed policy routes to
// give_up. After failure, the resource's current_version still points to
// the previously-successful version (double-buffer invariant).
package scenarios

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/resource/inlinejsonb"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/supervisor"
)

// failAfterFirstCommit fails on the second evaluation — lets the first
// commit land, then rejects subsequent commits with a quality_rule_failed.
type failAfterFirstCommit struct{ calls atomic.Int32 }

func (r *failAfterFirstCommit) Evaluate(_ context.Context, _ qualityrule.EvalInput) (bool, string, error) {
	n := r.calls.Add(1)
	if n == 1 {
		return true, "", nil
	}
	return false, "second run rejected", nil
}

func init() {
	qualityrule.Register("scenario7_fail_after_first", &failAfterFirstCommit{})
}

func TestDoubleBuffering(t *testing.T) {
	t.Parallel()
	// Start harness without a supervisor so we can wire our own GetResource
	// closure that attaches custom quality rules.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	// Pre-register fresh rule for this test-run. Each call to the evaluator
	// shares state, so keep rule type unique per test run.
	rule := &failAfterFirstCommit{}
	ruleName := "scenario7_inline_rule"
	qualityrule.Register(ruleName, rule)

	// Two-phase Complete: first changed=true, then changed=true with
	// a different payload — both succeed at the executor layer, but the
	// rule flips the second to rejected at the resource layer.
	h.Stub.WhenType("producer").Complete(map[string]any{"rows": []any{"a"}}, true, "first")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "double-buffer", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type: "producer", Executor: "stub",
				OwnsResources: []node.ResourceDef{
					{Path: []string{"db", "{consumer_key}"}, Implementation: "inline-jsonb"},
				},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"quality_rule_failed": {
						Policy: []node.PolicyAction{
							{Action: "give_up"},
						},
					},
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-db", map[string]any{})

	// Custom GetResource that injects quality rules into the Resource.
	getResource := func(ctx context.Context, rid shared.UUID) (resource.Resource, error) {
		row, err := h.Storage.Resources().Get(ctx, rid, nil)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, nil
		}
		fac := inlinejsonb.Factory{StorageRegistry: h.Storage.Resources()}
		cfg := resource.Config{
			"_resource_id":   rid.String(),
			"_path":          row.ResourcePath,
			"_owner_node_id": row.OwnerNodeID.String(),
			"keep_versions":  row.KeepVersions,
		}
		rules := []qualityrule.Spec{{
			Type:     ruleName,
			Severity: shared.SeverityError,
		}}
		return fac.Create(cfg, rules, nil)
	}

	// Start a supervisor wired to the stub executor + our custom GetResource.
	sv, err := supervisor.Start(supervisor.Config{
		SupervisorID:      "scenario7-sup",
		Storage:           h.Storage,
		Queue:             h.Queue,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		Concurrency:       2,
		HeartbeatInterval: 200 * time.Millisecond,
		ClaimPollInterval: 50 * time.Millisecond,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		GetResource:  getResource,
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sv.Shutdown(ctx)
	})

	producer := h.FindNode(iid, "producer")
	require.NotNil(t, producer)

	// First run → fresh with a committed version.
	require.True(t, h.WaitForNodeState(producer.ID, shared.NodeStateFresh, 20*time.Second),
		"producer did not reach fresh on first run")

	resources, err := h.Storage.Resources().ListByOwner(h.Ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	firstResourceID := resources[0].ID
	firstVersionID := resources[0].CurrentVersionID
	require.NotNil(t, firstVersionID, "first run must have committed a version")

	// Swap stub to deliver a new result + invalidate to trigger a re-run.
	h.Stub.WhenType("producer").Complete(map[string]any{"rows": []any{"b", "c"}}, true, "second")
	require.NoError(t, h.Storage.Nodes().UpdateState(h.Ctx, producer.ID,
		shared.NodeStateStale, node.ReasonOperatorInvalidate, nil))

	// Expect producer to move to failed (quality rule rejects → give_up).
	require.True(t, h.WaitForNodeState(producer.ID, shared.NodeStateFailed, 30*time.Second),
		"producer did not reach failed on second run")

	// The resource's current_version should be UNCHANGED — the rejected
	// commit must not replace the live data (double-buffer invariant).
	after, err := h.Storage.Resources().Get(h.Ctx, firstResourceID, nil)
	require.NoError(t, err)
	require.NotNil(t, after.CurrentVersionID)
	require.Equal(t, *firstVersionID, *after.CurrentVersionID,
		"current_version should still point at the successful first commit")
}
