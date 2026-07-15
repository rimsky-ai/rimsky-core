// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type driveSetup struct {
	backend   persistence.Tables
	cb        *runtime.CallbackServer
	addr      string
	nodeRunID shared.UUID
	ackID     string
}

func newDriveSetup(
	ctx context.Context, t *testing.T, supID string,
	schema map[string]any, declaredTags func(string) ([]string, bool),
) driveSetup {
	t.Helper()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()
	clk := newTickClock(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{Name: "cbk-validation", Version: "1"})
	ck := "ck-cbk-val"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, &ck)
		inst = i
		mainScopeID = ms
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "leaf", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nd = n
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, nd.ID, mainScopeID)
	nodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), nd.ID, frameID)

	ackID := "ack-cbk-val-" + nodeRunID.String()
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		claimed, err := d.Queue().ClaimDispatchRow(ctx, tx, nodeRunID, supID)
		if err != nil {
			return err
		}
		require.True(t, claimed, "run must be claimable")
		promoted, err := d.Queue().PromoteClaimedToRunning(ctx, tx, nodeRunID, supID)
		if err != nil {
			return err
		}
		require.True(t, promoted, "run must promote to running")
		return d.Queue().RegisterAsyncAck(ctx, tx, nodeRunID, ackID, clk.Now(), nil, nil)
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
		ResolvedAttributes: map[string]any{},
		AttributesSchema:   schema,
	})

	return driveSetup{backend: backend, cb: cb, addr: addr, nodeRunID: nodeRunID, ackID: ackID}
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
