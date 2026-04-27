// §19.1 — heartbeat tick extends rimsky_lock_holders.expires_at for
// rows whose holder_node is currently `running` (spec §13.4).
//
// We bypass RunNode and call the §13.4 SQL directly via
// `Storage.LockHolders().ExtendHeartbeat`. The setup:
//   - register a supervisor row,
//   - manufacture a node assigned to that supervisor in `running`,
//   - insert a lock-holder row with a near-future expires_at.
//
// Then call ExtendHeartbeat with a much-later expires_at and assert the
// row's expires_at moved forward. As a negative control we insert a
// second lock-holder row whose holder node is `stale` (preserve-for-
// resume); ExtendHeartbeat must NOT refresh it (the §13.4 running-node
// filter is the invariant that lets the resume-grace cutoff fire).
package locks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/storage"
)

func TestLockHeartbeatExtendsExpiry(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	// One template node so we have a real rimsky_nodes row to anchor
	// the lock-holder rows.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "heartbeat", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "running-node", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "stale-node", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-heartbeat", map[string]any{})

	running := h.FindNode(iid, "running-node")
	stale := h.FindNode(iid, "stale-node")
	require.NotNil(t, running)
	require.NotNil(t, stale)

	supID := "heartbeat-sup"

	// Register the supervisor so the rimsky_supervisors row exists for
	// the assigned_supervisor_id FK referenced by the node update.
	require.NoError(t, h.Storage.Supervisors().Register(h.Ctx,
		storage.SupervisorRegisterInput{
			ID:                supID,
			AcceptedExecutors: []string{"stub"},
			Concurrency:       4,
			CallbackHost:      "127.0.0.1",
			CallbackPort:      0,
		}, nil))

	// Move the running-node row to running + assigned to supID.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes
		    SET state = 'running',
		        assigned_supervisor_id = $1,
		        last_heartbeat_at = NOW()
		  WHERE id = $2`,
		supID, running.ID,
	)
	require.NoError(t, err)
	// Leave stale-node in stale (default) but still set assigned to
	// supID so the only filter that disqualifies it is `state='running'`.
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET assigned_supervisor_id = $1 WHERE id = $2`,
		supID, stale.ID,
	)
	require.NoError(t, err)

	// Insert two lock-holder rows: one anchored to the running node,
	// one anchored to the stale node. Initial expires_at is `now + 1s`
	// for both — short enough that any failure to refresh is observable
	// against the post-refresh window.
	runningHolderID := uuid.New()
	staleHolderID := uuid.New()
	lockNameRunning := "running-named"
	lockNameStale := "stale-named"

	initialExpiry := time.Now().Add(1 * time.Second)
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		if err := h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 runningHolderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockNameRunning,
			HolderSupervisorID: supID,
			HolderNodeID:       running.ID,
			ExpiresAt:          initialExpiry,
		}, tx); err != nil {
			return err
		}
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 staleHolderID,
			LockKind:           storage.LockKindNamed,
			LockName:           &lockNameStale,
			HolderSupervisorID: supID,
			HolderNodeID:       stale.ID,
			ExpiresAt:          initialExpiry,
		}, tx)
	}))

	// Capture the running-node row's last_heartbeat_at before the
	// ExtendHeartbeat so the post-extend assertion compares against a
	// known prior value rather than against time.Now() (which races
	// with postgres NOW()).
	preExtend, err := h.Storage.LockHolders().Get(h.Ctx, runningHolderID, nil)
	require.NoError(t, err)
	require.NotNil(t, preExtend)

	// Sleep briefly so the SQL `now()` advances past the inserted row's
	// last_heartbeat_at by an amount larger than test-host vs postgres
	// clock skew.
	time.Sleep(100 * time.Millisecond)

	// Call the §13.4 SQL: ExtendHeartbeat with a far-future expires_at.
	// Storage.LockHolders().ExtendHeartbeat converts the future
	// timestamp into a `5 × heartbeat_interval` style integer-second
	// budget; passing 30s gives clear separation from `initialExpiry`.
	farFuture := time.Now().Add(30 * time.Second)
	require.NoError(t, h.Storage.LockHolders().ExtendHeartbeat(h.Ctx, supID, farFuture, nil))

	// running-node's row: expires_at must have advanced past the
	// pre-call `initialExpiry` value.
	got, err := h.Storage.LockHolders().Get(h.Ctx, runningHolderID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, got.ExpiresAt.After(initialExpiry.Add(5*time.Second)),
		"running-node lock-holder expires_at should have been refreshed past initial+5s, got %v (initial %v)",
		got.ExpiresAt, initialExpiry)

	// stale-node's row: expires_at must be unchanged (the §13.4
	// running-node filter excludes it).
	gotStale, err := h.Storage.LockHolders().Get(h.Ctx, staleHolderID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotStale)
	require.WithinDuration(t, initialExpiry, gotStale.ExpiresAt, 100*time.Millisecond,
		"stale-node lock-holder expires_at must NOT be refreshed by the heartbeat (preserve-for-resume invariant)")

	// Sanity: the running-node row's last_heartbeat_at advanced relative
	// to its pre-extend value (the §13.4 SQL updates both columns in one
	// UPDATE).
	require.True(t, got.LastHeartbeatAt.After(preExtend.LastHeartbeatAt),
		"running-node lock-holder last_heartbeat_at should have advanced (pre=%v, post=%v)",
		preExtend.LastHeartbeatAt, got.LastHeartbeatAt)
}
