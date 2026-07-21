// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
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

func TestTerminateInstance_ConcurrentCallsAppendExactlyOneEvent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-race-"+uuid.NewString())

	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			reqBody, merr := json.Marshal(map[string]any{"reason": fmt.Sprintf("racer-%d", i)})
			if merr != nil {
				errs[i] = merr
				return
			}
			req, rerr := http.NewRequest("POST", h.srv.URL+"/v1/instances/"+inst.ID.String()+"/terminate", bytes.NewReader(reqBody))
			if rerr != nil {
				errs[i] = rerr
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, derr := http.DefaultClient.Do(req)
			if derr != nil {
				errs[i] = derr
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	for i := range statuses {
		require.NoError(t, errs[i], "racer %d request error", i)
		require.Equal(t, http.StatusOK, statuses[i], "racer %d must receive 200", i)
	}

	status, out := h.httpJSON(t, "GET",
		fmt.Sprintf("/v1/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	events, _ := out["events"].([]any)
	require.Len(t, events, 1, "concurrent terminate calls racing on the same instance must append exactly one instance_terminated event")
}

func TestTerminateInstance_DryRunOnAlreadyTerminatedHonorsDryRun(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-dryrun-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate?dry_run=true", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["dry_run"],
		"terminating an already-terminated instance under dry-run must still return the dry-run envelope, not the real instance item: %v", out)
	_, hasIntent := out["would_have_terminated"]
	require.True(t, hasIntent, "expected would_have_terminated intent in dry-run response: %v", out)
}

func TestPauseInstance_DryRunHonorsAlreadyPausedState(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "pause-dryrun-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/pause", nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/pause?dry_run=true", nil)
	require.Equal(t, http.StatusConflict, status, out,
		"dry-run pause on an already-paused instance must report the same 409 the real call would return, not a fake success")
}

func TestResumeInstance_DryRunHonorsNotPausedState(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "resume-dryrun-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/resume?dry_run=true", nil)
	require.Equal(t, http.StatusConflict, status, out,
		"dry-run resume on a not-paused instance must report the same 409 the real call would return, not a fake success")
}

func TestListInstances_RejectsUnrecognizedActiveValue(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/v1/instances?active=TRUE", nil)
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.httpJSON(t, "GET", "/v1/instances?active=garbage", nil)
	require.Equal(t, http.StatusBadRequest, status, out)

	for _, v := range []string{"true", "false", "1", "0"} {
		status, out = h.httpJSON(t, "GET", "/v1/instances?active="+v, nil)
		require.Equal(t, http.StatusOK, status, "active=%s must be accepted: %v", v, out)
	}
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
	t.Run("rejects: static count:-1 violates the executor schema's minimum:0 at registration", func(t *testing.T) {
		h, teardown := newConstrainedExecutorHarness(t)
		t.Cleanup(teardown)

		body := refModeTemplateProvisionedInvalid("static-gate-bad-" + uuid.NewString())
		status, out := h.httpJSON(t, "POST", "/v1/templates", body)
		require.Equal(t, http.StatusBadRequest, status,
			"registration must reject a static-config violation under unconditional strict validation; body: %v", out)
		errText := strings.ToLower(fmt.Sprint(out["error"]) + " " + fmt.Sprint(out["validation_errors"]))
		require.Contains(t, errText, "count",
			"rejection must name the offending attribute `count`; body: %v", out)
		require.Contains(t, errText, "minimum",
			"rejection must cite the `minimum` value-constraint violation (a genuine value check, not a missing/extra-attribute surface error); body: %v", out)
	})

	t.Run("admits: a well-formed instance of the same executor returns 201 and persists", func(t *testing.T) {
		h, teardown := newConstrainedExecutorHarness(t)
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

// @concept: instance
func TestCreateInstance_MessageQueueModeOperatorOverride(t *testing.T) {
	t.Parallel()
	h, teardown := newConstrainedExecutorHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeployBody(t, h, refModeTemplateProvisionedValid("mqm-override-"+uuid.NewString()))

	t.Run("omitted override inherits the template default", func(t *testing.T) {
		status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-" + uuid.NewString(),
		})
		require.Equal(t, http.StatusCreated, status, out)
		instID, _ := out["instance_id"].(string)
		require.NotEmpty(t, instID)

		getStatus, getOut := h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
		require.Equal(t, http.StatusOK, getStatus, getOut)
		require.Equal(t, "backlog", getOut["message_queue_mode"],
			"an instance created without an override must inherit the template's default queue mode")
	})

	t.Run("operator override at creation wins over the template default", func(t *testing.T) {
		status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
			"template":           tplID,
			"instance_key":       "ck-" + uuid.NewString(),
			"message_queue_mode": "coalesce",
		})
		require.Equal(t, http.StatusCreated, status, out)
		instID, _ := out["instance_id"].(string)
		require.NotEmpty(t, instID)

		getStatus, getOut := h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
		require.Equal(t, http.StatusOK, getStatus, getOut)
		require.Equal(t, "coalesce", getOut["message_queue_mode"],
			"an explicit message_queue_mode on the create request must override the template default on the persisted instance")
	})

	t.Run("invalid override is rejected before provisioning", func(t *testing.T) {
		before := instanceCountForTemplate(t, h, tplID)
		status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
			"template":           tplID,
			"instance_key":       "ck-" + uuid.NewString(),
			"message_queue_mode": "bogus",
		})
		require.Equal(t, http.StatusBadRequest, status, out)
		require.Equal(t, before, instanceCountForTemplate(t, h, tplID),
			"a rejected message_queue_mode must not provision an instance")
	})
}

