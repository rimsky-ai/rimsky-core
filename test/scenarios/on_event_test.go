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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
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
		Success(map[string]any{}, true, "a-done")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "on-event-stream", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
			}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "event/ready", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-on-event-stream", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should complete")

	// @deliberate: Wait for the named-event signal-shaped audit row on A. Per Pass 5
	// of spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design
	// the legacy `named_event_emitted` fixed-string row retired in
	// favor of the `event/<name>` signal type-path.
	require.True(t, h.WaitForEventKind(a.ID, "event/ready", 10*time.Second),
		"event/ready signal row should exist for A")

	// @deliberate: Verify the rimsky_node_events ledger row exists and carries the
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

	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
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
		Success(map[string]any{}, true, "a-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "on-event-latest", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-on-event-latest", map[string]any{})
	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should complete")

	require.Eventually(t, func() bool {
		evt, err := getLatestNodeEvent(t, h, iid, a.ID, "progress")
		return err == nil && evt != nil && len(evt.PayloadInline) > 0
	}, 10*time.Second, 100*time.Millisecond, "ledger row should be visible")

	evt, err := getLatestNodeEvent(t, h, iid, a.ID, "progress")
	require.NoError(t, err)
	require.NotNil(t, evt)
	// @constraint: LatestByName must return the most-recent emission.
	require.Contains(t, string(evt.PayloadInline), `"step":3`)
}

// TestOnEventUndeclaredEventNameRejectedAtRegistration covers H3 case (d)
// under the post-2026-05-14 subscription-cascade model. A template
// declaring a subscription `on: event` with a name not in the emitter
// executor's declared_events fails registration. The stub executor's
// ObservabilityCapabilities declares no events, so any event-topic
// subscription should be rejected at template-deploy time.
func TestOnEventUndeclaredEventNameRejectedAtRegistration(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	body := map[string]any{
		"template": map[string]any{
			"name":                  "on-event-undeclared",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "emitter", "executor": "stub"},
				{
					"type":     "receiver",
					"executor": "stub",
					"subscribes": []map[string]any{{
						"node":                   "emitter",
						"on":                     "event",
						"name":                   "undeclared_event",
						"wake_on_change":         true,
						"force_upstream_refresh": false,
					}},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/templates/register",
		"application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	// @constraint: Validator MUST reject; either 400 (validation) or 422 (semantic) is
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
