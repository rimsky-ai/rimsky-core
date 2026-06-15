// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// HTTP-level round-trip coverage for the per-instance terminate_after_run
// flag on POST /instances. Mirrors the existing paused / frame_delivery_mode
// flag coverage: the create request decodes the flag, persistence stores it,
// and the GET/list projection surfaces it. No termination-behavior assertion
// here — that lands in a later pass; this pass only proves the flag threads
// create-request → persistence column → projection end to end.
//
// Exercised against the pgtest harness (real Postgres via testcontainers).
//
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

// TestTerminateAfterRunRoundTrip proves the terminate_after_run flag threads
// create-request → persistence column → GET projection: an instance created
// with "terminate_after_run": true reports it true on GET; an instance
// created without the field reports it false (the column default).
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

	// @constraint: instance A: created WITH terminate_after_run: true.
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

	// @constraint: row inspection confirms persistence.
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

	// @constraint: instance B: created WITHOUT the field → defaults to false.
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

// TestTerminateAfterRunIdempotentRecreateIgnoresFlag pins the spec §1
// idempotency rule: a re-create with the same (template_hash, instance_key)
// returns the EXISTING row (200 OK, same instance_id) and ignores the
// terminate_after_run flag on the re-create request — exactly as `paused`
// behaves. The first create sets the flag true; the same-key re-create sends
// a conflicting false and must NOT flip the stored value. Without this guard
// the property is only structurally implied by the shared create path; this
// makes it an explicit gate.
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

	// @constraint: first create: terminate_after_run = true → 201 Created.
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        key,
		"terminate_after_run": true,
	})
	require.Equal(t, http.StatusCreated, status, out)
	firstID, _ := out["instance_id"].(string)
	require.NotEmpty(t, firstID)

	// @constraint: idempotent re-create: SAME key, CONFLICTING flag (false). Spec §1 — the
	// re-create resolves to the existing row (200 OK) and ignores the flag.
	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        key,
		"terminate_after_run": false,
	})
	require.Equal(t, http.StatusOK, status, out)
	secondID, _ := out["instance_id"].(string)
	require.Equal(t, firstID, secondID,
		"idempotent re-create must return the existing instance, not a new one")

	// @constraint: GET projection still reports true — the re-create's false was ignored.
	status, out = h.httpJSON(t, "GET", "/v1/instances/"+firstID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["terminate_after_run"],
		"idempotent re-create must ignore the flag; stored terminate_after_run=true must be unchanged")

	// @constraint: persisted row confirms the stored value survived the conflicting re-create.
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
