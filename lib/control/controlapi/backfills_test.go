// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// backfills_test.go — F4 integration tests: create + list + show +
// cancel against the pgtest harness.

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// postBackfillWithMode drives the real create-backfill handler with an
// explicit request mode injected into the context. The unit-test
// harness wires AuthState=nil, so the gateByAction middleware that
// resolves `?dry_run=true` into the request mode does not run; this
// helper mounts the handler on a chi router (so the {id} URL param
// resolves) behind a tiny middleware that sets ctxKeyMode, mirroring
// how the dry-run lineage test injects the mode directly.
func postBackfillWithMode(t *testing.T, h *harness, instID string, mode auth.Mode, body map[string]any) (int, map[string]any) {
	t.Helper()
	deps := AppDeps{
		Persist: h.persist,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKeyMode{}, mode)))
		})
	})
	r.Post("/v1/instances/{id}/backfills", handleCreateBackfill(deps))

	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/instances/%s/backfills", instID), bytes.NewReader(b))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// fanOutBackfillTemplateBody builds a template whose `root` node is a
// fan-out node wired for the backfill override: it pulls
// `partition_request` from the trigger message
// (`{{trigger.message.payload.partition_request_override | <default>}}`),
// so a backfill's override actually reaches the node. The `child` node
// subscribes to `root`. This is a valid `backfill:create` target.
//
// The harness does not wire the StoreAdvertisesSplitScope hook, so the
// fan-out split-scope capability gate is skipped at registration — the
// template validates against the `content` store fake without it having
// to advertise the capability (that gate is exercised in the runtime /
// scenario suites, not here).
func fanOutBackfillTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "root",
					"executor": "worker",
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "rw", "alias": "data"},
					},
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{{trigger.message.payload.partition_request_override | {"partition_keys":["default"]}}}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
				{"type": "child", "executor": "worker", "subscribes": []map[string]any{{"node": "root", "type": "terminal/*"}}},
			},
		},
	}
}

// unwiredFanOutBackfillTemplateBody builds a template whose `root` node
// IS a fan-out node but whose `partition_request` is a fixed literal —
// it does NOT reference the trigger message, so a backfill's override
// would be silently ignored. This is the subtle invalid case: a
// fan-out target that is not wired for the override. `backfill:create`
// must reject it (400) rather than accept-and-degrade.
func unwiredFanOutBackfillTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "root",
					"executor": "worker",
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "rw", "alias": "data"},
					},
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{"partition_keys":["a","b","c"]}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
				{"type": "child", "executor": "worker", "subscribes": []map[string]any{{"node": "root", "type": "terminal/*"}}},
			},
		},
	}
}

// deployFanOutInstance registers + deploys the given template body and
// creates an instance, returning the instance id.
func deployFanOutInstance(t *testing.T, h *harness, tplBody map[string]any, keyPrefix string) string {
	t.Helper()
	status, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": keyPrefix + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	return instID
}

// TestBackfills_CreateListShowCancel drives a backfill end-to-end
// against a fan-out node wired for the override.
func TestBackfills_CreateListShowCancel(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-"+uuid.NewString()), "bf-ck-")

	// @constraint: create a backfill targeting `root` (a wired fan-out node).
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/backfills", instID), map[string]any{
		"target_node": "root",
		"reason":      "smoke",
		"partition_request_override": map[string]any{
			"date_range": map[string]any{"start": "2024-01-01", "end": "2024-01-02"},
		},
	})
	require.Equal(t, http.StatusCreated, status, out)
	opID, _ := out["backfill_operation_id"].(string)
	require.NotEmpty(t, opID)
	require.NotEmpty(t, out["message_id"])

	// @constraint: list backfills for the instance.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/backfills", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	items, _ := out["backfills"].([]any)
	require.GreaterOrEqual(t, len(items), 1)
	first := items[0].(map[string]any)
	require.Equal(t, opID, first["operation_id"])
	require.Equal(t, "root", first["target_node"])
	require.Equal(t, "smoke", first["reason"])

	// @constraint: show single backfill.
	status, out = h.httpJSON(t, "GET", "/v1/backfills/"+opID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, opID, out["operation_id"])
	require.Equal(t, "smoke", out["reason"])

	// @constraint: partitions: not yet delivered → empty list.
	status, out = h.httpJSON(t, "GET", "/v1/backfills/"+opID+"/partitions", nil)
	require.Equal(t, http.StatusOK, status, out)
	parts, _ := out["partitions"].([]any)
	require.Equal(t, 0, len(parts))

	status, out = h.httpJSON(t, "POST", "/v1/backfills/"+opID+"/cancel", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["cancelled"])
}

