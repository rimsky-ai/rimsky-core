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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/locks/storetest"
	pgtest "github.com/fallguyconsulting/rimsky/internal/pgmigrate"
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
