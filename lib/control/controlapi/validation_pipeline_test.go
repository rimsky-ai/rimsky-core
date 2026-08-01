// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type fakeValidator struct {
	name           string
	supportedRoles []string

	mu        sync.Mutex
	errs      []runtime.ValidationFinding
	warns     []runtime.ValidationFinding
	rpcErr    error
	executor  int
	producer  int
	publisher int
	lifecycle int
}

func (f *fakeValidator) Name() string             { return f.name }
func (f *fakeValidator) SupportedRoles() []string { return f.supportedRoles }

func (f *fakeValidator) ValidateExecutor(_ context.Context, _ runtime.ValidateExecutorInput) (runtime.ValidationOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executor++
	return runtime.ValidationOutcome{Errors: f.errs, Warnings: f.warns}, f.rpcErr
}
func (f *fakeValidator) ValidateClaimProducer(_ context.Context, _ runtime.ValidateClaimProducerInput) (runtime.ValidationOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.producer++
	return runtime.ValidationOutcome{Errors: f.errs, Warnings: f.warns}, f.rpcErr
}
func (f *fakeValidator) ValidatePublisher(_ context.Context, _ runtime.ValidatePublisherInput) (runtime.ValidationOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publisher++
	return runtime.ValidationOutcome{Errors: f.errs, Warnings: f.warns}, f.rpcErr
}
func (f *fakeValidator) ValidateLifecycleSubscriber(_ context.Context, _ runtime.ValidateLifecycleSubscriberInput) (runtime.ValidationOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lifecycle++
	return runtime.ValidationOutcome{Errors: f.errs, Warnings: f.warns}, f.rpcErr
}

func (f *fakeValidator) ExecutorCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.executor
}

func (f *fakeValidator) ProducerCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.producer
}

func (f *fakeValidator) PublisherCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.publisher
}

func (f *fakeValidator) LifecycleCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lifecycle
}

type fakeValidatorRegistry struct {
	byName map[string]runtime.ValidationClient
}

func newFakeValidatorRegistry(validators ...*fakeValidator) *fakeValidatorRegistry {
	out := &fakeValidatorRegistry{byName: map[string]runtime.ValidationClient{}}
	for _, v := range validators {
		out.byName[v.Name()] = v
	}
	return out
}

func (r *fakeValidatorRegistry) Get(name string) (runtime.ValidationClient, bool) {
	c, ok := r.byName[name]
	return c, ok
}

