// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// HTTP-level coverage for the per-instance attribute_overrides field on
// POST /instances. Pairs with the deep-merge unit tests in
// runtime/attribute_overrides_test.go and the validator
// unit tests in attribute_overrides_test.go.

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

func TestInstanceCreate_AttributeOverrides_RoundTripAndPersistence(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-uo-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	overrides := map[string]any{
		"by_executor": map[string]any{
			"worker": map[string]any{
				"cli": map[string]any{
					"synthetic_scenario": "exit-clean-no-callback",
					"silence_timeout_ms": float64(120000),
				},
			},
		},
		"by_node": map[string]any{
			"root": map[string]any{
				"cli": map[string]any{"trace_to": "/var/traces/root.jsonl"},
			},
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        "ck-" + uuid.NewString(),
		"attribute_overrides": overrides,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	// @constraint: round-trips via GET /instances/:id.
	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	gotOverrides, ok := out["attribute_overrides"].(map[string]any)
	require.True(t, ok, "attribute_overrides missing from GET response: %v", out)
	require.Equal(t, overrides, gotOverrides)

	// @constraint: persisted on the row directly so the dispatch path (which reads
	// the row at acquisition) sees the same shape.
	id, err := uuid.Parse(instID)
	require.NoError(t, err)
	var inst *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, id, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	require.Equal(t, overrides, inst.AttributeOverrides)
}

func TestInstanceCreate_AttributeOverrides_OmittedDefaultsEmpty(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-empty-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status)
	// @constraint: omit-from-response when empty (omitempty on the struct tag).
	_, present := out["attribute_overrides"]
	require.False(t, present, "attribute_overrides should be omitted from the GET response when empty: %v", out)
}

func TestInstanceCreate_AttributeOverrides_RejectsUnknownExecutor(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-bad-exec-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"attribute_overrides": map[string]any{
			"by_executor": map[string]any{
				"made-up-executor": map[string]any{"cli": "x"},
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "unknown executor name")
}

func TestInstanceCreate_AttributeOverrides_RejectsUnknownNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-bad-node-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"attribute_overrides": map[string]any{
			"by_node": map[string]any{
				"made-up-node": map[string]any{"cli": "x"},
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "unknown node name")
}

func TestInstanceCreate_AttributeOverrides_RejectsExecutorNotReferencedByTemplate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-unused-exec-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	// @constraint: `unused-exec` is declared in the harness's Executors map but the
	// `validTemplateBody` template only routes to `worker`. Attempting
	// to override on `unused-exec` would be a silent no-op at dispatch —
	// the validator must reject it.
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"attribute_overrides": map[string]any{
			"by_executor": map[string]any{
				"unused-exec": map[string]any{"cli": "x"},
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "executor not referenced by any template node")
}

func TestInstanceCreate_AttributeOverrides_IdempotentMatch_NoWarn(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-idemp-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	overrides := map[string]any{
		"by_executor": map[string]any{
			"worker": map[string]any{
				"cli": map[string]any{"trace_to": "/var/traces/r.jsonl"},
			},
		},
	}
	instanceKey := "ck-" + uuid.NewString()
	status, _ := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        instanceKey,
		"attribute_overrides": overrides,
	})
	require.Equal(t, http.StatusCreated, status)

	// @constraint: clear logs from the initial create so we only see records from
	// the idempotent retry that follows.
	h.logger.Clear()

	// @constraint: idempotent retry with the SAME body: the persisted row's
	// overrides match the request, so nothing was actually discarded;
	// the WARN must not fire.
	status, _ = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        instanceKey,
		"attribute_overrides": overrides,
	})
	require.Equal(t, http.StatusOK, status)
	for _, rec := range h.logger.Records() {
		if rec.Msg == "instance.attribute_overrides_replaced_by_idempotent_match" ||
			rec.Msg == "instance.attribute_overrides_ignored_idempotent_match" {
			t.Fatalf("expected no idempotent-match WARN; got %+v", rec)
		}
	}
}

func TestInstanceCreate_AttributeOverrides_IdempotentMismatch_Warns(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-idemp-mm-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	originalOverrides := map[string]any{
		"by_executor": map[string]any{
			"worker": map[string]any{
				"cli": map[string]any{"trace_to": "/var/traces/orig.jsonl"},
			},
		},
	}
	instanceKey := "ck-" + uuid.NewString()
	status, _ := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        instanceKey,
		"attribute_overrides": originalOverrides,
	})
	require.Equal(t, http.StatusCreated, status)

	h.logger.Clear()

	// @constraint: idempotent retry with a DIFFERENT body — the persisted row's
	// overrides won't match, so the WARN must fire.
	differentOverrides := map[string]any{
		"by_executor": map[string]any{
			"worker": map[string]any{
				"cli": map[string]any{"trace_to": "/var/traces/different.jsonl"},
			},
		},
	}
	status, _ = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":            tplID,
		"instance_key":        instanceKey,
		"attribute_overrides": differentOverrides,
	})
	require.Equal(t, http.StatusOK, status)

	var found bool
	for _, rec := range h.logger.Records() {
		if rec.Msg == "instance.attribute_overrides_replaced_by_idempotent_match" {
			found = true
			break
		}
	}
	require.True(t, found, "expected instance.attribute_overrides_replaced_by_idempotent_match WARN; got records=%+v", h.logger.Records())
}

func TestInstanceCreate_AttributeOverrides_RejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-uo-bad-top-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"attribute_overrides": map[string]any{
			"global": map[string]any{"cli": "x"},
		},
	})
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "unknown top-level key")
}
