// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-validation-warnings-surfaced executable proof.
//
// As a template author, advisory warnings the static validator already
// computes must reach me in the registration and validation responses,
// and ?warnings_as_errors=true must promote them to rejections
// (TD-merge-validator-warnings).
//
// The proof drives the REAL register surface (POST /v1/templates on
// the real control-API, real Postgres, real remote claim-producer
// peer) with a template that acquires claims but declares no
// acquisition-failure policy — tripping the static validator's
// acquire/unavailable acquisition-policy advisory
// (validateAcquireUnavailablePolicyAdvised):
//
//  1. POST /v1/templates registers (201) and the advisory appears in
//     the response's validation_warnings — negating the falsifier's
//     "computed but absent from both responses" clause for the
//     register surface.
//  2. POST /v1/templates/validate carries the same advisory in
//     validation_warnings with ok:true (warnings alone are not a
//     rejection) — the falsifier's other response surface.
//  3. POST /v1/templates?warnings_as_errors=true with the same
//     advisory-tripping shape is REJECTED (400) and persists nothing —
//     negating the falsifier's "warnings_as_errors=true not tripping
//     on it" clause.
//
// @story: validation-warnings-surfaced
package scenarios

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestValidationWarnings_StaticAdvisorySurfacedAndPromotable(t *testing.T) {
	t.Parallel()

	// @deliberate: Real remote claim-producer peer so the template's `stores:` block
	// passes the StoreDeclared registry check and the advisory is the
	// ONLY finding in play.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// @deliberate: (1) Register a template whose node acquires a claim from
	// queue-store but declares NO `error_types: {"acquire/unavailable":
	// ...}` policy → the static validator emits the acquisition-policy
	// advisory; registration still succeeds (warnings are advisory) and
	// the warning reaches the response.
	registerResp := postJSON(t, h.ControlBase+"/v1/templates",
		map[string]any{"spec": advisoryTrippingSpec("project-alpha-warnings", "1")})
	require.Equal(t, http.StatusCreated, registerResp.status,
		"a template tripping only the advisory must still register: %s",
		registerResp.bodyStr())
	require.True(t, strings.HasPrefix(registerResp.stringField("template_id"), "sha256-"),
		"register must return the content hash: %s", registerResp.bodyStr())
	require.True(t, responseCarriesAcquireAdvisory(registerResp.body),
		"the static validator's acquire/unavailable advisory must appear in the "+
			"register response's validation_warnings (falsifier: computed but absent): %s",
		registerResp.bodyStr())

	// @deliberate: (2) The validate endpoint carries the same advisory: ok stays
	// true (no flag), validation_warnings lists the advisory.
	validateResp := postJSON(t, h.ControlBase+"/v1/templates/validate",
		map[string]any{"spec": advisoryTrippingSpec("project-alpha-warnings-lint", "1")})
	require.Equal(t, http.StatusOK, validateResp.status, validateResp.bodyStr())
	okVal, isBool := validateResp.body["ok"].(bool)
	require.True(t, isBool && okVal,
		"warnings alone must not flip the validate verdict without the flag: %s",
		validateResp.bodyStr())
	require.True(t, responseCarriesAcquireAdvisory(validateResp.body),
		"the advisory must appear in the validate response's validation_warnings: %s",
		validateResp.bodyStr())

	// @deliberate: (3) warnings_as_errors=true promotes the same advisory to a
	// rejection: 400, the advisory named in the rejection set, nothing
	// persisted.
	preCount := listTemplateCount(t, h.ControlBase)
	rejectResp := postJSON(t, h.ControlBase+"/v1/templates?warnings_as_errors=true",
		map[string]any{"spec": advisoryTrippingSpec("project-alpha-warnings-strict", "1")})
	require.Equal(t, http.StatusBadRequest, rejectResp.status,
		"warnings_as_errors=true must reject a registration carrying the static "+
			"advisory (falsifier: flag not tripping on it): %s", rejectResp.bodyStr())
	require.Equal(t, true, rejectResp.body["warnings_as_errors"],
		"rejection must echo the flag: %s", rejectResp.bodyStr())
	require.True(t, responseCarriesAcquireAdvisory(rejectResp.body),
		"the rejection set must name the advisory that tripped it: %s",
		rejectResp.bodyStr())
	require.Equal(t, preCount, listTemplateCount(t, h.ControlBase),
		"a warnings_as_errors rejection must not persist a template row")

	strictValidateResp := postJSON(t, h.ControlBase+"/v1/templates/validate?warnings_as_errors=true",
		map[string]any{"spec": advisoryTrippingSpec("project-alpha-warnings-lint", "1")})
	require.Equal(t, http.StatusOK, strictValidateResp.status, strictValidateResp.bodyStr())
	strictOk, isBool := strictValidateResp.body["ok"].(bool)
	require.True(t, isBool && !strictOk,
		"warnings_as_errors=true must flip the validate verdict on the advisory: %s",
		strictValidateResp.bodyStr())
}

// advisoryTrippingSpec builds the inner `spec:` map for a one-node
// template that acquires a claim from queue-store and declares no
// acquisition-failure policy — exactly the shape the static validator's
// acquisition-policy advisory warns about. Canonical naming
// (`project-alpha-*`) per decision:project-agnostic.
func advisoryTrippingSpec(name, version string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": version,
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": "stub",
				"stores": []map[string]any{
					{"name": "queue-store", "selector": "@queue", "intent": "rw"},
				},
			},
		},
	}
}

// responseCarriesAcquireAdvisory scans a decoded response body's
// validation_warnings array for the acquire/unavailable
// acquisition-policy advisory. Register-surface entries carry the
// message under "message" (ValidationFinding JSON); the validate
// endpoint's {path, msg} projection carries it under "msg" — accept
// both.
func responseCarriesAcquireAdvisory(body map[string]any) bool {
	warns, ok := body["validation_warnings"].([]any)
	if !ok {
		return false
	}
	for _, w := range warns {
		entry, ok := w.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := entry["message"].(string)
		if msg == "" {
			msg, _ = entry["msg"].(string)
		}
		if strings.Contains(msg, "acquire/unavailable") {
			return true
		}
	}
	return false
}