func TestGetInstance_SurfacesSubscriptionStates(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("inst-subs-" + uuid.NewString())
	tplID := registerAndDeployBody(t, h, tplBody)

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

			State: persistence.PublisherSubscriptionStateMounting,
		}); err != nil {
			return err
		}
		return h.persist.PublisherSubscriptions().Insert(ctx, tx, persistence.PublisherSubscriptionRow{
			ID:             failedID,
			InstanceID:     shared.UUID(instUUID),
			PublisherName:  "sensor-beta",
			Kind:           "http",
			ResolvedConfig: []byte(`{}`),

			State:         persistence.PublisherSubscriptionStateFailed,
			FailureReason: `publisher "sensor-beta" is not registered`,
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

func TestDeleteInstance_NonTerminatedReturns409(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "del-live-"+uuid.NewString())

	status, out := h.httpJSON(t, "DELETE", "/v1/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusConflict, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "not in terminal state")

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out, "the rejected delete must not have removed the instance")
}

func TestDeleteInstance_DryRunUsesDeleteIntent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "del-dryrun-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "DELETE", "/v1/instances/"+inst.ID.String()+"?dry_run=true", nil)
	require.Equal(t, http.StatusOK, status, out)
	_, hasDeleteIntent := out["would_have_deleted_instance"]
	require.True(t, hasDeleteIntent,
		"DELETE dry-run must use the would_have_deleted_instance idiom shared with tags/assets/breakpoints: %v", out)
	_, hasTerminateIntent := out["would_have_terminated"]
	require.False(t, hasTerminateIntent,
		"DELETE dry-run must not reuse the terminate handler's would_have_terminated intent: %v", out)
}

func TestDeleteInstance_PurgesRunScopeLifecycleIdempotencyRows(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("del-runscope-purge-"+uuid.NewString()))
	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, err := uuid.Parse(out["instance_id"].(string))
	require.NoError(t, err)

	rootScopeID := uuid.New()
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: rootScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		msgID := uuid.New()
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		_, err := h.persist.Frames().InsertRunningFrame(ctx, instID, msgID, rootScopeID, tx)
		return err
	}))

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Instances().MarkTerminated(ctx, instID, tx)
	}))

	status, out = h.httpJSON(t, "DELETE", "/v1/instances/"+instID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.LifecycleIdempotency().Get(ctx,
			"topics-ring", persistence.LifecycleIdempotencyScopeRunScope, rootScopeID.String(), tx)
		require.NoError(t, err)
		require.Nil(t, row,
			"run-scope-scoped lifecycle idempotency row must be purged once the owning instance is hard-deleted, "+
				"otherwise it leaks permanently since the run_scope row itself is cascade-deleted with the instance")
		return nil
	}))
}

func TestCreateInstance_IdempotentRetryFansOutExistingParamsNotRequestParams(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplID := registerAndDeployBody(t, h, templateWithClaimProducersAndLocks("create-idem-params-"+uuid.NewString()))
	ck := "ck-" + uuid.NewString()

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
		"params":       map[string]any{"region": "us-east"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID := out["instance_id"].(string)

	cp, ok := h.stores.Get("content")
	require.True(t, ok)
	contentFake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.LifecycleIdempotency().Delete(ctx,
			"content", persistence.LifecycleIdempotencyScopeInstance, instID, tx)
	}))
	contentFake.Reset()

	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
		"params":       map[string]any{"region": "us-west"},
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, instID, out["instance_id"])

	calls := contentFake.Calls()
	var createdCall *storetest.FakeCall
	for i := range calls {
		if calls[i].Verb == "on_instance_created" {
			createdCall = &calls[i]
		}
	}
	require.NotNil(t, createdCall,
		"the peer with a missing lifecycle row must still receive an OnInstanceCreated re-dispatch on idempotent retry")
	require.JSONEq(t, `{"region":"us-east"}`, string(createdCall.Params),
		"the re-dispatch to a peer recovering from a missing lifecycle row must carry the EXISTING instance's "+
			"stored params, not the retry request's params")
}

