// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// templates_test.go — coverage for the new POST /templates state
// machine + tag interactions per the 2026-05-01 control-plane spec
// §1.4 / §1.5. Drives a real testcontainer-backed Postgres + the chi
// router as a black box.
package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// templateBodyWithTag wraps validTemplateBody and attaches a tag.
func templateBodyWithTag(name, tag string) map[string]any {
	body := validTemplateBody(name)
	body["tag"] = tag
	return body
}

// TestTemplateRegister_ExecutorAccepted confirms that a template
// referencing an executor declared in AppDeps.Executors registers
// successfully (positive case for the validator's ExecutorDeclared
// hook).
func TestTemplateRegister_ExecutorAccepted(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// validTemplateBody references "worker" which is wired into
	// newHarness's AppDeps.Executors.
	body := validTemplateBody("exec-ok-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
}

// TestTemplateRegister_RejectsUnknownExecutor confirms that a
// template referencing an executor not declared in AppDeps.Executors
// fails registration with a 400 (negative case for the validator's
// ExecutorDeclared hook). Without the hook wired, this test would
// silently pass and an undeclared executor would reach the runtime.
func TestTemplateRegister_RejectsUnknownExecutor(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("exec-bad-" + uuid.NewString())
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["executor"] = "ghost-executor"
	spec["nodes"] = nodes

	status, _ := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status,
		"unknown-executor template must be rejected at register time")
}

// TestTemplateRegister_RejectsDelegateCycleOverRoute confirms a
// delegate-cycle template (main → g1 → g2 → g1) is rejected at the real
// POST /templates route with HTTP 400 and a validation_errors entry
// naming subgraph_recursion_unsupported. The in-memory validator's
// detectDelegateCycles is unit-tested in package node
// (TestCanonicalizeGraphs_RejectDelegateCycle); this test proves the
// rejection survives the full route → ValidateTemplate → 400-body path,
// not just the in-memory validator call. Each delegating node uses
// `delegate:` (not `executor:`) so the executor-declared hook does not
// fire first and mask the cycle.
func TestTemplateRegister_RejectsDelegateCycleOverRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// graphs: main → g1 → g2 → g1, mirroring the struct cycle in
	// node.TestCanonicalizeGraphs_RejectDelegateCycle. Each non-main
	// graph declares entry/exit and its exit subscribes to its entry on
	// terminal/*.
	reqBody := map[string]any{
		"spec": map[string]any{
			"name":                  "delegate-cycle-" + uuid.NewString(),
			"version":               "1",
			"frame_resolution_mode": "coalesce",
			"graphs": []map[string]any{
				{
					"name": "main",
					"nodes": []map[string]any{
						{"type": "m", "delegate": "g1"},
					},
				},
				{
					"name":  "g1",
					"entry": "g1n",
					"exit":  "g1x",
					"nodes": []map[string]any{
						{"type": "g1n", "delegate": "g2"},
						{"type": "g1x", "subscribes": []map[string]any{{"node": "g1n", "type": "terminal/*"}}},
					},
				},
				{
					"name":  "g2",
					"entry": "g2n",
					"exit":  "g2x",
					"nodes": []map[string]any{
						{"type": "g2n", "delegate": "g1"}, // cycle: g1 → g2 → g1
						{"type": "g2x", "subscribes": []map[string]any{{"node": "g2n", "type": "terminal/*"}}},
					},
				},
			},
		},
	}

	status, body := h.httpJSON(t, "POST", "/templates", reqBody)
	require.Equal(t, http.StatusBadRequest, status,
		"delegate-cycle template must be rejected at register time")

	rawErrs, ok := body["validation_errors"].([]any)
	require.True(t, ok, "response must carry a validation_errors array, got: %v", body)
	found := false
	for _, raw := range rawErrs {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if msg, _ := entry["msg"].(string); strings.Contains(msg, "subgraph_recursion_unsupported") {
			found = true
			break
		}
	}
	require.True(t, found,
		"a validation_errors entry must name subgraph_recursion_unsupported, got: %v", rawErrs)
}

