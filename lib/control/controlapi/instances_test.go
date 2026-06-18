// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestTerminateInstance_NoReasonEmptyBody(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-nobody-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.NotEmpty(t, out["terminated_at"])

	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	events, _ := out["events"].([]any)
	require.Len(t, events, 1)
	ev, _ := events[0].(map[string]any)
	payload, _ := ev["payload"].(map[string]any)
	require.Equal(t, "", payload["reason"])
}

func TestTerminateInstance_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-idem-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "first",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.NotEmpty(t, out["terminated_at"])

	status, out2 := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "second",
	})
	require.Equal(t, http.StatusOK, status, out2)
	require.Equal(t, out["terminated_at"], out2["terminated_at"],
		"idempotent terminate must not move terminated_at")

	status, out3 := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out3)
	events, _ := out3["events"].([]any)
	require.Len(t, events, 1, "idempotent terminate must not append a second event")
}

func TestTerminateInstance_NotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "POST", "/v1/instances/"+uuid.NewString()+"/terminate", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func refModeTemplateProvisionedValid(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"nodes": []map[string]any{
				{
					"type":     "root",
					"executor": "constrained",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"count": map[string]any{
									"type":    "integer",
									"default": 1,
								},
							},
						},
					},
				},
			},
		},
	}
}

func registerAndDeployBody(t *testing.T, h *harness, body map[string]any) string {
	t.Helper()
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, "register must succeed under the harness ref-mode; body: %v", out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, deployOut := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, "deploy must succeed; body: %v", deployOut)
	return tplID
}

func instanceCountForTemplate(t *testing.T, h *harness, templateHash string) int {
	t.Helper()
	status, out := h.httpJSON(t, "GET", "/v1/instances?template_hash="+templateHash, nil)
	require.Equal(t, http.StatusOK, status, out)
	rows, _ := out["instances"].([]any)
	return len(rows)
}

func TestCreateInstance_StaticConfigValidationGate(t *testing.T) {
	t.Run("rejects: static count:-1 violates the executor schema's minimum:0", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateNone)
		t.Cleanup(teardown)

		tplID := registerAndDeployBody(t, h, refModeTemplateProvisionedInvalid("static-gate-bad-"+uuid.NewString()))

		status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-" + uuid.NewString(),
		})
		require.Equal(t, http.StatusBadRequest, status,
			"instantiation must reject a static-config violation at create-time; body: %v", out)
		errText := strings.ToLower(fmt.Sprint(out["error"]) + " " + fmt.Sprint(out["validation_errors"]))
		require.Contains(t, errText, "count",
			"rejection must name the offending attribute `count`; body: %v", out)
		require.Contains(t, errText, "minimum",
			"rejection must cite the `minimum` value-constraint violation (a genuine value check, not a missing/extra-attribute surface error); body: %v", out)

		require.Equal(t, 0, instanceCountForTemplate(t, h, tplID),
			"a rejected static-config create must persist no instance row")
	})

	t.Run("admits: a well-formed instance of the same executor returns 201 and persists", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateNone)
		t.Cleanup(teardown)

		tplID := registerAndDeployBody(t, h, refModeTemplateProvisionedValid("static-gate-ok-"+uuid.NewString()))

		status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-" + uuid.NewString(),
		})
		require.Equal(t, http.StatusCreated, status,
			"a schema-compliant static default (count:1 ≥ minimum:0) must instantiate cleanly; body: %v", out)
		require.NotEmpty(t, out["instance_id"])

		require.Equal(t, 1, instanceCountForTemplate(t, h, tplID),
			"a well-formed create must persist exactly one instance row")
	})
}

func TestGetInstance_SurfacesSubscriptionStates(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-subs-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template": tplID,
		"params":   map[string]any{"region": "us-east"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	_, present := out["subscriptions"]
	require.False(t, present, "instance without subscriptions must omit the array")

	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)
	mountingID := shared.UUID(uuid.New())
	failedID := shared.UUID(uuid.New())
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             mountingID,
			InstanceID:     shared.UUID(instUUID),
			PublisherName:  "sensor-alpha",
			Kind:           "http",
			ResolvedConfig: []byte(`{"url":"https://example.invalid"}`),
			TargetNode:     "root",
			State:          persistence.PublisherSubscriptionStateMounting,
		}); err != nil {
			return err
		}
		return h.persist.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             failedID,
			InstanceID:     shared.UUID(instUUID),
			PublisherName:  "sensor-beta",
			Kind:           "http",
			ResolvedConfig: []byte(`{}`),
			TargetNode:     "root",
			State:          persistence.PublisherSubscriptionStateFailed,
			FailureReason:  `publisher "sensor-beta" is not registered`,
		})
	}))

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	subs, _ := out["subscriptions"].([]any)
	require.Len(t, subs, 2, "expected both subscription rows on the detail response: %v", out)

	byName := map[string]map[string]any{}
	for _, s := range subs {
		entry, ok := s.(map[string]any)
		require.True(t, ok)
		name, _ := entry["publisher_name"].(string)
		byName[name] = entry
	}
	mounting := byName["sensor-alpha"]
	require.NotNil(t, mounting)
	require.Equal(t, mountingID.String(), mounting["id"])
	require.Equal(t, "http", mounting["kind"])
	require.Equal(t, persistence.PublisherSubscriptionStateMounting, mounting["state"])
	require.NotEmpty(t, mounting["started_at"])
	_, reasonPresent := mounting["failure_reason"]
	require.False(t, reasonPresent, "non-failed row must omit failure_reason")

	failed := byName["sensor-beta"]
	require.NotNil(t, failed)
	require.Equal(t, persistence.PublisherSubscriptionStateFailed, failed["state"])
	require.Equal(t, `publisher "sensor-beta" is not registered`, failed["failure_reason"])
}