func seedTerminatedInstanceWithoutTemplate(t *testing.T, h *harness, tag string) persistence.InstanceRow {
	t.Helper()
	ctx := context.Background()
	inst := seedInstance(t, h, tag)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+inst.ID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)

	pgdbtest.ExecForTest(ctx, t, h.driver,
		`ALTER TABLE rimsky_instances DROP CONSTRAINT IF EXISTS rimsky_instances_template_hash_fkey`)
	pgdbtest.ExecForTest(ctx, t, h.driver,
		`DELETE FROM rimsky_templates WHERE id = $1`, inst.TemplateHash)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		require.NoError(t, err)
		require.Nil(t, row, "template must be gone to exercise the missing-template fallback path")
		return nil
	}))
	return inst
}

func TestDeleteInstance_MissingTemplateFallsBackToLifecycleRows(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedTerminatedInstanceWithoutTemplate(t, h, "del-notpl-"+uuid.NewString())

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "content",
			ScopeKind:         persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:           inst.ID.String(),
			State:             persistence.LifecycleIdempotencyStateCreated,
		}, tx); err != nil {
			return err
		}
		return h.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "ghost-store",
			ScopeKind:         persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:           inst.ID.String(),
			State:             persistence.LifecycleIdempotencyStateCreated,
		}, tx)
	}))

	status, out := h.httpJSON(t, "DELETE", "/v1/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["deleted"])

	cp, ok := h.stores.Get("content")
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	calls := fake.Calls()
	require.Len(t, calls, 1, "the known store must receive exactly one OnInstanceTerminated fan-out call")
	require.Equal(t, "on_instance_terminated", calls[0].Verb)
	require.Equal(t, inst.ID.String(), calls[0].InstanceID)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.LifecycleIdempotency().Get(ctx,
			"content", persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx)
		require.NoError(t, err)
		require.Nil(t, row, "known-store lifecycle row must be deleted after a successful fan-out")
		row, err = h.persist.LifecycleIdempotency().Get(ctx,
			"ghost-store", persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx)
		require.NoError(t, err)
		require.Nil(t, row, "unregistered-store lifecycle row must be deleted by the unknown-subscriber branch")
		return nil
	}))
}

func TestDeleteInstance_MissingTemplateLifecycleFailureReturns500(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedTerminatedInstanceWithoutTemplate(t, h, "del-notpl-fail-"+uuid.NewString())

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "content",
			ScopeKind:         persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:           inst.ID.String(),
			State:             persistence.LifecycleIdempotencyStateCreated,
		}, tx)
	}))

	cp, ok := h.stores.Get("content")
	require.True(t, ok)
	fake, ok := cp.(*storetest.Fake)
	require.True(t, ok)
	fake.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_instance_terminated" {
			return errors.New("simulated lifecycle failure")
		}
		return nil
	}

	status, out := h.httpJSON(t, "DELETE", "/v1/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusInternalServerError, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "simulated lifecycle failure")

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.LifecycleIdempotency().Get(ctx,
			"content", persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx)
		require.NoError(t, err)
		require.NotNil(t, row, "lifecycle row must survive a failed fan-out so it can be retried")
		return nil
	}))
}

func TestCreateInstance_NodeListingIncludesMaterializedReceiversButNodeCountDoesNot(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeployBody(t, h, validTemplateBody("msg-receivers-"+uuid.NewString()))

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	require.EqualValues(t, 2, out["node_count"],
		"node_count must report the user-declared node count (root, child), excluding materialized message-receiver nodes")
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)

	seenTypes := map[string]int{}
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		nt, _ := m["node_type"].(string)
		seenTypes[nt]++
	}
	require.Len(t, nodes, 4,
		"node listing must include the 2 user-declared nodes plus the 2 materialized message-receiver nodes (empty-type + system/invalidate); got %v", seenTypes)
	require.Equal(t, 1, seenTypes["root"])
	require.Equal(t, 1, seenTypes["child"])
	require.Equal(t, 1, seenTypes[""],
		"materialized empty-type message-receiver node must be present in the node listing")
	require.Equal(t, 1, seenTypes["system/invalidate"],
		"materialized message-receiver node for the declared message type must be present in the node listing")
}
