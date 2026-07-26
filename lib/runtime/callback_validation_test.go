// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type driveSetup struct {
	backend    persistence.Tables
	cb         *runtime.CallbackServer
	addr       string
	instanceID shared.UUID
	nodeRunID  shared.UUID
	ackID      string
}

func newDriveSetup(
	ctx context.Context, t *testing.T, supID string,
	schema map[string]any, declaredTags func(string) ([]string, bool),
) driveSetup {
	t.Helper()
	return newDriveSetupWithPartitionKey(ctx, t, supID, schema, declaredTags, "")
}

func newDriveSetupWithPartitionKey(
	ctx context.Context, t *testing.T, supID string,
	schema map[string]any, declaredTags func(string) ([]string, bool),
	partitionKey string,
) driveSetup {
	t.Helper()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()
	clk := newTickClock(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{Name: "cbk-validation", Version: "1"})
	ck := "ck-cbk-val"
	var runScopeID shared.UUID
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, &ck, tx)
		inst = i
		runScopeID = ms
		if partitionKey != "" {
			childScopeID := shared.UUID(uuid.New())
			if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
				ID:           childScopeID,
				GraphName:    "main",
				InstanceID:   inst.ID,
				PartitionKey: partitionKey,
			}, tx); err != nil {
				return err
			}
			runScopeID = childScopeID
		}
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "leaf", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nd = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, runScopeID)
	nodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), nd.ID, frameID)

	ackID := "ack-cbk-val-" + nodeRunID.String()
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		claimed, err := d.Queue().ClaimDispatchRow(ctx, nodeRunID, supID, tx)
		if err != nil {
			return err
		}
		require.True(t, claimed, "run must be claimable")
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, nodeRunID, supID, tx)
		if err != nil {
			return err
		}
		require.True(t, promoted, "run must promote to running")
		return d.Queue().RegisterAsyncAck(ctx, nodeRunID, ackID, clk.Now(), nil, nil, "", "", tx)
	}))

	cb := &runtime.CallbackServer{
		Registry:        runtime.NewCallbackRegistry(),
		Persist:         backend,
		Queue:           d.Queue(),
		AdvisoryLocker:  d.AdvisoryLocker(),
		ClaimHandles:    backend.ClaimHandles(),
		Clock:           clk,
		Logger:          shared.SilentLogger{},
		SupervisorID:    supID,
		DeclaredTagsFor: declaredTags,
	}
	addr, err := cb.Start("127.0.0.1", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cb.Close(context.Background()) })

	cb.Registry.Register(ackID, runtime.AsyncContext{
		NodeID:             nd.ID,
		InstanceID:         inst.ID,
		NodeRunID:          nodeRunID,
		SupervisorID:       supID,
		FrameID:            frameID,
		NodeType:           "leaf",
		Executor:           "stub",
		GraphName:          "main",
		ResolvedAttributes: map[string]any{},
		AttributesSchema:   schema,
	})

	return driveSetup{backend: backend, cb: cb, addr: addr, instanceID: inst.ID, nodeRunID: nodeRunID, ackID: ackID}
}

func (s driveSetup) post(ctx context.Context, t *testing.T, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+s.addr+"/v1/callback/"+s.ackID, bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (s driveSetup) runState(ctx context.Context, t *testing.T) cascade.NodeState {
	t.Helper()
	var st cascade.NodeState
	require.NoError(t, s.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := s.backend.Nodes().GetRunByDispatchIDForUpdate(ctx, s.nodeRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		st = row.State
		return nil
	}))
	return st
}

func (s driveSetup) finalAttrs(ctx context.Context, t *testing.T) *persistence.NodeAttributesRow {
	t.Helper()
	var row *persistence.NodeAttributesRow
	require.NoError(t, s.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := s.backend.NodeAttributes().GetByRun(ctx, s.nodeRunID, tx)
		row = r
		return err
	}))
	return row
}

func TestDriveTerminal_CommitValidatesDeltaWhenUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	}
	s := newDriveSetup(ctx, t, "sup-1631", schema, nil)

	s.post(ctx, t, `{"success":{"changed":false,"attributes_delta":{"count":"not-an-int"}}}`)

	attrs := s.finalAttrs(ctx, t)
	if attrs != nil {
		require.NotEqual(t, "not-an-int", attrs.Data["count"],
			"schema-violating writeback must not be committed even when changed=false")
	}
	require.Equal(t, cascade.NodeStateFailed, s.runState(ctx, t),
		"a failed commit gate must error the run, not settle it as complete")
}

