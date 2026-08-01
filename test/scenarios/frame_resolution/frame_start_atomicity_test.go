// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package frame_resolution

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFrameStartAtomicity(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "frame-start-atomicity", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomicity", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE frame_id IN (SELECT frame_id FROM rimsky_frames WHERE instance_id = $1)`, uuid.UUID(iid))
	h.ExecSQL(`DELETE FROM rimsky_frames WHERE instance_id = $1`, uuid.UUID(iid))

	messageID := uuid.New()
	h.ExecSQL(`INSERT INTO rimsky_messages
	    (id, instance_id, type, sender, sender_kind)
	    VALUES ($1, $2, 'fixture/frame-start-atomicity', 'operator', 'operator')`,
		messageID, uuid.UUID(iid))

	var wg sync.WaitGroup
	const N = 4
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = frame.RunTick(h.Ctx, h.Driver.Tables(), h.Driver.Queue(), slog.Default(), nil, nil)
		}()
	}
	wg.Wait()

	require.Equal(t, 1, countFramesByState(t, h, iid, "running"),
		"exactly one frame should have won the concurrent atomic frame-start insert race")

	var state string
	var startedAt *time.Time
	var triggeringMessageID uuid.UUID
	h.QueryRowSQL(
		`SELECT CASE WHEN f.ended_at IS NULL THEN 'running' WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed' ELSE 'completed' END,
		        f.started_at, f.triggering_message_id
		   FROM rimsky_frames f WHERE f.instance_id = $1`,
		[]any{uuid.UUID(iid)}, &state, &startedAt, &triggeringMessageID)
	require.Equal(t, "running", state)
	require.NotNil(t, startedAt, "running frame must have started_at set atomically")
	require.Equal(t, messageID, triggeringMessageID,
		"the winning frame must be anchored to the genuinely-pending message that both racers contended over")
}
