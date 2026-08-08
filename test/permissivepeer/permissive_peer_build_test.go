// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: permissive-peer-build
// @decision: licensing-dual-apache-agpl
package permissivepeer

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const peerPkg = "./test/permissivepeer/peer"

const rimskyModulePrefix = "github.com/rimsky-ai/rimsky-core/"

const permissiveModulePrefix = "github.com/rimsky-ai/rimsky-core/lib/protocols/"

const peerImportPath = "github.com/rimsky-ai/rimsky-core/test/permissivepeer/peer"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must locate the test source file")
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func TestPermissivePeerDependsOnProtocolsModuleAlone(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-deps", peerPkg)
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoErrorf(t, err, "go list -deps %s: %s", peerPkg, stderr.String())

	var copyleft []string
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if !strings.HasPrefix(dep, rimskyModulePrefix) {
			continue
		}
		if strings.HasPrefix(dep, permissiveModulePrefix) || dep == peerImportPath {
			continue
		}
		copyleft = append(copyleft, dep)
	}
	require.Emptyf(t, copyleft,
		"the permissive peer must reach no rimsky package outside the protocols module; it reaches %v", copyleft)
}

func TestPermissivePeerRunsAgainstRimskyStack(t *testing.T) {
	t.Parallel()

	addr, stderr := startPermissivePeer(t)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			"permissive-peer": {Transport: "grpc", URL: addr},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:    "permissive-peer-build",
		Version: "1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "worker",
			Executor: "permissive-peer",
		}},
	})

	iid := h.CreateInstance(tid, "ck-permissive-peer-build", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node missing from instance")

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	require.NotContains(t, stderr.String(), "permissive-peer:",
		"the peer must not have reported an error while serving the dispatch")
}

func startPermissivePeer(t *testing.T) (string, *bytes.Buffer) {
	t.Helper()
	root := repoRoot(t)

	binPath := filepath.Join(t.TempDir(), "permissive-peer")
	build := exec.Command("go", "build", "-o", binPath, peerPkg)
	build.Dir = root
	buildOut, err := build.CombinedOutput()
	require.NoErrorf(t, err, "go build %s: %s", peerPkg, string(buildOut))

	cmd := exec.Command(binPath)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err, "peer stdout pipe")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start(), "start the permissive peer")
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "listening="); ok {
			return rest, &stderr
		}
	}
	t.Fatalf("the permissive peer exited without announcing its address; stderr:\n%s", stderr.String())
	return "", nil
}
