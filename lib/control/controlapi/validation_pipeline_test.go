// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
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

type validatorHarness struct {
	*harness
	validator *fakeValidator
}

func newValidatorHarness(t *testing.T, vr *fakeValidatorRegistry, vfake *fakeValidator) (*validatorHarness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)

	capLog := shared.NewCapturingLogger()
	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Clock:          shared.SystemClock{},
		Logger:         capLog,
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
		Executors: map[string]ExecutorEntry{
			"worker": {Transport: "grpc", Endpoint: "localhost:0"},
		},
		Validators:                 vr,
		UnreachableValidatorPolicy: runtime.UnreachableValidatorPermissiveWarn,
	})
	srv := httptest.NewServer(app)
	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	vh := &validatorHarness{harness: h, validator: vfake}
	return vh, func() {
		srv.Close()
	}
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
	vh, teardown := newValidatorHarness(t, vr, vfake)
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
	vh, teardown := newValidatorHarness(t, vr, vfake)
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
	vh, teardown := newValidatorHarness(t, vr, vfake)
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
	vh, teardown := newValidatorHarness(t, vr, vfake)
	t.Cleanup(teardown)

	body := validTemplateBody("vp-waserrs-" + uuid.NewString())
	status, out := vh.httpJSON(t, "POST", "/v1/templates?warnings_as_errors=true", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Equal(t, true, out["warnings_as_errors"])
}
