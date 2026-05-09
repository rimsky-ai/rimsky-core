// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// on_event lifecycle scenario tests covering H1 (event-emission processing
// + persistence) and H2 (on_event handler dispatch). Per the 2026-05-08
// platform-extensions plan H3.

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
)

// TestOnEventGRPCStreamPath covers H3 case (a). Node A emits a NamedEvent
// on the gRPC stream; node B's on_event[ready] fires; B is invalidated.
// Verify the persisted rimsky_node_events row carries the expected
// payload.
func TestOnEventGRPCStreamPath(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		EmitNamedEvent("ready", []byte(`{"go":true,"score":42}`)).
		Complete(map[string]any{}, true, "a-done")
	h.Stub.WhenType("b").Complete(map[string]any{}, true, "b-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "on-event-stream", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
				OnEvent: map[string]node.EventHandler{
					"ready": {
						Invalidate: &node.HandlerInvalidate{Targets: []string{"b"}, Frame: node.FrameNext},
					},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-on-event-stream", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 30*time.Second),
		"a should complete")

	// Wait for the named-event audit log on A.
	require.True(t, h.WaitForEventKind(a.ID, "named_event_emitted", 10*time.Second),
		"named_event_emitted audit row should exist for A")

	// Verify the rimsky_node_events ledger row exists and carries the
	// payload bytes.
	require.Eventually(t, func() bool {
		evt, err := getLatestNodeEvent(t, h, iid, a.ID, "ready")
		return err == nil && evt != nil && len(evt.PayloadInline) > 0
	}, 10*time.Second, 100*time.Millisecond,
		"rimsky_node_events row should be visible")

	evt, err := getLatestNodeEvent(t, h, iid, a.ID, "ready")
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.Contains(t, string(evt.PayloadInline), `"go":true`)

	// B should have re-fired (the on_event handler invalidated it).
	require.True(t, h.WaitForNodeState(b.ID, shared.NodeStateFresh, 30*time.Second),
		"b should re-run after on_event handler invalidate")
}

// TestOnEventMultipleEmissionsLatestWins covers H3 case (e). Two emissions
// of the same event name; substitution / LatestByName should return the
// most recent.
func TestOnEventMultipleEmissionsLatestWins(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").
		EmitNamedEvent("progress", []byte(`{"step":1}`)).
		EmitNamedEvent("progress", []byte(`{"step":2}`)).
		EmitNamedEvent("progress", []byte(`{"step":3}`)).
		Complete(map[string]any{}, true, "a-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "on-event-latest", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-on-event-latest", map[string]any{})
	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 30*time.Second),
		"a should complete")

	require.Eventually(t, func() bool {
		evt, err := getLatestNodeEvent(t, h, iid, a.ID, "progress")
		return err == nil && evt != nil && len(evt.PayloadInline) > 0
	}, 10*time.Second, 100*time.Millisecond, "ledger row should be visible")

	evt, err := getLatestNodeEvent(t, h, iid, a.ID, "progress")
	require.NoError(t, err)
	require.NotNil(t, evt)
	// LatestByName must return the most-recent emission.
	require.Contains(t, string(evt.PayloadInline), `"step":3`)
}

// TestOnEventUndeclaredEventNameRejectedAtRegistration covers H3 case (d).
// A template referencing an event name not in the executor's
// declared_events fails registration. The stub executor's
// ObservabilityCapabilities currently declares no events, so any
// on_event referencing an event name should be rejected.
func TestOnEventUndeclaredEventNameRejectedAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Build a template whose on_event handler references an event name
	// the stub executor does not declare. With AppDeps.ExecutorCapabilities
	// wired (config.StartControlAPI) the controlapi templates handler
	// rejects this at registration before the deploy step.
	body := map[string]any{
		"template": map[string]any{
			"name":             "on-event-undeclared",
			"version":          "1",
			"frame_resolution": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"on_event": map[string]any{
						"undeclared_event": map[string]any{
							"invalidate": map[string]any{
								"targets": []string{"worker"},
								"frame":   "next",
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/templates/register",
		"application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	// Validator MUST reject; either 400 (validation) or 422 (semantic) is
	// acceptable. 200 means the validator silently accepted — bug.
	require.NotEqual(t, http.StatusOK, resp.StatusCode,
		"controlapi must reject template referencing undeclared event %q",
		"undeclared_event")
}

// getLatestNodeEvent reads the most-recent rimsky_node_events row for
// the (instance, emitter, event_name) tuple via the persistence-layer
// interface. Used by H3 / L3 to assert the ledger end-to-end.
func getLatestNodeEvent(
	t *testing.T,
	h *scenario.Harness,
	instanceID shared.UUID,
	emitterID shared.UUID,
	eventName string,
) (*persistence.NodeEvent, error) {
	t.Helper()
	var out *persistence.NodeEvent
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.NodeEvents().LatestByName(
			ctx, instanceID.String(), emitterID.String(), eventName, tx,
		)
		out = row
		return err
	})
	return out, err
}
