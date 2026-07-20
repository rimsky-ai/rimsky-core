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
)

func TestConformanceScratchParkRoundTrip_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint:        executorconf.Endpoint{Transport: "grpc", URL: addr},
		RequireStubMode: true,
		Only:            []string{"scratch_park_round_trip"},
		Timeout:         30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "scratch_park_round_trip")
	require.False(t, r.Skipped, "scratch_park_round_trip scenario unexpectedly skipped: %s", r.Error)
	require.True(t, r.Passed, "scratch_park_round_trip scenario expected to PASS against the honest stub, got Error: %s", r.Error)
}
