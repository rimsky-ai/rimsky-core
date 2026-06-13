// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-validation-author: a service author's
// example Validation mix-in — registered with rimsky's catalog,
// advertising the `validation` protocol on its primary protocol's
// Capabilities handshake, surfacing per-finding errors and warnings to
// the operator — plugs into a running rimsky stack end-to-end through
// the real registration surface. Each leg of the spec's Acceptance is
// exhibited against the REAL assembled product (rimsky-all-in-one in a
// testcontainer, Postgres state DB) plus the REAL example service (this
// directory's Validation + Producer combo, run in-process and exposed
// to the container via WithHostPortAccess):
//
//  1. Error-severity findings BLOCK registration. A template carrying a
//     claim binding whose selector triggers the example validator's
//     error sentinel (SelectorTriggerError) is POSTed to
//     `/v1/templates`. The control-api's registration pipeline calls
//     the example service's `Validate` RPC against the live registry,
//     the validator returns a `valid=false` response with a
//     ValidationFinding, and the control-api returns HTTP 400 carrying
//     the finding under `validation_errors`. The row is NEVER inserted
//     — proof "an error-severity finding from a registered Validator
//     blocks the POST /v1/templates registration".
//
//  2. Warning-severity findings PASS registration AND are surfaced. A
//     second template carries a selector triggering the warning
//     sentinel (SelectorTriggerWarning). POSTed to
//     `/v1/templates/validate` (the validate-only mode that runs the
//     same pipeline but never persists), the response is HTTP 200 with
//     `ok=true`, an empty `validation_errors` array, and a
//     non-empty `validation_warnings` array carrying the finding —
//     proof "warning-severity findings are surfaced without blocking".
//     POSTed to the full `/v1/templates` register surface, the same
//     template is accepted with HTTP 201 — proof the warning truly
//     does not block.
//
//  3. Bindings outside the sentinel grammar are accepted cleanly. A
//     control template whose selector carries neither sentinel is
//     POSTed to `/v1/templates/validate`; the response is HTTP 200
//     with `ok=true`, EMPTY `validation_errors`, AND EMPTY
//     `validation_warnings` — proof the validator participated (the
//     RPC actually ran) without emitting a spurious finding. This is
//     the load-bearing "validator was called and decided yes"
//     observation: a Validator that is registered but never called
//     would yield the same empty `validation_warnings` shape as one
//     that decided yes, which is why legs 1 + 2 (where the validator
//     MUST have been called for the response to carry the trigger-
//     keyed findings) are the falsifier guards on the "validator is
//     never called" arm.
//
// The three legs together exhibit the falsifier's three failure modes
// (error doesn't block / warning blocks / Validate never called) as
// observable failures rather than silent drops.
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import the lib/services testcontainers harness without
// putting the example's published Apache surface at risk — consumers
// who `go build` the example never pull in any test dependency.
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