// TestBackfills_CreateMissingTargetNode rejects missing target_node.
func TestBackfills_CreateMissingTargetNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-mt-"+uuid.NewString()), "bf-mt-ck-")

	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/backfills", instID), map[string]any{
		"reason": "no target",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestBackfills_CreateRejectsUnknownTargetNode rejects a target_node
// that is not declared in the instance's template (400).
func TestBackfills_CreateRejectsUnknownTargetNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-unk-"+uuid.NewString()), "bf-unk-ck-")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/backfills", instID), map[string]any{
		"target_node": "does-not-exist",
		"reason":      "bad target",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
}

// TestBackfills_CreateRejectsNonFanOutTarget rejects a target_node that
// exists but declares no fan_out block (400). A backfill is meaningless
// without a fan-out.
func TestBackfills_CreateRejectsNonFanOutTarget(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// @constraint: `child` exists but is a plain executor node (no fan_out).
	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-nf-"+uuid.NewString()), "bf-nf-ck-")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/backfills", instID), map[string]any{
		"target_node": "child",
		"reason":      "not a fan-out",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
}

// TestBackfills_CreateRejectsFanOutNotWiredForOverride rejects a
// target_node that IS a fan-out node but whose partition_request does
// not reference the trigger message — the override would be silently
// ignored. This is the subtle "not wired for override" case; it must
// reject (400), not accept-and-degrade.
func TestBackfills_CreateRejectsFanOutNotWiredForOverride(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, unwiredFanOutBackfillTemplateBody("bf-uw-"+uuid.NewString()), "bf-uw-ck-")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/backfills", instID), map[string]any{
		"target_node": "root",
		"reason":      "fan-out but unwired",
		"partition_request_override": map[string]any{
			"partition_keys": []string{"x", "y"},
		},
	})
	require.Equal(t, http.StatusBadRequest, status, out)
}

// TestBackfills_DryRunRejectsBadTarget confirms a dry-run of a bad
// target fails identically to a live call (400) — the validation
// projects into the preview path, never silently passing. The mode is
// injected into the request context (the unit harness has no
// gateByAction to resolve ?dry_run=true).
func TestBackfills_DryRunRejectsBadTarget(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-dr-"+uuid.NewString()), "bf-dr-ck-")

	// @constraint: non-fan-out target under dry-run → 400 (same as live).
	status, out := postBackfillWithMode(t, h, instID, auth.ModeDryRun, map[string]any{
		"target_node": "child",
		"reason":      "dry-run bad target",
	})
	require.Equal(t, http.StatusBadRequest, status, out)

	// @constraint: fan-out-but-unwired target under dry-run → 400 too. Re-uses an
	// unwired template instance to cover the subtle case in preview.
	unwiredInst := deployFanOutInstance(t, h, unwiredFanOutBackfillTemplateBody("bf-dr-uw-"+uuid.NewString()), "bf-dr-uw-ck-")
	status, out = postBackfillWithMode(t, h, unwiredInst, auth.ModeDryRun, map[string]any{
		"target_node": "root",
		"reason":      "dry-run unwired fan-out",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
}

// TestBackfills_DryRunAcceptsWiredTarget confirms a dry-run of a valid
// wired fan-out target returns the synthetic preview envelope (no
// mutation), not a rejection.
func TestBackfills_DryRunAcceptsWiredTarget(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := deployFanOutInstance(t, h, fanOutBackfillTemplateBody("bf-dra-"+uuid.NewString()), "bf-dra-ck-")

	status, out := postBackfillWithMode(t, h, instID, auth.ModeDryRun, map[string]any{
		"target_node": "root",
		"reason":      "dry-run wired target",
		"partition_request_override": map[string]any{
			"partition_keys": []string{"x", "y"},
		},
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["dry_run"])
	require.Contains(t, out, "would_have_created_backfill")

	// @constraint: the dry-run must NOT have enqueued a backfill message — listing
	// returns no backfills for this instance.
	listStatus, listOut := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/backfills", instID), nil)
	require.Equal(t, http.StatusOK, listStatus, listOut)
	items, _ := listOut["backfills"].([]any)
	require.Equal(t, 0, len(items), "dry-run must not enqueue a backfill")
}
