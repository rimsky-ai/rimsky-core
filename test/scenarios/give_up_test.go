// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "retry-give-up", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				MaxRetries: node.IntPtr(2),
				RetryBackoff: &node.RetryBackoffConfig{
					Kind:        shared.BackoffExponential,
					BaseDelayMs: 50,
					MaxDelayMs:  200,
				},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-giveup", map[string]any{})

	n := h.FindNode(iid, "flaky")
	require.NotNil(t, n)

	if !h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 30*time.Second) {
		var runs *string
		h.QueryRowSQL(`SELECT string_agg(state || ':seq=' || sequence::text || ':reason=' || creation_reason || ':claim=' || COALESCE(claimed_by,''), E'\n' ORDER BY sequence) FROM rimsky_node_runs WHERE node_id = $1`, []any{n.ID}, &runs)
		var events *string
		h.QueryRowSQL(`SELECT string_agg(kind || ':' || COALESCE(payload::text,''), E'\n' ORDER BY id) FROM rimsky_events WHERE node_id = $1`, []any{n.ID}, &events)
		safe := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		t.Fatalf("flaky not failed; runs:\n%s\nevents:\n%s", safe(runs), safe(events))
	}
}
