// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func templateBodyWithTag(name, tag string) map[string]any {
	body := validTemplateBody(name)
	body["tag"] = tag
	return body
}

func TestTemplateRegister_ExecutorAccepted(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("exec-ok-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
}

func TestTemplateRegister_RejectsUnknownExecutor(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("exec-bad-" + uuid.NewString())
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["executor"] = "ghost-executor"
	spec["nodes"] = nodes

	status, _ := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status,
		"unknown-executor template must be rejected at register time")
}

func TestTemplateRegister_RejectsDelegateCycleOverRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	reqBody := map[string]any{
		"spec": map[string]any{
			"name":    "delegate-cycle-" + uuid.NewString(),
			"version": "1",
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
						{"type": "g1x", "subscribes": []map[string]any{{"node": "g1n", "type": "terminal/*", "force_upstream_refresh": false}}},
					},
				},
				{
					"name":  "g2",
					"entry": "g2n",
					"exit":  "g2x",
					"nodes": []map[string]any{
						{"type": "g2n", "delegate": "g1"},
						{"type": "g2x", "subscribes": []map[string]any{{"node": "g2n", "type": "terminal/*", "force_upstream_refresh": false}}},
					},
				},
			},
		},
	}

	status, body := h.httpJSON(t, "POST", "/v1/templates", reqBody)
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

func TestTemplateValidate_RejectsButDoesNotPersist(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("validate-bad-" + uuid.NewString())
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["executor"] = "ghost-executor"
	spec["nodes"] = nodes

	_, listBefore := h.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	status, out := h.httpJSON(t, "POST", "/v1/templates/validate", body)
	require.Equal(t, http.StatusOK, status,
		"validate ran; verdict carried in the body, not the status code")
	require.Equal(t, false, out["ok"], "unknown-executor spec must lint as not-ok")
	errs, ok := out["validation_errors"].([]any)
	require.True(t, ok, "validation_errors must be present")
	require.NotEmpty(t, errs, "validation_errors must be non-empty for an invalid spec")

	_, listAfter := h.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"validate must not persist a template row")
}

func TestTemplateValidate_CleanSpecOk(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("validate-ok-" + uuid.NewString())

	_, listBefore := h.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	status, out := h.httpJSON(t, "POST", "/v1/templates/validate", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["ok"], "valid spec must lint as ok")
	require.Empty(t, out["validation_errors"], "valid spec must have no errors")

	_, listAfter := h.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"validate must not persist even for a clean spec")
}

func TestTemplateRegister_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := templateWithClaimProducersAndLocks("idem-" + uuid.NewString())
	status1, out1 := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status1, out1)

	storeNames := []string{"content", "topics-ring"}
	preCounts := make(map[string]int, len(storeNames))
	for _, name := range storeNames {
		s, ok := h.stores.Get(name)
		require.True(t, ok)
		fake, ok := s.(*storetest.Fake)
		require.True(t, ok)
		preCounts[name] = len(fake.Calls())
	}

	status2, out2 := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusOK, status2, out2)
	require.Equal(t, out1["template_id"], out2["template_id"])

	for _, name := range storeNames {
		s, _ := h.stores.Get(name)
		fake := s.(*storetest.Fake)
		require.Equal(t, preCounts[name], len(fake.Calls()),
			"idempotent re-register must not fire OnTemplateRegistered again for %q", name)
	}
}

func TestTemplateRegister_TagAttachment(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "tag-" + uuid.NewString()
	body := templateBodyWithTag("tag-attach-"+uuid.NewString(), tag)
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)

	tplID := out["template_id"].(string)
	listStatus, listOut := h.httpJSON(t, "GET", "/v1/tags", nil)
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

func TestTemplateRegister_RejectsHashShapedTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	hashShape := "sha256-" + repeatHex("a", 64)
	body := templateBodyWithTag("hashy-"+uuid.NewString(), hashShape)
	status, _ := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status,
		"hash-shaped tag must be rejected at register time")
}

func TestTemplateRegister_MovesExistingTagOnFreshInsertWhenCallerHasTagSet(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "shared-tag-" + uuid.NewString()
	firstBody := templateBodyWithTag("tag-conflict-first-"+uuid.NewString(), tag)
	status, first := h.httpJSON(t, "POST", "/v1/templates", firstBody)
	require.Equal(t, http.StatusCreated, status, first)

	secondBody := templateBodyWithTag("tag-conflict-second-"+uuid.NewString(), tag)
	status, second := h.httpJSON(t, "POST", "/v1/templates", secondBody)
	require.Equal(t, http.StatusCreated, status, second,
		"a caller holding tag:set (the harness's anonymous-mode identity has full permissions) may move an existing tag via register-with-tag")
	secondID := second["template_id"].(string)

	getStatus, getOut := h.httpJSON(t, "GET", "/v1/templates/"+tag, nil)
	require.Equal(t, http.StatusOK, getStatus, getOut)
	require.Equal(t, secondID, getOut["id"],
		"the tag must now resolve to the newly-registered template")
}

