// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (f *fakeValidator) ValidateExecutor(_ context.Context, _ runtime.ValidateExecutorInput) ([]runtime.ValidationFinding, []runtime.ValidationFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executor++
	return f.errs, f.warns, f.rpcErr
}
func (f *fakeValidator) ValidateClaimProducer(_ context.Context, _ runtime.ValidateClaimProducerInput) ([]runtime.ValidationFinding, []runtime.ValidationFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.producer++
	return f.errs, f.warns, f.rpcErr
}
func (f *fakeValidator) ValidatePublisher(_ context.Context, _ runtime.ValidatePublisherInput) ([]runtime.ValidationFinding, []runtime.ValidationFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publisher++
	return f.errs, f.warns, f.rpcErr
}
func (f *fakeValidator) ValidateLifecycleSubscriber(_ context.Context, _ runtime.ValidateLifecycleSubscriberInput) ([]runtime.ValidationFinding, []runtime.ValidationFinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lifecycle++
	return f.errs, f.warns, f.rpcErr
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

type validatorHarness struct {
	*harness
	validator *fakeValidator
}

func newValidatorHarness(t *testing.T, vr *fakeValidatorRegistry, vfake *fakeValidator, policy runtime.UnreachableValidatorPolicy) (*validatorHarness, func()) {
	t.Helper()
	h, teardown := newAppHarness(t, func(deps *AppDeps) {
		deps.Validators = vr
		deps.UnreachableValidatorPolicy = policy
	})
	return &validatorHarness{harness: h, validator: vfake}, teardown
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-err-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.executor, 1)
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-warn-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
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
	require.GreaterOrEqual(t, vfake.publisher, 1,
		"registering a template with a publisher whose validator advertises the publisher role must invoke ValidatePublisher")
	require.Equal(t, 0, vfake.executor)
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-lifecycle-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, out["error"], "validation pipeline")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs)
	require.GreaterOrEqual(t, vfake.lifecycle, 1,
		"registering any template must invoke ValidateLifecycleSubscriber template-wide on every validator advertising the lifecycle_subscriber role")
	require.Equal(t, 0, vfake.executor)
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-waserrs-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates?warnings_as_errors=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Equal(t, true, out["warnings_as_errors"])
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
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
	require.GreaterOrEqual(t, vfake.executor, 1)
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorPermissiveWarn)
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
		if ok && strings.Contains(fmt.Sprint(w["message"]), "connection refused") {
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
	vh, teardown := newValidatorHarness(t, vr, vfake, runtime.UnreachableValidatorStrict)
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
		if ok && strings.Contains(fmt.Sprint(e["message"]), "connection refused") {
			found = true
		}
	}
	require.True(t, found, "validator_unreachable finding must surface as an error under strict policy, got: %v", errs)
}