func TestDriveTerminal_RejectsUndeclaredTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newDriveSetup(ctx, t, "sup-1504", nil, func(string) ([]string, bool) {
		return []string{"approved"}, true
	})

	s.post(ctx, t, `{"success":{"changed":true,"tags":["rogue"]}}`)

	require.Equal(t, cascade.NodeStateFailed, s.runState(ctx, t),
		"an undeclared terminal tag on the async path must be rejected as a protocol violation, not completed")
}

func TestDriveTerminal_RejectedCallbackSkipsAfterTerminalBreakpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newDriveSetup(ctx, t, "sup-phantom-1506", nil, nil)

	bpID := createBreakpointForEval(t, ctx, s.backend, persistence.BreakpointRow{
		InstanceID:     s.instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointAfterTerminal,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	s.post(ctx, t, `{"success":{"changed":true}}`)
	require.Equal(t, cascade.NodeStateFresh, s.runState(ctx, t))

	hitsAfterFirst := listHitsForBreakpoint(t, ctx, s.backend, bpID)
	require.Len(t, hitsAfterFirst, 1,
		"the legitimate settling callback must record exactly one after_terminal hit")

	s.post(ctx, t, `{"success":{"changed":true}}`)

	hitsAfterDuplicate := listHitsForBreakpoint(t, ctx, s.backend, bpID)
	require.Len(t, hitsAfterDuplicate, 1,
		"a late/duplicate callback rejected as already-terminal must not mint a phantom after_terminal hit")
}

func TestDriveTerminal_AsyncCallbackAfterTerminalBreakpoint_ChildKeyAndGraphNameRecovered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newDriveSetupWithPartitionKey(ctx, t, "sup-asyncctx-2413", nil, nil, "partition-a")

	bpID := createBreakpointForEval(t, ctx, s.backend, persistence.BreakpointRow{
		InstanceID:     s.instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointAfterTerminal,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	s.post(ctx, t, `{"success":{"changed":true}}`)

	hits := listHitsForBreakpoint(t, ctx, s.backend, bpID)
	require.Len(t, hits, 1)

	dispatchContext, ok := hits[0].Snapshot["dispatch_context"].(map[string]any)
	require.True(t, ok, "hit snapshot must carry a dispatch_context object")

	require.Equal(t, "partition-a", dispatchContext["child_key"],
		"async-callback after_terminal recovers child_key from the resolved run scope's partition key")
	require.Equal(t, "main", dispatchContext["graph"],
		"async-callback after_terminal now recovers graph name: AsyncContext carries GraphName from "+
			"registration through to driveTerminal's breakpoint eval (breakpoint.md, ledger 2413) — "+
			"a graph-scoped breakpoint can match this checkpoint on the async path")
}

func TestCallback_ConcurrentDuplicateCallbacks_AckOutcomeNeverSwapped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newDriveSetup(ctx, t, "sup-dup-1503", nil, nil)

	const n = 2
	statuses := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"http://"+s.addr+"/v1/callback/"+s.ackID, bytes.NewReader([]byte(`{"success":{"changed":true}}`)))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var body struct {
				AckStatus string `json:"ack_status"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			statuses[i] = body.AckStatus
		}(i)
	}
	close(start)
	wg.Wait()

	accepted, rejectedTerminal := 0, 0
	for _, st := range statuses {
		switch st {
		case "accepted":
			accepted++
		case "rejected_run_terminal":
			rejectedTerminal++
		default:
			t.Fatalf("unexpected ack_status %q among %v", st, statuses)
		}
	}
	require.Equal(t, 1, accepted,
		"exactly one concurrent duplicate must apply the terminal and report accepted; got statuses %v", statuses)
	require.Equal(t, 1, rejectedTerminal,
		"exactly one concurrent duplicate must be rejected as already-terminal, never both accepted or both rejected; got statuses %v", statuses)
	require.Equal(t, cascade.NodeStateFresh, s.runState(ctx, t))
}
