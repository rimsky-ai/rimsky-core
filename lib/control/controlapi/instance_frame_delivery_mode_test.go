// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// HTTP-level coverage for the per-instance frame_delivery_mode field on
// POST /instances. Pairs with the runtime helper in
// runtime/message_delivery.go::DeliverPendingMessages and the scenario
// tests under test/scenarios/messages/frame_delivery_mode_test.go.

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// strPtr is a local helper for *string literals in JSON bodies.
func strPtr(s string) *string { return &s }

func TestInstanceCreate_FrameDeliveryMode_DefaultIsSerialQueue(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-fdm-def-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	// GET response echoes the persisted value. An omitted mode is defaulted
	// by the INSERT literal (COALESCE(?, 'serial_queue')) to 'serial_queue'
	// — the new default per spec 2026-05-29 (one message per frame; coalesce
	// is now the opt-in mode).
	status, out = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "serial_queue", out["frame_delivery_mode"])

	// Row inspection confirms persistence.
	id, err := uuid.Parse(instID)
	require.NoError(t, err)
	var inst *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, id, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	require.Equal(t, "serial_queue", inst.FrameDeliveryMode)
}

func TestInstanceCreate_FrameDeliveryMode_SerialQueueRoundTrips(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-fdm-sq-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":            tplID,
		"instance_key":        "ck-" + uuid.NewString(),
		"frame_delivery_mode": "serial_queue",
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	status, out = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "serial_queue", out["frame_delivery_mode"])

	id, err := uuid.Parse(instID)
	require.NoError(t, err)
	var inst *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, id, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	require.Equal(t, "serial_queue", inst.FrameDeliveryMode)
}

func TestInstanceCreate_FrameDeliveryMode_RejectsUnknownValue(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-fdm-bad-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":            tplID,
		"instance_key":        "ck-" + uuid.NewString(),
		"frame_delivery_mode": "wishful-thinking",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "frame_delivery_mode")
	// Silence unused-helper lint when only invoked from other tests.
	_ = strPtr
}
