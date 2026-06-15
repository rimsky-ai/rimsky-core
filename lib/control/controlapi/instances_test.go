// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instances_test.go — HTTP-level integration tests for the
// instance force-terminate surface (POST /instances/{idOrKey}/terminate)
// Feature 2. Exercised against the pgtest harness (real Postgres via
// testcontainers).
//
// terminate is the first production instance-teardown path: it marks the
// instance terminal (sets terminated_at, previously only test-driven via
// MarkTerminated) and force-fails the instance's resource-holding
// in-flight node-runs under the new instance_killed transition reason.
//
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

// @constraint: the await-async-stuck terminate proof (a running node-run force-failed
// to instance_killed, then DELETE'd) is no longer exercised at this
// handler altitude. Per spec S-lifecycle-fullstack-terminate-backfill —
// "FULL-STACK scenario tests, NOT handler-altitude unit tests with
// fakes" — that path is now covered end to end by
// TestForceTerminateAwaitAsyncStuckFullStack in test/scenarios/, which
// drives a REAL running run-row through the real dispatch path instead of
// a hand-INSERTed one. The superseded TestTerminateInstance_ForceFailsRunningNode
// and its raw-SQL seedRunningNodeRun / loadNodeState / loadRunScopeClosed
// helpers were removed here (pre-v1 "delete superseded code" rule). The
// remaining terminate tests below cover orthogonal surfaces (no-reason
// body, idempotent re-terminate, not-found) that the full-stack scenario
// does not, so they stay.

// TestTerminateInstance_NoReasonEmptyBody confirms terminate tolerates an
// absent body (reason defaults to empty) and still marks terminal.
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

// TestTerminateInstance_Idempotent confirms a second terminate on an
// already-terminal instance returns 200 with no error and records no
// second event.
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

	// @constraint: second call is idempotent: still 200, terminated_at unchanged.
	status, out2 := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "second",
	})
	require.Equal(t, http.StatusOK, status, out2)
	require.Equal(t, out["terminated_at"], out2["terminated_at"],
		"idempotent terminate must not move terminated_at")

	// @constraint: only the first call recorded an event.
	status, out3 := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out3)
	events, _ := out3["events"].([]any)
	require.Len(t, events, 1, "idempotent terminate must not append a second event")
}

// TestTerminateInstance_NotFound returns 404 for an unknown instance.
func TestTerminateInstance_NotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "POST", "/v1/instances/"+uuid.NewString()+"/terminate", nil)
	require.Equal(t, http.StatusNotFound, status)
}

// refModeTemplateProvisionedValid returns a wrapped POST /templates body
// for a template whose node references the PROVISIONED constrained
// executor (advertising `count` with `minimum: 0` via the
// newRefModeHarness ExecutorCapabilities) with a schema-compliant static
// default (`count: 1`). It is the well-formed twin of
// refModeTemplateProvisionedInvalid (in templates_test.go) — same
// executor, same schema shape, but a default that satisfies the
// executor's `minimum: 0` constraint. The companion sub-case of
// TestCreateInstance_StaticConfigValidationGate instantiates this body
// and asserts a clean 201 + persisted row, proving the gate rejects only
// the genuinely-misconfigured static default, not every instance of a
// constrained executor.
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

// registerAndDeployBody registers a specific wrapped POST /templates body
// and transitions register → deployed, returning the template hash. Both
// steps must succeed; instance creation requires state='deployed' per
// spec §2.2. (The sibling registerAndDeploy in compose_prefix_test.go
// always registers validTemplateBody; this variant takes an arbitrary
// body so the ref-mode constrained-executor templates can be deployed.)
// Registration runs under whatever reference-validation mode the harness
// was booted with — under `none` it skips the executor-schema cross-check
// entirely, so a static-default violation slips past registration and
// must be caught at instantiation.
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

// instanceCountForTemplate reads GET /instances filtered to the given
// template hash and returns how many instance rows are persisted for it.
// Used to prove the static-config gate persists NOTHING on a rejected
// create (the no-data-loss / no-partial-write property the gate must
// hold: a 400 instance-create leaves zero rows behind).
func instanceCountForTemplate(t *testing.T, h *harness, templateHash string) int {
	t.Helper()
	status, out := h.httpJSON(t, "GET", "/v1/instances?template_hash="+templateHash, nil)
	require.Equal(t, http.StatusOK, status, out)
	rows, _ := out["instances"].([]any)
	return len(rows)
}

// TestCreateInstance_StaticConfigValidationGate is the mandatory
// instantiation-time static-config validation gate
// (story S-template-validation-instantiation-mandatory, plan
// TEMPLCASCADE-4). It drives POST /instances through the real
// handleCreateInstance against a real Postgres-backed control-api.
//
// Setup mirrors the spec's claude-agent example
// (`cli.max_signoff_attempts: -1` vs an executor schema declaring
// `minimum: 0`): the constrained executor in newRefModeHarness advertises
// `count` with `minimum: 0`, and refModeTemplateProvisionedInvalid sets
// the node's static default to `count: -1`. The harness boots under
// reference-validation mode `none`, so REGISTRATION skips the executor-
// schema cross-check and the misconfigured template registers + deploys
// clean. Instantiation is then the gate that must catch the static
// misconfiguration.
//
// RED today: handleCreateInstance runs NO schema validation
// (validateAttributeOverrides checks only override keys/shapes, not
// node-attribute VALUES against the executor schema), so the rejected
// sub-case's POST /instances returns 201 with a persisted row instead of
// the demanded 400 — the value-constraint violation surfaces only at
// dispatch today, not at create-time. A later GREEN pass adds the
// instantiation-time validation gate. The verification command inverts
// this test's expected failure (`! go test ...`) to a pass.
func TestCreateInstance_StaticConfigValidationGate(t *testing.T) {
	t.Run("rejects: static count:-1 violates the executor schema's minimum:0", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateNone)
		t.Cleanup(teardown)

		// @constraint: registration succeeds under mode `none` (it skips the executor-
		// schema cross-check) even though count:-1 violates minimum:0.
		tplID := registerAndDeployBody(t, h, refModeTemplateProvisionedInvalid("static-gate-bad-"+uuid.NewString()))

		// @constraint: instantiation is the mandatory gate: it MUST reject the create
		// with a 400 that names the offending attribute (`count`) AND cites
		// the `minimum` value-constraint violation — a genuine value check,
		// not a missing/extra-attribute surface error.
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

		// @constraint: no instance row was persisted for the rejected template.
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

// TestGetInstance_SurfacesSubscriptionStates — the instance-detail GET
// exposes per-subscription publisher state
// (concept:publisher-subscription): the operator can watch a
// subscription's mounting → active progress, and a failed row carries
// its reason — instead of inferring publisher health from the create
// response succeeding.
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

	// @constraint: no publishers declared → no subscriptions key (omitempty).
	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	_, present := out["subscriptions"]
	require.False(t, present, "instance without subscriptions must omit the array")

	// @constraint: seed one mounting row and one failed row with a reason — the two
	// states the operator most needs to distinguish.
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
