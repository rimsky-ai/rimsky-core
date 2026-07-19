// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func writeTemplateSpecFile(t *testing.T, spec map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "template.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func parseInstanceIDFromRunOutput(t *testing.T, out string) shared.UUID {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if id, ok := strings.CutPrefix(line, "instance_id="); ok {
			id = strings.TrimSpace(id)
			u, err := uuid.Parse(id)
			require.NoErrorf(t, err, "parse instance_id %q from run output: %v", id, err)
			return shared.UUID(u)
		}
	}
	t.Fatalf("no instance_id= line found in `rimsky run` output:\n%s", out)
	return shared.UUID{}
}

func readAgentPIDFileFromHome(t *testing.T, home string) (int, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".rimsky", "agent.pid"))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// @story: host-agent-control-plane
func TestCLIRunService_AutoStartsAgentAndSpawnsHoldsReapsBoundBinary(t *testing.T) {
	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: {"executor", "lifecycle_subscriber"}},
	})

	adminKey, _ := h.MintAdminKey("cli-autostart-admin")
	startProxyOnPort(t, proxyPort, h.ControlBase, adminKey)

	stubBinary := buildBinary(t, "lib/runtime/hostagent/testdata/stubchild")
	rimskyBinary := buildBinary(t, "cmd/rimsky")
	specPath := writeTemplateSpecFile(t, lateBindTemplateSpec("cli-autostart-late-bind"))

	homeDir := t.TempDir()

	cmd := exec.Command(rimskyBinary, "run", specPath,
		"--service", lateBindServiceName+"="+stubBinary,
		"--endpoint", h.ControlBase,
		"--instance-key", "ck-cli-autostart",
	)
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"RIMSKY_API_KEY="+adminKey,
		"RIMSKY_URL="+proxyAddr,
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "`rimsky run --service` failed: %v\noutput:\n%s", err, out)

	pid, ok := readAgentPIDFileFromHome(t, homeDir)
	require.True(t, ok, "`rimsky run --service` must auto-start the host-agent daemon; "+
		"expected %s/.rimsky/agent.pid to exist after a run with --service bindings", homeDir)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGTERM) })

	instanceID := parseInstanceIDFromRunOutput(t, string(out))

	worker := h.FindNode(instanceID, "worker")
	require.NotNil(t, worker, "worker node should exist for instance %s", instanceID)
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	waitForNodeEventKindOnHarness(t, h, instanceID, "terminal/success")
}

func waitForNodeEventKindOnHarness(t *testing.T, h *scenario.Harness, instanceID shared.UUID, kinds ...string) string {
	t.Helper()
	fx := &hostAgentFixture{h: h}
	return fx.waitForNodeEventKind(t, instanceID, kinds...)
}