// TestTemplateValidate_RejectsButDoesNotPersist confirms POST
// /templates/validate runs the full validation pipeline on a spec with
// a deliberate error and returns HTTP 200 with ok:false + non-empty
// validation_errors, while persisting nothing (a follow-up GET by the
// would-be hash 404s).
func TestTemplateValidate_RejectsButDoesNotPersist(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("validate-bad-" + uuid.NewString())
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["executor"] = "ghost-executor"
	spec["nodes"] = nodes

	// Count templates before validate so we can assert nothing changed.
	_, listBefore := h.httpJSON(t, "GET", "/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	status, out := h.httpJSON(t, "POST", "/templates/validate", body)
	require.Equal(t, http.StatusOK, status,
		"validate ran; verdict carried in the body, not the status code")
	require.Equal(t, false, out["ok"], "unknown-executor spec must lint as not-ok")
	errs, ok := out["validation_errors"].([]any)
	require.True(t, ok, "validation_errors must be present")
	require.NotEmpty(t, errs, "validation_errors must be non-empty for an invalid spec")

	// Nothing was persisted: template count is unchanged.
	_, listAfter := h.httpJSON(t, "GET", "/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"validate must not persist a template row")
}

// TestTemplateValidate_CleanSpecOk confirms a valid spec lints clean:
// HTTP 200, ok:true, empty errors — and still persists nothing.
func TestTemplateValidate_CleanSpecOk(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("validate-ok-" + uuid.NewString())

	_, listBefore := h.httpJSON(t, "GET", "/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	status, out := h.httpJSON(t, "POST", "/templates/validate", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["ok"], "valid spec must lint as ok")
	require.Empty(t, out["validation_errors"], "valid spec must have no errors")

	// Validate-only must not register the template.
	_, listAfter := h.httpJSON(t, "GET", "/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"validate must not persist even for a clean spec")
}

// TestTemplateRegister_Idempotent confirms the second POST with the
// same canonical spec returns 200 OK and the same template_id, and
// that the lifecycle fan-out runs exactly once across both POSTs (re-
// register must be a cheap no-op past the FanOutTemplateEvent helper's
// skip-if-already-at-target gate).
func TestTemplateRegister_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// validTemplateBody references no stores, so the fan-out call set
	// is empty even on first register. Use a body that does reference
	// a store so the count assertion has teeth.
	body := templateWithStoresAndLocks("idem-" + uuid.NewString())
	status1, out1 := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status1, out1)

	// Snapshot per-store call counts after the first register.
	storeNames := []string{"content", "topics-ring"}
	preCounts := make(map[string]int, len(storeNames))
	for _, name := range storeNames {
		s, ok := h.stores.Get(name)
		require.True(t, ok)
		fake, ok := s.(*storetest.Fake)
		require.True(t, ok)
		preCounts[name] = len(fake.Calls())
	}

	status2, out2 := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusOK, status2, out2)
	require.Equal(t, out1["template_id"], out2["template_id"])

	// Re-register must NOT trigger an additional fan-out call to any
	// of the referenced stores (the lifecycle row is already at
	// target state from the first register).
	for _, name := range storeNames {
		s, _ := h.stores.Get(name)
		fake := s.(*storetest.Fake)
		require.Equal(t, preCounts[name], len(fake.Calls()),
			"idempotent re-register must not fire OnTemplateRegistered again for %q", name)
	}
}

// TestTemplateRegister_TagAttachment confirms a tag can be attached at
// register time and persists through GET /tags.
func TestTemplateRegister_TagAttachment(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "tag-" + uuid.NewString()
	body := templateBodyWithTag("tag-attach-"+uuid.NewString(), tag)
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)

	tplID := out["template_id"].(string)
	listStatus, listOut := h.httpJSON(t, "GET", "/tags", nil)
	require.Equal(t, http.StatusOK, listStatus, listOut)
	tags := listOut["tags"].([]any)
	found := false
	for _, raw := range tags {
		entry := raw.(map[string]any)
		if entry["tag"] == tag {
			found = true
			require.Equal(t, tplID, entry["template_id"])
		}
	}
	require.True(t, found, "tag %q must show up in GET /tags", tag)
}

