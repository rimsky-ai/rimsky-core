// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestValidationWarnings_StaticAdvisorySurfacedAndPromotable(t *testing.T) {
	t.Parallel()

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
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

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

func advisoryTrippingSpec(name, version string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": version,
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": "stub",
				"claim_producers": []map[string]any{
					{"name": "queue-store", "selector": "@queue", "intent": "rw"},
				},
			},
		},
	}
}

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
