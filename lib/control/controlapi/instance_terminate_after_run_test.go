// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestTerminateAfterRunRoundTrip(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-tar-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        "ck-tar-true-" + uuid.NewString(),
		"terminate_after_run": true,
	})
	require.Equal(t, http.StatusCreated, status, out)
	idTrue, _ := out["instance_id"].(string)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+idTrue, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["terminate_after_run"],
		"GET projection must surface terminate_after_run=true")

	uTrue, err := uuid.Parse(idTrue)
	require.NoError(t, err)
	var instTrue *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, uTrue, tx)
		instTrue = r
		return err
	}))
	require.NotNil(t, instTrue)
	require.True(t, instTrue.TerminateAfterRun,
		"persisted row must carry terminate_after_run=true")

	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-tar-false-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	idFalse, _ := out["instance_id"].(string)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+idFalse, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, false, out["terminate_after_run"],
		"GET projection must surface terminate_after_run=false when the field is omitted")

	uFalse, err := uuid.Parse(idFalse)
	require.NoError(t, err)
	var instFalse *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, uFalse, tx)
		instFalse = r
		return err
	}))
	require.NotNil(t, instFalse)
	require.False(t, instFalse.TerminateAfterRun,
		"persisted row must default terminate_after_run to false")
}

func TestTerminateAfterRunIdempotentRecreateIgnoresFlag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-tar-idem-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	key := "ck-tar-idem-" + uuid.NewString()

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        key,
		"terminate_after_run": true,
	})
	require.Equal(t, http.StatusCreated, status, out)
	firstID, _ := out["instance_id"].(string)
	require.NotEmpty(t, firstID)

	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        key,
		"terminate_after_run": false,
	})
	require.Equal(t, http.StatusOK, status, out)
	secondID, _ := out["instance_id"].(string)
	require.Equal(t, firstID, secondID,
		"idempotent re-create must return the existing instance, not a new one")

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+firstID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["terminate_after_run"],
		"idempotent re-create must ignore the flag; stored terminate_after_run=true must be unchanged")

	uID, err := uuid.Parse(firstID)
	require.NoError(t, err)
	var inst *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, uID, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	require.True(t, inst.TerminateAfterRun,
		"persisted row must keep terminate_after_run=true after the conflicting re-create")
}