// TestTemplateRegister_RejectsHashShapedTag rejects a tag whose value
// looks like a content hash. Issue 6's fix.
func TestTemplateRegister_RejectsHashShapedTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	hashShape := "sha256-" + repeatHex("a", 64)
	body := templateBodyWithTag("hashy-"+uuid.NewString(), hashShape)
	status, _ := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status,
		"hash-shaped tag must be rejected at register time")
}

// TestTemplateDeploy_StateTransitions covers the four spec'd entry/
// exit transitions on POST /templates/{id}/deploy and …/undeploy.
func TestTemplateDeploy_StateTransitions(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("deploy-states-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)

	// registered → deployed.
	status, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	// deployed → deployed (idempotent no-op).
	status, out2 := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out2["no_op"])

	// deployed → undeployed.
	status, _ = h.httpJSON(t, "POST", "/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	// undeployed → undeployed (idempotent no-op).
	status, out3 := h.httpJSON(t, "POST", "/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out3["no_op"])

	// undeployed → deployed.
	status, _ = h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
}

// TestTemplateUndeploy_RefusedWithActiveInstances pins the spec §1.5
// guard: undeploying a template with active instances returns 409.
func TestTemplateUndeploy_RefusedWithActiveInstances(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("undeploy-refused-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	// Spawn an instance.
	status, _ = h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status)

	// Undeploy is now refused.
	status, _ = h.httpJSON(t, "POST", "/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusConflict, status)
}

// TestTemplateDelete_RefusedWhenDeployed: deleting a deployed template
// returns 409. Caller must undeploy first.
func TestTemplateDelete_RefusedWhenDeployed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("delete-deployed-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "DELETE", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusConflict, status)
}

// TestTemplateDelete_TagOnlyVsLastTag covers the tag-form delete:
// when other tags still point at the template, a tag-form delete only
// removes the tag; when this is the last tag, the template row goes
// too.
func TestTemplateDelete_TagOnlyVsLastTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag1 := "first-" + uuid.NewString()
	tag2 := "second-" + uuid.NewString()
	body := templateBodyWithTag("two-tags-"+uuid.NewString(), tag1)
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)

	// Add a second tag.
	status, _ := h.httpJSON(t, "POST", "/tags", map[string]any{
		"tag": tag2, "template": tplID,
	})
	require.Equal(t, http.StatusCreated, status)

	// Delete tag1: tag_only=true and the template must persist.
	status, deleteOut := h.httpJSON(t, "DELETE", "/templates/"+tag1, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, deleteOut["tag_only"])
	status, _ = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	// Delete tag2 (last tag): drops the template entirely.
	status, _ = h.httpJSON(t, "DELETE", "/templates/"+tag2, nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestTemplateDelete_DirectHashDropsAllTags: deleting by hash drops
// every tag pointing at the row in one transactional step.
func TestTemplateDelete_DirectHashDropsAllTags(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag1 := "alpha-" + uuid.NewString()
	tag2 := "beta-" + uuid.NewString()
	body := templateBodyWithTag("multi-tag-direct-"+uuid.NewString(), tag1)
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/tags", map[string]any{"tag": tag2, "template": tplID})
	require.Equal(t, http.StatusCreated, status)

	// Direct-hash delete drops all tags atomically with the template row.
	status, _ = h.httpJSON(t, "DELETE", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
	for _, tag := range []string{tag1, tag2} {
		listStatus, listOut := h.httpJSON(t, "GET", "/tags", nil)
		require.Equal(t, http.StatusOK, listStatus)
		tags := listOut["tags"].([]any)
		for _, raw := range tags {
			require.NotEqual(t, tag, raw.(map[string]any)["tag"],
				"tag %q must be cleaned up alongside the template", tag)
		}
	}
}

// TestInstanceCreate_RequiresDeployedTemplate enforces the spec §2.2
// state guard: instance creation against a 'registered' or
// 'undeployed' template returns 409. Drives the template through the
// full lifecycle (registered → deployed → undeployed) and asserts
// instance creation succeeds only at 'deployed'.
func TestInstanceCreate_RequiresDeployedTemplate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("not-deployed-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", body)
	tplID := out["template_id"].(string)

	// State is 'registered'.
	status, _ := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template": tplID, "instance_key": "ck-1",
	})
	require.Equal(t, http.StatusConflict, status,
		"instance creation against state='registered' must be refused")

	// Move to deployed → should succeed.
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, createdOut := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template": tplID, "instance_key": "ck-2",
	})
	require.Equal(t, http.StatusCreated, status, createdOut)
	instID := createdOut["instance_id"].(string)
	require.NotEmpty(t, instID)

	// Drive the instance to terminal so undeploy is permitted (the
	// active-instances guard would otherwise mask the test's intent).
	// Setting terminated_at directly bypasses the frame engine; this
	// test is targeted at the undeploy → instance-create state guard,
	// not at terminal-state detection.
	pgtest.ExecForTest(context.Background(), t, h.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)

	// Move to undeployed.
	undeployStatus, undeployOut := h.httpJSON(t, "POST", "/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, undeployStatus, undeployOut)

	// Instance creation against state='undeployed' returns 409.
	status, _ = h.httpJSON(t, "POST", "/instances", map[string]any{
		"template": tplID, "instance_key": "ck-3",
	})
	require.Equal(t, http.StatusConflict, status,
		"instance creation against state='undeployed' must be refused")
}

