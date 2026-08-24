// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: permissive-service-build
// @decision: licensing-dual-apache-agpl
package permissiveservice

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

const servicePkg = "./test/permissiveservice/service"

const rimskyModulePrefix = "github.com/rimsky-ai/rimsky-core/"

const permissiveModulePrefix = "github.com/rimsky-ai/rimsky-core/lib/protocols/"

const serviceImportPath = "github.com/rimsky-ai/rimsky-core/test/permissiveservice/service"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must locate the test source file")
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func TestPermissiveServiceDependsOnProtocolsModuleAlone(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-deps", servicePkg)
	cmd.Dir = repoRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoErrorf(t, err, "go list -deps %s: %s", servicePkg, stderr.String())

	var copyleft []string
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if !strings.HasPrefix(dep, rimskyModulePrefix) {
			continue
		}
		if strings.HasPrefix(dep, permissiveModulePrefix) || dep == serviceImportPath {
			continue
		}
		copyleft = append(copyleft, dep)
	}
	require.Emptyf(t, copyleft,
		"the permissive service must reach no rimsky package outside the protocols module; it reaches %v", copyleft)
}

func TestPermissiveServiceRunsAgainstRimskyStack(t *testing.T) {
	t.Parallel()

	addr, stderr := startPermissiveService(t)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			"permissive-service": {Transport: "grpc", URL: addr},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name:    "permissive-service-build",
		Version: "1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "worker",
			Executor: "permissive-service",
		}},
	})

	iid := h.CreateInstance(tid, "ck-permissive-service-build", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node missing from instance")

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	require.NotContains(t, stderr.String(), "permissive-service:",
		"the service must not have reported an error while serving the dispatch")
}

func startPermissiveService(t *testing.T) (string, *bytes.Buffer) {
	t.Helper()
	root := repoRoot(t)

	binPath := filepath.Join(t.TempDir(), "permissive-service")
	build := exec.Command("go", "build", "-o", binPath, servicePkg)
	build.Dir = root
	buildOut, err := build.CombinedOutput()
	require.NoErrorf(t, err, "go build %s: %s", servicePkg, string(buildOut))

	cmd := exec.Command(binPath)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err, "service stdout pipe")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start(), "start the permissive service")
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
	t.Fatalf("the permissive service exited without announcing its address; stderr:\n%s", stderr.String())
	return "", nil
}