func TestTemplateRegister_MovesExistingTagOnIdempotentReRegisterWhenCallerHasTagSet(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "shared-tag-idem-" + uuid.NewString()
	firstBody := templateBodyWithTag("tag-conflict-idem-first-"+uuid.NewString(), tag)
	status, first := h.httpJSON(t, "POST", "/v1/templates", firstBody)
	require.Equal(t, http.StatusCreated, status, first)

	secondBody := validTemplateBody("tag-conflict-idem-second-" + uuid.NewString())
	status, second := h.httpJSON(t, "POST", "/v1/templates", secondBody)
	require.Equal(t, http.StatusCreated, status, second)
	secondID := second["template_id"].(string)

	secondWithTag := map[string]any{
		"spec": specOf(secondBody),
		"tag":  tag,
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", secondWithTag)
	require.Equal(t, http.StatusOK, status, out,
		"idempotent re-register with a tag that points elsewhere must move it when the caller holds tag:set")

	getStatus, getOut := h.httpJSON(t, "GET", "/v1/templates/"+tag, nil)
	require.Equal(t, http.StatusOK, getStatus, getOut)
	require.Equal(t, secondID, getOut["id"])
}

func TestTemplateRegister_IdempotentReRegisterWithSameTagOK(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "stable-tag-" + uuid.NewString()
	body := templateBodyWithTag("tag-stable-"+uuid.NewString(), tag)
	status, first := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, first)

	status, second := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusOK, status, second)
	require.Equal(t, first["template_id"], second["template_id"])
}

func TestTemplateRegister_RejectsComposeReservedTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := templateBodyWithTag("compose-reserved-"+uuid.NewString(), "compose:"+uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out,
		"register-with-tag must enforce the compose: reserved-prefix guard, same as POST /v1/tags")
}

func TestTemplateDeploy_StateTransitions(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("deploy-states-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)

	status, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, out2 := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out2["no_op"])

	status, _ = h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, out3 := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out3["no_op"])

	status, _ = h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
}

func TestTemplateUndeploy_RefusedWithActiveInstances(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("undeploy-refused-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status)

	status, _ = h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusConflict, status)
}

func TestTemplateDelete_RefusedWhenDeployed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("delete-deployed-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "DELETE", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusConflict, status)
}

func TestTemplateDelete_TagOnlyVsLastTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag1 := "first-" + uuid.NewString()
	tag2 := "second-" + uuid.NewString()
	body := templateBodyWithTag("two-tags-"+uuid.NewString(), tag1)
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)

	status, _ := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag": tag2, "template": tplID,
	})
	require.Equal(t, http.StatusCreated, status)

	status, deleteOut := h.httpJSON(t, "DELETE", "/v1/templates/"+tag1, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, deleteOut["tag_only"])
	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "DELETE", "/v1/templates/"+tag2, nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestTemplateDelete_DirectHashDropsAllTags(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag1 := "alpha-" + uuid.NewString()
	tag2 := "beta-" + uuid.NewString()
	body := templateBodyWithTag("multi-tag-direct-"+uuid.NewString(), tag1)
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)
	status, _ := h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag2, "template": tplID})
	require.Equal(t, http.StatusCreated, status)

	status, _ = h.httpJSON(t, "DELETE", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
	for _, tag := range []string{tag1, tag2} {
		listStatus, listOut := h.httpJSON(t, "GET", "/v1/tags", nil)
		require.Equal(t, http.StatusOK, listStatus)
		tags := listOut["tags"].([]any)
		for _, raw := range tags {
			require.NotEqual(t, tag, raw.(map[string]any)["tag"],
				"tag %q must be cleaned up alongside the template", tag)
		}
	}
}

func TestInstanceCreate_RequiresDeployedTemplate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("not-deployed-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)

	status, _ := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template": tplID, "instance_key": "ck-1",
	})
	require.Equal(t, http.StatusConflict, status,
		"instance creation against state='registered' must be refused")

	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, createdOut := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template": tplID, "instance_key": "ck-2",
	})
	require.Equal(t, http.StatusCreated, status, createdOut)
	instID := createdOut["instance_id"].(string)
	require.NotEmpty(t, instID)

	pgtest.ExecForTest(context.Background(), t, h.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)

	undeployStatus, undeployOut := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/undeploy", map[string]any{})
	require.Equal(t, http.StatusOK, undeployStatus, undeployOut)

	status, _ = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template": tplID, "instance_key": "ck-3",
	})
	require.Equal(t, http.StatusConflict, status,
		"instance creation against state='undeployed' must be refused")
}