// newRefModeHarness boots a control-api harness with the registration-
// time reference-validation MODE set to `mode`, and wires
// ExecutorCapabilities so a CONSTRAINED provisioned executor advertises
// a schema declaring `count` with `minimum: 0`. Used by
// TestRegisterTemplate_RefMode to drive POST /templates through the real
// handleDeployTemplate under each mode.
//
// Two executors are modeled:
//   - "constrained" — declared in AppDeps.Executors (provisioned) and
//     advertising the constraining schema via ExecutorCapabilities, so a
//     node default of `count: -1` is a genuinely-invalid provisioned ref.
//   - the not-provisioned executor referenced by the test template is
//     absent from both Executors and ExecutorCapabilities (ok=false).
func newRefModeHarness(t *testing.T, mode node.RefValidationMode) (*harness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)

	const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

	capLog := shared.NewCapturingLogger()
	app := NewApp(AppDeps{
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Clock:         shared.SystemClock{},
		Logger:        capLog,
		Stores:        reg,
		LifecycleSubs: lcReg,
		// Only "constrained" is provisioned. The not-yet-provisioned
		// executor referenced by the test template is intentionally absent.
		Executors: map[string]ExecutorEntry{
			"constrained": {Transport: "grpc", Endpoint: "localhost:0"},
		},
		// The constrained executor's schema is visible; everything else
		// reports ok=false (schema not visible / not provisioned).
		ExecutorCapabilities: func(name string) ([]string, []string, []byte, bool) {
			if name == "constrained" {
				return nil, nil, []byte(constrainedSchema), true
			}
			return nil, nil, nil, false
		},
		// The operator-set registration-time reference-validation mode.
		RefValidationMode: mode,
	})
	srv := httptest.NewServer(app)
	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	return h, func() { srv.Close() }
}

// refModeTemplateNotProvisioned returns a wrapped POST /templates body
// for a single-node template referencing a not-yet-provisioned executor
// ("ghost-executor", absent from AppDeps.Executors / ExecutorCapabilities).
func refModeTemplateNotProvisioned(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "root", "executor": "ghost-executor"},
			},
		},
	}
}

