// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: one-shot-to-terminal
// @decision: termination
// @concept: instance
func TestRemoteOneShotRunTerminatesItsInstanceOnQuiescenceAndReturns(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "worker done")

	t.Setenv(hostagent.IdentityFileEnvVar, filepath.Join(t.TempDir(), "identity.json"))

	spec := node.TemplateSpec{
		Name: "remote-one-shot-to-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	}
	specBytes, err := json.Marshal(spec)
	require.NoError(t, err)
	specPath := filepath.Join(t.TempDir(), "template.json")
	require.NoError(t, os.WriteFile(specPath, specBytes, 0o600))

	code := cli.RunRunRemote(context.Background(), &cli.CommonFlags{}, h.ControlBase, cli.RunFlags{
		TemplateFile: specPath,
		Keep:         false,
		PollInterval: 50 * time.Millisecond,
	})

	require.Equal(t, cli.ExitAllSuccess, code,
		"the remote one-shot run polls its instance's quiescence, terminates it, and exits; "+
			"it never waits on a terminal stamp the platform does not set")
}