// TestE2E_ExampleValidationAgainstRunningRimsky boots the rimsky-all-in-one
// image with the example Validation service registered as a peer (host
// claim-producer + validation mix-in), then exhibits the three
// registration-time properties STORY-validation-author's Acceptance
// names: error blocks, warning passes-with-surface, accept passes
// silently.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`) before this test runs. The harness pulls
// `rimsky-all-in-one:latest` from the local Docker daemon — nothing is
// fetched from a registry. A missing image is a hard t.Fatal (the
// harness never t.Skip's), so a developer who hasn't run `make
// core-images` sees the missing-image error directly.
func TestE2E_ExampleValidationAgainstRunningRimsky(t *testing.T) {
	// Not parallel: this scenario stands up a docker network + a
	// rimsky-all-in-one container + a Postgres testcontainer + an
	// executor stub + an in-process validation peer on a host port.
	// The cost is real, so other test methods in this package
	// (`validation_test.go`) keep their fast in-process shape and
	// only this gate pays the cross-stack price.
	ctx := context.Background()

	// 1. Shared docker network so both peers (the example validator
	//    and the stub executor) can come up on stable in-network
	//    aliases BEFORE rimsky boots. The peers MUST be up before
	//    rimsky comes up because the control-api eager-dials every
	//    declared claim-producer at startup and EXITS NON-ZERO on any
	//    unreachable peer; the host-port-tunnel path races that
	//    eager dial (see Dockerfile.example for the rationale), so
	//    both peers are containers on the same network as rimsky.
	netName := harness.NewNetwork(ctx, t)

	// 2. Stub executor on the network at alias `exec-stub`.
	//    Templates the proof posts reference this executor by name;
	//    rimsky's startup eager-dial runs the Capabilities handshake
	//    against it (a missing stub would exit the all-in-one
	//    container non-zero before /health). The stub returns Success
	//    on every dispatch, but the proof never actually creates
	//    instances — it only POSTs templates, so the stub's only job
	//    is to satisfy the registry's "executor declared" gate.
	stubEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-stub")

	// 3. Example validation peer on the network at alias `validator`,
	//    built on demand from Dockerfile.example via testcontainers
	//    FromDockerfile with the repo root as the build context. The
	//    peer's Capabilities handshake advertises the `validation`
	//    mix-in alongside its primary claim-producer protocol — see
	//    producer.go's Capabilities response.
	valEndpoint := startExampleValidatorOnNetwork(ctx, t, netName, "validator")

	// 4. Bring up rimsky-all-in-one. The peer is registered as a
	//    claim-producer (its primary protocol) with the `validation`
	//    mix-in advertised — see WithClaimProducerProtocols below.
	//    Strict-`all` (the all-in-one image's baked default) would
	//    require the executor's expected_attributes_schema to be in
	//    the discovery cache before POST /v1/templates accepts the
	//    template; the stub advertises a permissive open schema so
	//    `all` would work too, but `none` keeps the gate on the
	//    validation wiring under test, not the schema-cache freshness
	//    race.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("exec-stub", stubEndpoint),
		harness.WithClaimProducer("validator", valEndpoint, "read_only"),
		// The validation mix-in arrives via the producer's
		// Capabilities — see producer.go's Capabilities response.
		// The harness writes `protocols: [claim_producer, validation]`
		// into the rendered rimsky.yml; rimsky's
		// DialPublisherAndValidationRegistries dials the matching
		// Validation client per advertised protocol.
		harness.WithClaimProducerProtocols("validator", "validation"),
		harness.WithRefValidationMode("none"),
	)

	// Run each acceptance leg as a sub-test against the SAME running
	// stack — the three legs are independent registration observations,
	// so a single bring-up is sufficient and a per-leg bring-up would
	// only multiply the bring-up cost.
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

// exerciseErrorBlocksRegistrationLeg posts a template whose claim
// binding selector carries the validator's error sentinel; asserts the
// control-api refused the registration with HTTP 400 AND that the
// rejection body cites the validator's finding class.
//
// Proof for spec acceptance leg (a): "findings returned as errors
// cause the registration to be refused with the finding surfaced to
// the operator." Falsifier: "Error-severity finding doesn't block
// registration."
func exerciseErrorBlocksRegistrationLeg(t *testing.T, ep harness.RimskyEndpoint) {
	spec := validatedTemplate("validation-example-error", SelectorTriggerError)
	status, raw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": spec})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /v1/templates with the validator's error-trigger selector: got status %d, want 400 (the example validator must refuse the registration via a ValidationFinding under `errors`); body: %s",
			status, string(raw))
	}

	// The rejection body must cite the validator's finding class so an
	// operator can route the failure back to the validating service.
	bodyLower := strings.ToLower(string(raw))
	if !strings.Contains(bodyLower, "selector_rejected_by_example_validator") {
		t.Fatalf("rejection body must cite the validator's error-finding class (`selector_rejected_by_example_validator`); the absence proves either the Validator wasn't called or its finding was dropped at the rimsky↔response boundary; body: %s", string(raw))
	}

	// And the body's `validation_errors` array must be non-empty —
	// the finding must round-trip from the validator through rimsky's
	// pipeline to the operator-facing response.
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