// refModeTemplateProvisionedInvalid returns a wrapped POST /templates
// body for a template whose node references the PROVISIONED constrained
// executor with a default (`count: -1`) that violates its advertised
// schema (`minimum: 0`) — a genuinely-invalid provisioned reference.
func refModeTemplateProvisionedInvalid(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
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
									"default": -1,
								},
							},
						},
					},
				},
			},
		},
	}
}

// TestRegisterTemplate_RefMode pins the operator-set registration-time
// reference-validation MODE read from control-api config (story
// S-template-validation-ref-validation-mode), driven through the real
// POST /templates → handleDeployTemplate → node.ValidateTemplate path.
//
//   - mode all (default): a template referencing a not-yet-provisioned
//     executor is rejected with HTTP 400 carrying a missing-reference
//     validation_errors entry.
//   - mode available: that same registration SUCCEEDS (200/201) for the
//     not-provisioned ref, while a genuinely-invalid ref to a PROVISIONED
//     executor (a default below the executor schema's minimum) still 400s.
//   - mode none: registration SUCCEEDS (200/201) with no registration-time
//     reference validation, even for the provisioned-invalid ref.
//
// RED today: AppDeps has no RefValidationMode field and node has no
// RefValidationMode type, so this test does not compile against the
// current package — the gate command's `!` inverts that build failure to
// a pass. A later GREEN pass adds the field, the node-level mode, and
// stamps it onto RegistryHooks in validatorHooksFor + the config/env
// plumbing.
func TestRegisterTemplate_RefMode(t *testing.T) {
	t.Run("all: not-provisioned ref rejected with 400 missing-reference", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateAll)
		t.Cleanup(teardown)

		body := refModeTemplateNotProvisioned("refmode-all-" + uuid.NewString())
		status, out := h.httpJSON(t, "POST", "/templates", body)
		require.Equal(t, http.StatusBadRequest, status,
			"mode all must reject a not-yet-provisioned executor reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "response must carry validation_errors, got: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the missing reference")
	})

	t.Run("available: not-provisioned ref succeeds; provisioned-invalid ref still 400s", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateAvailable)
		t.Cleanup(teardown)

		// Not-provisioned ref: registration succeeds under `available`.
		okBody := refModeTemplateNotProvisioned("refmode-avail-ok-" + uuid.NewString())
		okStatus, okOut := h.httpJSON(t, "POST", "/templates", okBody)
		require.Equal(t, http.StatusCreated, okStatus,
			"mode available must accept a not-yet-provisioned executor reference; body: %v", okOut)

		// Genuinely-invalid provisioned ref: still rejected (count: -1 vs
		// the executor schema's minimum: 0).
		badBody := refModeTemplateProvisionedInvalid("refmode-avail-bad-" + uuid.NewString())
		badStatus, badOut := h.httpJSON(t, "POST", "/templates", badBody)
		require.Equal(t, http.StatusBadRequest, badStatus,
			"mode available must still reject a genuinely-invalid provisioned ref; body: %v", badOut)
		errs, ok := badOut["validation_errors"].([]any)
		require.True(t, ok, "response must carry validation_errors, got: %v", badOut)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
	})

	t.Run("none: no registration-time reference validation", func(t *testing.T) {
		h, teardown := newRefModeHarness(t, node.RefValidateNone)
		t.Cleanup(teardown)

		// Not-provisioned ref succeeds.
		okBody := refModeTemplateNotProvisioned("refmode-none-ghost-" + uuid.NewString())
		okStatus, okOut := h.httpJSON(t, "POST", "/templates", okBody)
		require.Equal(t, http.StatusCreated, okStatus,
			"mode none must accept a not-yet-provisioned executor reference; body: %v", okOut)

		// Even the provisioned-invalid ref registers clean under `none`:
		// registration-time reference validation is off entirely.
		invalidBody := refModeTemplateProvisionedInvalid("refmode-none-invalid-" + uuid.NewString())
		invalidStatus, invalidOut := h.httpJSON(t, "POST", "/templates", invalidBody)
		require.Equal(t, http.StatusCreated, invalidStatus,
			"mode none must perform no registration-time reference validation; body: %v", invalidOut)
	})
}
