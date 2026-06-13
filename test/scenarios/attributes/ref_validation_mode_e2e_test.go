// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end acceptance gate for the operator-set registration-time
// reference-validation MODE (story S-template-validation-ref-validation-
// mode).
//
// The mode is a single startup-level operator choice governing ALL
// registration-time reference validation. This test boots the REAL
// assembled product — the in-process control-api reachable over HTTP,
// backed by a testcontainers Postgres, fronting a real observability
// handshake against real stub executor gRPC services — three times, once
// per mode (all / available / none), and drives `POST /templates`
// through the real registration validator. The observable surface is the
// differing registration HTTP responses against the real control-api,
// exactly the user-visible outcome the story promises:
//
//   - mode all (default): a template referencing a not-yet-provisioned
//     executor is REJECTED with HTTP 400 carrying a missing-reference
//     validation_errors entry.
//   - mode available: that same registration SUCCEEDS (201) for the not-
//     yet-provisioned reference WHILE a genuinely-invalid reference to a
//     PROVISIONED executor (a node default below the executor schema's
//     `minimum`) is still REJECTED with 400.
//   - mode none: registration SUCCEEDS (201) with no registration-time
//     reference validation at all — even the provisioned-invalid ref
//     registers clean.
//
// It also carries the executable acceptance proof for
// STORY-validation-names-the-mode: the mode-all rejection body must name
// the failing reference, the active mode, and the
// templates.ref_validation_mode config key with its relaxed settings —
// and registering the SAME template under the advised relaxed mode must
// succeed, proving the advice the error gives is true.
//
// The "genuinely-invalid PROVISIONED ref" leg needs an executor whose
// advertised schema actually constrains the attribute — the default
// permissive `{"type":"object"}` stub can never make a ref "invalid".
// We stand up a constraint-advertising executor via the TEMPLCASCADE-3.0
// knob `scenario.StartStubExecutorWithSchema` as an ExtraExecutors entry
// ("constrained") whose Capabilities advertise a `minimum:0` property,
// and reference THAT executor with a node default of `count: -1`. The
// control-api's real observability handshake probes that executor at
// startup and the registration validator reads the constrained schema
// from the live discovery cache — no fake stands in for the schema
// source.
//
// The mode is plumbed through `scenario.HarnessOpts.RefValidationMode`
// into the in-process control-api's
// `config.ControlAPIConfig.RefValidationMode` →
// `controlapi.AppDeps.RefValidationMode` → the registration validator
// hooks — the same operator config path production uses (the field is
// also sourced from cfg:templates.ref_validation_mode /
// env:RIMSKY_REF_VALIDATION_MODE in the production loader).
package attributes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// constrainedSchema is the JSON Schema the "constrained" executor
// advertises via its observability Capabilities. The `count` property's
// `minimum:0` makes a node default of `count: -1` a genuinely-invalid
// PROVISIONED reference — without a constraint the permissive default
// stub could never make any ref "invalid."
const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

// startRefModeHarness boots a full real stack at the given reference-
// validation mode with a constraint-advertising "constrained" executor
// wired as an ExtraExecutors entry. The "ghost-executor" name used by
// the not-provisioned templates is deliberately NOT registered, so it is
// a genuinely not-yet-provisioned reference at registration time.
func startRefModeHarness(t *testing.T, mode node.RefValidationMode) *scenario.Harness {
	t.Helper()
	return scenario.Start(t, scenario.HarnessOpts{
		// No supervisor/scheduler needed — this gate observes only the
		// registration HTTP response from the control-api, no run is
		// driven. Skipping them keeps the gate fast and isolates the
		// surface under test (the registration validator) from any
		// dispatch behavior.
		NoSupervisor: true,
		NoScheduler:  true,
		// "constrained" is a real, provisioned stub executor whose
		// observability Capabilities advertise the constraining schema.
		// The control-api's real observability handshake probes it at
		// startup so the registration validator reads its `minimum:0`
		// schema from the live discovery cache — making `count: -1` a
		// genuinely-invalid PROVISIONED reference. "ghost-executor"
		// (referenced by the not-provisioned templates) is deliberately
		// absent, so it is genuinely not-yet-provisioned.
		ExtraExecutors: map[string]executor.Endpoint{
			"constrained": scenario.StartStubExecutorWithSchema(t, []byte(constrainedSchema)),
		},
		RefValidationMode: mode,
	})
}

// postTemplate issues a raw POST /templates against the real control-api
// and returns the HTTP status plus the decoded JSON body. Unlike
// Harness.DeployTemplate (which fatals on a non-2xx), this lets the test
// assert the rejection statuses the mode produces.
func postTemplate(t *testing.T, h *scenario.Harness, specBody map[string]any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"spec": specBody})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/templates", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	// A 400 carries {error, validation_errors}; a 201 carries
	// {template_id}. Either decodes into the generic map.
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

// notProvisionedTemplate is a single-node template referencing the
// not-yet-provisioned "ghost-executor" (absent from the harness executor
// set / discovery cache).
func notProvisionedTemplate(name string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               "v1",
		"frame_resolution_mode": "serial_queue",
		"nodes": []map[string]any{
			{"type": "root", "executor": "ghost-executor"},
		},
	}
}