func newConstrainedExecutorHarness(t *testing.T) (*harness, func()) {
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
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Clock:          shared.SystemClock{},
		Logger:         capLog,
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
		Executors: map[string]ExecutorEntry{
			"constrained": {Transport: "grpc", Endpoint: "localhost:0"},
		},
		ExecutorCapabilities: func(name string) ([]string, []string, []byte, bool) {
			if name == "constrained" {
				return nil, nil, []byte(constrainedSchema), true
			}
			return nil, nil, nil, false
		},
		AuthState: &AuthState{
			Tables:   d.Tables(),
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   capLog,
		},
	})
	srv := httptest.NewServer(app)
	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	return h, func() { srv.Close() }
}

func refModeTemplateNotProvisioned(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "root", "executor": "ghost-executor"},
			},
		},
	}
}

func refModeTemplateProvisionedInvalid(name string) map[string]any {
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

func TestRegisterTemplate_ReferenceValidationStrict(t *testing.T) {
	t.Run("not-provisioned ref rejected with 400 missing-reference", func(t *testing.T) {
		h, teardown := newConstrainedExecutorHarness(t)
		t.Cleanup(teardown)

		body := refModeTemplateNotProvisioned("strict-ghost-" + uuid.NewString())
		status, out := h.httpJSON(t, "POST", "/v1/templates", body)
		require.Equal(t, http.StatusBadRequest, status,
			"registration must reject a not-yet-provisioned executor reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "response must carry validation_errors, got: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the missing reference")
	})

	t.Run("provisioned-invalid ref rejected with 400 schema violation", func(t *testing.T) {
		h, teardown := newConstrainedExecutorHarness(t)
		t.Cleanup(teardown)

		badBody := refModeTemplateProvisionedInvalid("strict-invalid-" + uuid.NewString())
		badStatus, badOut := h.httpJSON(t, "POST", "/v1/templates", badBody)
		require.Equal(t, http.StatusBadRequest, badStatus,
			"registration must reject a schema-invalid provisioned ref; body: %v", badOut)
		errs, ok := badOut["validation_errors"].([]any)
		require.True(t, ok, "response must carry validation_errors, got: %v", badOut)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
	})
}

func warningsContainAdvisory(t *testing.T, out map[string]any) bool {
	t.Helper()
	warns, ok := out["validation_warnings"].([]any)
	if !ok {
		return false
	}
	for _, w := range warns {
		entry, ok := w.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := entry["msg"].(string)
		if msg == "" {
			msg, _ = entry["message"].(string)
		}
		if strings.Contains(msg, "acquire/unavailable") {
			return true
		}
	}
	return false
}

func storesTemplateWithoutAcquirePolicy(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"nodes": []map[string]any{
				{
					"type":     "claim-topic",
					"executor": "worker",
					"claim_producers": []map[string]any{
						{"name": "topics-ring", "selector": "@queue", "intent": "rw"},
					},
				},
			},
		},
	}
}

func TestTemplateRegister_StaticWarningSurfaced(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := storesTemplateWithoutAcquirePolicy("static-warn-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
	require.True(t, warningsContainAdvisory(t, out),
		"register response must surface the static validator's acquire/unavailable advisory in validation_warnings; got: %v", out)
}

func TestTemplateRegister_StaticWarningAsErrorsRejects(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, listBefore := h.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := 0
	if l, ok := listBefore["templates"].([]any); ok {
		beforeCount = len(l)
	}

	body := storesTemplateWithoutAcquirePolicy("static-warn-rej-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates?warnings_as_errors=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Equal(t, true, out["warnings_as_errors"])
	require.True(t, warningsContainAdvisory(t, out),
		"rejection body must carry the static advisory in validation_warnings; got: %v", out)

	_, listAfter := h.httpJSON(t, "GET", "/v1/templates", nil)
	afterCount := 0
	if l, ok := listAfter["templates"].([]any); ok {
		afterCount = len(l)
	}
	require.Equal(t, beforeCount, afterCount,
		"warnings_as_errors rejection must not persist a template row")
}

func TestTemplateValidate_StaticWarningSurfaced(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := storesTemplateWithoutAcquirePolicy("static-warn-validate-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates/validate", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["ok"], "warnings alone must not flip ok without the flag: %v", out)
	require.True(t, warningsContainAdvisory(t, out),
		"validate response must surface the static advisory in validation_warnings; got: %v", out)

	status, out = h.httpJSON(t, "POST", "/v1/templates/validate?warnings_as_errors=true", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, false, out["ok"],
		"warnings_as_errors=true must flip the validate verdict on a static advisory: %v", out)
	require.True(t, warningsContainAdvisory(t, out), "advisory must still be listed: %v", out)
}
