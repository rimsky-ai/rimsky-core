// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package stub

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	executorconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
)

func findScenarioResult(t *testing.T, results []executorconf.Result, name string) executorconf.Result {
	t.Helper()
	for _, r := range results {
		if r.Scenario == name {
			return r
		}
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Scenario)
	}
	t.Fatalf("result set missing scenario %q (have: %v)", name, names)
	return executorconf.Result{}
}

func TestConformanceCancelScenario_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: addr},
		Only:     []string{"cancel"},
		Timeout:  30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "cancel")
	require.False(t, r.Skipped, "cancel scenario unexpectedly skipped: %s", r.Error)
	require.True(t, r.Passed, "cancel scenario expected to PASS against the cancel-aware stub, got Error: %s", r.Error)
}

func TestConformanceCancelScenario_CancelIgnoringStubFails(t *testing.T) {
	s := New().EnableStubMode().IgnoreCancelProbe()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: addr},
		Only:     []string{"cancel"},
		Timeout:  5 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "cancel")
	require.False(t, r.Skipped, "cancel scenario unexpectedly skipped: %s", r.Error)
	require.False(t, r.Passed, "cancel scenario expected to FAIL against a stub that ignores cancellation")
}
