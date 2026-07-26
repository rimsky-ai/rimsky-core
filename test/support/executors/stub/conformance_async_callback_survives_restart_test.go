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

func TestConformanceAsyncCallbackSurvivesRestart_HonestStubPasses(t *testing.T) {
	s := New().EnableStubMode()
	addr := listenForTest(t, s)

	results, err := executorconf.Run(context.Background(), executorconf.RunnerOpts{
		Endpoint: executorconf.Endpoint{Transport: "grpc", URL: addr},
		Only:     []string{"async_callback_survives_restart"},
		Timeout:  30 * time.Second,
	})
	require.NoError(t, err)
	r := findScenarioResult(t, results, "async_callback_survives_restart")
	require.False(t, r.Skipped, "async_callback_survives_restart scenario unexpectedly skipped: %s", r.Error)
	require.True(t, r.Passed, "async_callback_survives_restart scenario expected to PASS against the retrying stub, got Error: %s", r.Error)
}