// provisionedInvalidTemplate is a single-node template whose node
// references the PROVISIONED "constrained" executor with a default
// (`count: -1`) that violates its advertised schema (`minimum: 0`) — a
// genuinely-invalid provisioned reference.
func provisionedInvalidTemplate(name string) map[string]any {
	return map[string]any{
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
	}
}

// TestAcceptance_RefValidationMode is the end-to-end acceptance gate for
// the operator-set registration-time reference-validation mode. Each
// sub-case boots an independent real stack at one mode and asserts the
// registration HTTP responses the operator observes.
func TestAcceptance_RefValidationMode(t *testing.T) {
	t.Run("all: not-provisioned ref rejected with 400 missing-reference", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateAll)

		status, out := postTemplate(t, h, notProvisionedTemplate("refmode-all-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, status,
			"mode all must reject a not-yet-provisioned executor reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the missing reference")
	})

	t.Run("available: not-provisioned ref succeeds; provisioned-invalid ref still 400s", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateAvailable)

		// The not-yet-provisioned ref registers clean under `available`.
		okStatus, okOut := postTemplate(t, h, notProvisionedTemplate("refmode-avail-ok-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, okStatus,
			"mode available must accept a not-yet-provisioned executor reference; body: %v", okOut)
		require.NotEmpty(t, okOut["template_id"],
			"a successful registration must return a template_id; body: %v", okOut)

		// A genuinely-invalid PROVISIONED ref (count: -1 vs the executor
		// schema's minimum: 0) is still rejected — `available` validates
		// provisioned refs, it does not turn validation off.
		badStatus, badOut := postTemplate(t, h, provisionedInvalidTemplate("refmode-avail-bad-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, badStatus,
			"mode available must still reject a genuinely-invalid provisioned ref; body: %v", badOut)
		errs, ok := badOut["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", badOut)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
	})

	// STORY-validation-names-the-mode: the rejection is self-documenting.
	// Under the strict default mode the operator registering a template
	// whose reference cannot be validated is told WHICH mode rejected it
	// and WHICH config key changes the behavior — the register-before-
	// provision workflow is discoverable from the error message itself.
	// The companion sub-assertion re-registers the SAME template under
	// the relaxed mode the message advises and succeeds, proving the
	// advice the error gives is true.
	t.Run("rejection names the active mode and the config key; the advised relaxed mode accepts", func(t *testing.T) {
		t.Parallel()
		spec := notProvisionedTemplate("refmode-msg-" + uuid.NewString())

		// Strict default mode: rejection must name the mode + config key.
		strict := startRefModeHarness(t, node.RefValidateAll)
		status, out := postTemplate(t, strict, spec)
		require.Equal(t, http.StatusBadRequest, status,
			"mode all must reject the unprovisioned reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs)
		// Collect every rejection message; the missing-reference entry
		// must be self-documenting.
		var msgs []string
		for _, e := range errs {
			entry, entryOK := e.(map[string]any)
			require.True(t, entryOK, "validation_errors entries must be objects; got %v", e)
			msg, msgOK := entry["msg"].(string)
			require.True(t, msgOK, "validation_errors entries must carry msg; got %v", entry)
			msgs = append(msgs, msg)
		}
		joined := strings.Join(msgs, "\n")
		require.Contains(t, joined, `"ghost-executor"`,
			"the rejection must name the failing reference; messages: %s", joined)
		require.Contains(t, joined, `mode "all"`,
			"the rejection must name the active reference-validation mode; messages: %s", joined)
		require.Contains(t, joined, "templates.ref_validation_mode",
			"the rejection must name the config key that changes the behavior; messages: %s", joined)
		require.Contains(t, joined, `"available"`,
			"the rejection must name the relaxed settings for register-first workflows; messages: %s", joined)
		require.Contains(t, joined, `"none"`,
			"the rejection must name the relaxed settings for register-first workflows; messages: %s", joined)

		// The advice is true: the SAME template registers clean under the
		// relaxed mode the message names.
		relaxed := startRefModeHarness(t, node.RefValidateAvailable)
		okStatus, okOut := postTemplate(t, relaxed, spec)
		require.Equal(t, http.StatusCreated, okStatus,
			"the relaxed mode the rejection advises must accept the same template; body: %v", okOut)
		require.NotEmpty(t, okOut["template_id"],
			"a successful registration must return a template_id; body: %v", okOut)
	})

	t.Run("none: no registration-time reference validation", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateNone)

		// The not-yet-provisioned ref registers clean.
		ghostStatus, ghostOut := postTemplate(t, h, notProvisionedTemplate("refmode-none-ghost-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, ghostStatus,
			"mode none must accept a not-yet-provisioned executor reference; body: %v", ghostOut)
		require.NotEmpty(t, ghostOut["template_id"],
			"a successful registration must return a template_id; body: %v", ghostOut)

		// Even the provisioned-invalid ref registers clean under `none`:
		// registration-time reference validation is off entirely.
		invalidStatus, invalidOut := postTemplate(t, h, provisionedInvalidTemplate("refmode-none-invalid-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, invalidStatus,
			"mode none must perform no registration-time reference validation; body: %v", invalidOut)
		require.NotEmpty(t, invalidOut["template_id"],
			"a successful registration must return a template_id; body: %v", invalidOut)
	})
}
