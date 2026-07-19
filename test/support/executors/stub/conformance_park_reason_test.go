// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stub

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	executorconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestConformanceParkReasonEmissionScenarios_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint:        executorconf.Endpoint{Transport: "grpc", URL: addr},
		RequireStubMode: true,
		Only:            []string{"park_reason_emission", "park_reason_emission_snooze"},
		Timeout:         30 * time.Second,
	})
	require.NoError(t, err)
	for _, name := range []string{"park_reason_emission", "park_reason_emission_snooze"} {
		r := findScenarioResult(t, results, name)
		require.False(t, r.Skipped, "%s scenario unexpectedly skipped: %s", name, r.Error)
		require.True(t, r.Passed, "%s scenario expected to PASS against the honest stub, got Error: %s", name, r.Error)
	}
}

func TestConformanceParkReasonEmission_WrongReasonFails(t *testing.T) {
	s := New().EnableStubMode().ForceParkReason(genv1.ParkReason_PARK_REASON_SNOOZE)
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint:        executorconf.Endpoint{Transport: "grpc", URL: addr},
		RequireStubMode: true,
		Only:            []string{"park_reason_emission"},
		Timeout:         30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "park_reason_emission")
	require.False(t, r.Skipped, "park_reason_emission scenario unexpectedly skipped: %s", r.Error)
	require.False(t, r.Passed,
		"park_reason_emission expected to FAIL when the executor emits PARK_REASON_SNOOZE for an await_callback request")
}
