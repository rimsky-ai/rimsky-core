// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestE2E_ExampleValidationAgainstRunningRimsky(t *testing.T) {
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	stubEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)
	valEndpoint := startExampleValidatorOnNetwork(ctx, t, netName, "validator")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("exec-stub", stubEndpoint),
		harness.WithClaimProducer("validator", valEndpoint, "read_only"),
		harness.WithClaimProducerProtocols("validator", "validation"),
		harness.WithRefValidationMode("none"),
	)

	t.Run("Error_severity_finding_blocks_registration", func(t *testing.T) {
		exerciseErrorBlocksRegistrationLeg(t, ep)
	})
	t.Run("Warning_severity_finding_passes_with_surface", func(t *testing.T) {
		exerciseWarningPassesWithSurfaceLeg(t, ep)
	})
	t.Run("Accept_case_passes_silently", func(t *testing.T) {
		exerciseAcceptCaseLeg(t, ep)
	})
}

func exerciseErrorBlocksRegistrationLeg(t *testing.T, ep harness.RimskyEndpoint) {
	spec := validatedTemplate("validation-example-error", SelectorTriggerError)
	status, raw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": spec})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /v1/templates with the validator's error-trigger selector: got status %d, want 400 (the example validator must refuse the registration via a ValidationFinding under `errors`); body: %s",
			status, string(raw))
	}

	bodyLower := strings.ToLower(string(raw))
	if !strings.Contains(bodyLower, "selector_rejected_by_example_validator") {
		t.Fatalf("rejection body must cite the validator's error-finding class (`selector_rejected_by_example_validator`); the absence proves either the Validator wasn't called or its finding was dropped at the rimsky↔response boundary; body: %s", string(raw))
	}

	var resp struct {
		Error            string           `json:"error"`
		ValidationErrors []map[string]any `json:"validation_errors"`
		ValidationWarns  []map[string]any `json:"validation_warnings"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode 400 body: %v: %s", err, string(raw))
	}
	if len(resp.ValidationErrors) == 0 {
		t.Fatalf("rejection body's `validation_errors` is empty — the validator's finding did not survive the round-trip to the operator response (falsifier guard: the operator must see WHY their registration was refused); body: %s", string(raw))
	}
}

func exerciseWarningPassesWithSurfaceLeg(t *testing.T, ep harness.RimskyEndpoint) {
	spec := validatedTemplate("validation-example-warning", SelectorTriggerWarning)

	status, raw := ep.PostJSON(t, "/v1/templates/validate", map[string]any{"spec": spec})
	if status != http.StatusOK {
		t.Fatalf("POST /v1/templates/validate with the warning-trigger selector: got status %d, want 200 (validate-only never 4xx's on a valid spec; warnings are echoed in the response body); body: %s",
			status, string(raw))
	}
	var validateResp struct {
		OK               bool             `json:"ok"`
		ValidationErrors []map[string]any `json:"validation_errors"`
		ValidationWarns  []map[string]any `json:"validation_warnings"`
	}
	if err := json.Unmarshal(raw, &validateResp); err != nil {
		t.Fatalf("decode validate response: %v: %s", err, string(raw))
	}
	if !validateResp.OK {
		t.Fatalf("validate-only response: ok=false on a warnings-only spec — a warning blocked the validation verdict, which violates the falsifier (warnings must NOT block); body: %s", string(raw))
	}
	if len(validateResp.ValidationErrors) != 0 {
		t.Fatalf("validate-only response: validation_errors non-empty on a warnings-only spec — the validator returned an error instead of a warning, OR rimsky misclassified the severity; body: %s", string(raw))
	}
	if len(validateResp.ValidationWarns) == 0 {
		t.Fatalf("validate-only response: `validation_warnings` is empty — the validator's warning did NOT survive the round-trip to the operator response (falsifier guard: warnings must be surfaced); body: %s", string(raw))
	}
	warningBlob := strings.ToLower(string(raw))
	if !strings.Contains(warningBlob, "/claim_producer/claims/0/selector") {
		t.Fatalf("validation_warnings body does not cite the per-binding selector path the example validator stamps on its findings; body: %s", string(raw))
	}
	if !strings.Contains(warningBlob, "warning-trigger sentinel") {
		t.Fatalf("validation_warnings body does not cite the example validator's warning message wording; body: %s", string(raw))
	}

	regStatus, regRaw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": spec})
	if regStatus != http.StatusCreated {
		t.Fatalf("POST /v1/templates with the warning-trigger selector: got status %d, want 201 (the falsifier fires when a warning-severity finding blocks registration); body: %s",
			regStatus, string(regRaw))
	}
}

func exerciseAcceptCaseLeg(t *testing.T, ep harness.RimskyEndpoint) {
	spec := validatedTemplate("validation-example-accept", "no-sentinel-selector")
	status, raw := ep.PostJSON(t, "/v1/templates/validate", map[string]any{"spec": spec})
	if status != http.StatusOK {
		t.Fatalf("POST /v1/templates/validate with a clean selector: got status %d, want 200; body: %s",
			status, string(raw))
	}
	var resp struct {
		OK               bool             `json:"ok"`
		ValidationErrors []map[string]any `json:"validation_errors"`
		ValidationWarns  []map[string]any `json:"validation_warnings"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode validate response: %v: %s", err, string(raw))
	}
	if !resp.OK {
		t.Fatalf("accept-case validate response: ok=false on a clean selector; body: %s", string(raw))
	}
	if len(resp.ValidationErrors) != 0 {
		t.Fatalf("accept-case validate response: validation_errors non-empty on a clean selector; body: %s", string(raw))
	}
	if len(resp.ValidationWarns) != 0 {
		t.Fatalf("accept-case validate response: validation_warnings non-empty on a clean selector — the validator emitted a spurious warning for a binding outside the sentinel grammar; body: %s", string(raw))
	}
}

func validatedTemplate(name, selector string) map[string]any {
	return map[string]any{
		"name":             name,
		"version":          "1",
		"frame_timeout_ms": 600000,
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": "exec-stub",
				"error_types": map[string]any{
					"acquire/unavailable": map[string]any{
						"policy": []map[string]any{{"action": "give_up"}},
					},
				},
				"stores": []map[string]any{
					{
						"name":     "validator",
						"selector": selector,
						"intent":   "r",
						"alias":    "claim",
					},
				},
			},
		},
	}
}

var exampleValidatorBuildMu sync.Mutex

func startExampleValidatorOnNetwork(ctx context.Context, t *testing.T, networkName, alias string) (endpoint string) {
	t.Helper()
	exampleValidatorBuildMu.Lock()
	defer exampleValidatorBuildMu.Unlock()

	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:    repoRoot(),
			Dockerfile: "examples/validation/Dockerfile.example",
			Repo:       "rimsky-example/validation",
			Tag:        "latest",
			KeepImage:  true,
		}),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithExposedPorts("9400/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9400/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start example validator: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9400"
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