func (r *fakeValidatorRegistry) All() []runtime.ValidationClient {
	out := make([]runtime.ValidationClient, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}

func newValidatorHarness(t *testing.T, vr *fakeValidatorRegistry, policy runtime.UnreachableValidatorPolicy) (*harness, func()) {
	t.Helper()
	return newAppHarness(t, func(deps *AppDeps) {
		deps.Validators = vr
		deps.UnreachableValidatorPolicy = policy
	})
}

func TestValidationPipeline_RejectsOnError(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		errs: []runtime.ValidationFinding{{
			Class:   "attribute_shape_invalid",
			Message: "missing required field foo",
			Path:    "/executor/attributes/foo",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	_, listBefore := vh.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	body := validTemplateBody("vp-err-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.ExecutorCalls(), 1)

	found := false
	for _, raw := range errs {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if e["path"] == "/executor/attributes/foo" && strings.Contains(fmt.Sprint(e["msg"]), "missing required field foo") {
			found = true
		}
	}
	require.True(t, found,
		"the fake validator's finding (path and message) must survive to the wire unmodified, got: %v", errs)

	_, listAfter := vh.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"a pipeline-rejected template must not be persisted")
}

func TestValidationPipeline_DryRunRegisterStillRunsPipeline(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		errs: []runtime.ValidationFinding{{
			Class:   "attribute_shape_invalid",
			Message: "missing required field foo",
			Path:    "/executor/attributes/foo",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	_, listBefore := vh.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	body := validTemplateBody("vp-dryrun-err-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates?dry_run=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline",
		"a dry-run register must still surface the pipeline rejection, proving validation ran before the dry-run gate")
	require.NotContains(t, out, "would_have_registered",
		"a validation-rejected dry-run register must NOT return a would_have_registered envelope — "+
			"a canned success here would mean the dry-run branch ran BEFORE the validation pipeline")
	require.GreaterOrEqual(t, vfake.ExecutorCalls(), 1,
		"the validation-protocol's checks must actually fire against the advertising executor under dry-run")

	_, listAfter := vh.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"a pipeline-rejected dry-run register must not be persisted")
}

func TestValidationPipeline_PassesOnWarningsOnly(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		warns: []runtime.ValidationFinding{{
			Class:   "attribute_deprecated_field",
			Message: "field bar is deprecated",
			Path:    "/executor/attributes/bar",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-warn-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])

	warns, ok := out["validation_warnings"].([]any)
	require.True(t, ok, "response must carry validation_warnings, got: %v", out)
	found := false
	for _, raw := range warns {
		w, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if w["path"] == "/executor/attributes/bar" && strings.Contains(fmt.Sprint(w["msg"]), "field bar is deprecated") {
			found = true
		}
	}
	require.True(t, found,
		"a warnings-only pipeline outcome must still surface the warning in the response, got: %v", warns)
}

func TestValidationPipeline_ClaimProducerRoleHonoredAtRegistration(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "topics-ring",
		supportedRoles: []string{"claim_producer"},
		errs: []runtime.ValidationFinding{{
			Class:   "claim_producer_config_invalid",
			Message: "bad claim producer config",
			Path:    "/claim_producers/topics-ring",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := templateWithClaimProducersAndLocks("vp-producer-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.ProducerCalls(), 1,
		"registering a template with a claim_producer whose validator advertises the claim_producer role must invoke ValidateClaimProducer")
	require.Equal(t, 0, vfake.ExecutorCalls())
}

func TestValidationPipeline_PublisherRoleHonoredAtRegistration(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "pub-1",
		supportedRoles: []string{"publisher"},
		errs: []runtime.ValidationFinding{{
			Class:   "publisher_config_invalid",
			Message: "bad publisher config",
			Path:    "/publisher/config",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-pub-" + uuid.NewString())
	spec := specOf(body)
	spec["publishers"] = []map[string]any{
		{
			"name":         "pub-1",
			"kind":         "http",
			"config":       map[string]any{},
			"message_type": "system/invalidate",
		},
	}
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.PublisherCalls(), 1,
		"registering a template with a publisher whose validator advertises the publisher role must invoke ValidatePublisher")
	require.Equal(t, 0, vfake.ExecutorCalls())
}

func TestValidationPipeline_LifecycleSubscriberRoleHonoredAtRegistration(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "content",
		supportedRoles: []string{"lifecycle_subscriber"},
		errs: []runtime.ValidationFinding{{
			Class:   "lifecycle_subscriber_rejected",
			Message: "subscriber refuses this template",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-lifecycle-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.LifecycleCalls(), 1,
		"registering any template must invoke ValidateLifecycleSubscriber template-wide on every validator advertising the lifecycle_subscriber role")
	require.Equal(t, 0, vfake.ExecutorCalls())
}

func TestValidationPipeline_WarningsAsErrorsRejects(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		warns: []runtime.ValidationFinding{{
			Class: "attribute_deprecated_field",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	_, listBefore := vh.httpJSON(t, "GET", "/v1/templates", nil)
	beforeCount := len(listBefore["templates"].([]any))

	body := validTemplateBody("vp-waserrs-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates?warnings_as_errors=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Equal(t, true, out["warnings_as_errors"])

	warns, ok := out["validation_warnings"].([]any)
	require.True(t, ok, "the rejection body must still carry the warning findings, got: %v", out)
	found := false
	for _, raw := range warns {
		w, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.Contains(fmt.Sprint(w["path"]), "worker") {
			found = true
		}
	}
	require.True(t, found, "the triggering warning must be present in the rejection body, got: %v", warns)

	_, listAfter := vh.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, beforeCount, len(listAfter["templates"].([]any)),
		"a warnings_as_errors rejection must not persist the template")
}

func TestValidationPipeline_ValidateEndpointMergesPipelineErrors(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		errs: []runtime.ValidationFinding{{
			Class:   "attribute_shape_invalid",
			Message: "missing required field foo",
			Path:    "/executor/attributes/foo",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-validate-err-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates/validate", body)
	require.Equal(t, http.StatusOK, status,
		"validate ran; verdict carried in the body, not the status code")
	require.Equal(t, false, out["ok"],
		"a template that the pipeline rejects at register time must also lint as not-ok at /validate")
	errs, ok := out["validation_errors"].([]any)
	require.True(t, ok, "validation_errors must be present")
	require.NotEmpty(t, errs)
	found := false
	for _, raw := range errs {
		e, ok := raw.(map[string]any)
		if ok && strings.Contains(fmt.Sprint(e["msg"]), "missing required field foo") {
			found = true
		}
	}
	require.True(t, found, "pipeline error must merge into /validate's validation_errors, got: %v", errs)
	require.GreaterOrEqual(t, vfake.ExecutorCalls(), 1)
}

func TestValidationPipeline_ValidateEndpointMergesPipelineWarnings(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		warns: []runtime.ValidationFinding{{
			Class:   "attribute_deprecated_field",
			Message: "field bar is deprecated",
			Path:    "/executor/attributes/bar",
		}},
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-validate-warn-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates/validate", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["ok"], "a warnings-only pipeline outcome must still lint as ok")
	warns, ok := out["validation_warnings"].([]any)
	require.True(t, ok, "validation_warnings must be present")
	found := false
	for _, raw := range warns {
		w, ok := raw.(map[string]any)
		if ok && strings.Contains(fmt.Sprint(w["msg"]), "field bar is deprecated") {
			found = true
		}
	}
	require.True(t, found, "pipeline warning must merge into /validate's validation_warnings, got: %v", warns)

	status, out = vh.httpJSON(t, "POST", "/v1/templates/validate?warnings_as_errors=true", body)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, false, out["ok"],
		"warnings_as_errors must flip ok to false when the pipeline emits a warning")
}

func TestValidationPipeline_UnreachableValidatorPermissiveWarn_SucceedsWithWarning(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		rpcErr:         errors.New("connection refused"),
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-unreachable-permissive-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])

	warns, ok := out["validation_warnings"].([]any)
	require.True(t, ok, "response must carry validation_warnings")
	require.NotEmpty(t, warns)
	found := false
	for _, raw := range warns {
		w, ok := raw.(map[string]any)
		if ok && strings.Contains(fmt.Sprint(w["msg"]), "connection refused") {
			found = true
		}
	}
	require.True(t, found, "validator_unreachable finding must surface as a warning, got: %v", warns)
}

func TestValidationPipeline_UnreachableValidatorStrict_RejectsRegistration(t *testing.T) {
	t.Parallel()
	vfake := &fakeValidator{
		name:           "worker",
		supportedRoles: []string{"executor"},
		rpcErr:         errors.New("connection refused"),
	}
	vr := newFakeValidatorRegistry(vfake)
	vh, teardown := newValidatorHarness(t, vr, runtime.UnreachableValidatorStrict)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-unreachable-strict-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")

	errs, ok := out["validation_errors"].([]any)
	require.True(t, ok, "response must carry validation_errors")
	require.NotEmpty(t, errs)
	found := false
	for _, raw := range errs {
		e, ok := raw.(map[string]any)
		if ok && strings.Contains(fmt.Sprint(e["msg"]), "connection refused") {
			found = true
		}
	}
	require.True(t, found, "validator_unreachable finding must surface as an error under strict policy, got: %v", errs)
}
