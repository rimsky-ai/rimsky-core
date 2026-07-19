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

func TestConformanceParkEmission_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint:        executorconf.Endpoint{Transport: "grpc", URL: addr},
		RequireStubMode: true,
		Only:            []string{"park_emission"},
		Timeout:         30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "park_emission")
	require.False(t, r.Skipped, "park_emission scenario unexpectedly skipped: %s", r.Error)
	require.True(t, r.Passed, "park_emission scenario expected to PASS against the honest stub, got Error: %s", r.Error)
}
