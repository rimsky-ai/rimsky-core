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

func TestConformanceScratchParkRoundTrip_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: addr},
		Only:     []string{"scratch_park_round_trip"},
		Timeout:  30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "scratch_park_round_trip")
	require.False(t, r.Skipped, "scratch_park_round_trip scenario unexpectedly skipped: %s", r.Error)
	require.True(t, r.Passed, "scratch_park_round_trip scenario expected to PASS against the honest stub, got Error: %s", r.Error)
}