// exerciseWarningPassesWithSurfaceLeg posts a template whose claim
// binding selector carries the validator's warning sentinel. Two
// observations:
//
//  1. `POST /v1/templates/validate` (validate-only, never persists)
//     returns 200 + ok=true + a non-empty `validation_warnings`
//     array carrying the validator's finding. The validate-only
//     surface is the one the spec's "warnings are surfaced" predicate
//     observes directly — the full registration response carries a
//     `template_id` + tags, not the warnings array.
//
//  2. `POST /v1/templates` accepts the same template with HTTP 201.
//     A warning that BLOCKED would surface here as a 400 — the 201
//     confirms warnings truly don't block.
//
// Proof for spec acceptance leg (b): "findings returned as warnings
// are surfaced without blocking." Falsifier: "warning-severity
// finding blocks registration."
func exerciseWarningPassesWithSurfaceLeg(t *testing.T, ep harness.RimskyEndpoint) {
	spec := validatedTemplate("validation-example-warning", SelectorTriggerWarning)

	// Leg (b.i): warnings ARE surfaced. The validate-only surface
	// runs the same pipeline and echoes warnings in the response.
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
	// Confirm the warning carries the per-binding JSON-pointer path the
	// example validator stamps on every finding. The control-api's
	// `findingToProjection` flattens the proto ValidationFinding into a
	// `{path, msg}` projection (class is dropped from the projection
	// today; only path + msg survive to the operator response), so
	// asserting on the path's slug confirms the finding the example
	// validator emitted is the one rimsky surfaced — not a finding
	// fabricated elsewhere in the pipeline. Use a case-insensitive
	// match on the serialized projection so a body capitalisation
	// change does not flap the assertion.
	warningBlob := strings.ToLower(string(raw))
	if !strings.Contains(warningBlob, "/claim_producer/claims/0/selector") {
		t.Fatalf("validation_warnings body does not cite the per-binding selector path the example validator stamps on its findings; body: %s", string(raw))
	}
	if !strings.Contains(warningBlob, "warning-trigger sentinel") {
		t.Fatalf("validation_warnings body does not cite the example validator's warning message wording; body: %s", string(raw))
	}

	// Leg (b.ii): warnings do NOT block. The full register surface
	// accepts the same spec.
	regStatus, regRaw := ep.PostJSON(t, "/v1/templates", map[string]any{"spec": spec})
	if regStatus != http.StatusCreated {
		t.Fatalf("POST /v1/templates with the warning-trigger selector: got status %d, want 201 (the falsifier fires when a warning-severity finding blocks registration); body: %s",
			regStatus, string(regRaw))
	}
}

// exerciseAcceptCaseLeg posts a template whose claim binding selector
// carries neither sentinel; asserts the validate-only response is
// `ok=true` with empty errors and empty warnings. This rules out a
// stray finding the validator might emit for every binding (which
// would be a different, but real, falsifier of the spec acceptance).
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

// --- helpers ---------------------------------------------------------------

// validatedTemplate builds a single-node template spec whose worker
// references the stub executor (so the registry's executor-declared
// gate passes) AND carries a `stores:` binding to the example
// validator peer with the given selector. The selector is what the
// example validator routes on — the proof passes the trigger sentinel
// per leg.
func validatedTemplate(name, selector string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               "1",
		"frame_resolution_mode": "serial_queue",
		"frame_timeout_ms":      600000,
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": "exec-stub",
				// Declaring the acquire/unavailable policy explicitly (give_up
				// is the default fail-fast behavior anyway) keeps the
				// acquisition-policy advisory out of the response, so the
				// accept-case leg's zero-warnings assertion isolates the
				// example validator's findings.
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

// exampleValidatorBuildMu serializes the testcontainers FromDockerfile
// build of the example validator image so parallel runs of the e2e
// test don't race on the same image tag (mirrors the executor-stub
// harness's stubBuildMu pattern and the claimproducer example's
// exampleProducerBuildMu). The mutex makes the first build
// single-flight; every later call rebuilds from the docker layer cache
// quickly under the same lock.
var exampleValidatorBuildMu sync.Mutex

// startExampleValidatorOnNetwork builds (on first use) and starts the
// example validator in a container on the given docker network with
// the given alias, returning the in-network endpoint
// (`<alias>:9400`) that rimsky's claim-producer registry dials at
// startup.
//
// The image is built on demand from this directory's
// Dockerfile.example via testcontainers FromDockerfile with the repo
// root as the build context — same pattern as the example
// claim-producer cross-stack proof. KeepImage=true so a repeated run
// reuses the cached layer.
//
// Why a container instead of an in-process server? rimsky's eager
// startup Capabilities dial against every declared claim-producer is
// hard-fail: any unreachable peer EXITS the all-in-one container
// non-zero before /health flips. An in-process peer exposed via
// WithHostPortAccess races the reverse-SSH host-port tunnel against
// the eager dial; under load the tunnel loses and the container
// exits. A container on a stable in-network alias is up BEFORE rimsky
// boots, so the handshake reaches it deterministically. See
// Dockerfile.example's preamble for the long-form rationale.
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

// repoRoot returns the rimsky-core repo root (the directory
// containing go.work), derived from this file's own location
// (examples/validation/main_e2e_test.go) so it is independent of the
// test's working directory. The Docker build context for the example
// validator is the repo root because the build copies in
// lib/protocols + the examples module via go.work — see
// Dockerfile.example.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
