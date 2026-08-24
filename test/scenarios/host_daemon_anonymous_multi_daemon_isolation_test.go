// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: host-daemon-anonymous-mode
// @story: anonymous-daemons-isolated
func TestHostDaemonAnonymousModeMultiDaemonIsolation(t *testing.T) {
	stub := buildBinary(t, "lib/runtime/hostdaemon/testdata/stubchild")
	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	proxyServicePort := freePort(t)
	proxyServiceAddr := fmt.Sprintf("127.0.0.1:%d", proxyServicePort)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyServiceAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: {"executor", "lifecycle_subscriber"}},
	})

	proxyCAPath := startProxyOnPort(t, proxyPort, proxyServicePort, h.ControlBase, "")

	const (
		daemonAlpha = "sparkling-wombat"
		daemonBeta  = "quiet-otter"
	)

	t.Setenv("STUBCHILD_EXEC_ENV_KEY", "RIMSKY_DAEMON_ROUTING_LABEL")
	execLogAlpha := t.TempDir() + "/stub-exec-alpha.log"
	execLogBeta := t.TempDir() + "/stub-exec-beta.log"

	cancelAlpha, doneAlpha, alphaStatus := startDaemon(t, proxyAddr, proxyCAPath, daemonStartOptions{RoutingLabel: daemonAlpha})
	t.Cleanup(func() {
		cancelAlpha()
		<-doneAlpha
	})
	waitDaemonConnected(t, alphaStatus)

	cancelBeta, doneBeta, betaStatus := startDaemon(t, proxyAddr, proxyCAPath, daemonStartOptions{RoutingLabel: daemonBeta})
	t.Cleanup(func() {
		cancelBeta()
		<-doneBeta
	})
	waitDaemonConnected(t, betaStatus)

	assertAnonymousLabelCollisionRejected(t, proxyAddr, proxyCAPath, daemonAlpha, alphaStatus)

	tid := h.DeployTemplateSpecMap(lateBindTemplateSpec("multi-daemon-isolation"), "")

	iidAlpha := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-alpha", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{
			"path": stub,
			"env":  map[string]any{"STUBCHILD_EXEC_LOG": execLogAlpha},
		}},
		daemonAlpha,
	)
	iidBeta := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-beta", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{
			"path": stub,
			"env":  map[string]any{"STUBCHILD_EXEC_LOG": execLogBeta},
		}},
		daemonBeta,
	)
	require.NotEqual(t, iidAlpha, iidBeta, "the two instances must be distinct")

	waitInstanceLateBindTerminal(t, h, iidAlpha)
	waitInstanceLateBindTerminal(t, h, iidBeta)

	require.Equal(t, 1, countWorkerRuns(t, h, iidAlpha), "alpha instance worker must complete on exactly one dispatch (no cross-daemon routing)")
	require.Equal(t, 1, countWorkerRuns(t, h, iidBeta), "beta instance worker must complete on exactly one dispatch (no cross-daemon routing)")

	alphaLine := waitForExecLog(t, execLogAlpha)
	require.Equal(t, daemonAlpha, alphaLine.Env,
		"alpha instance's dispatch must have been spawned by the alpha daemon, not a cross-routed one")
	betaLine := waitForExecLog(t, execLogBeta)
	require.Equal(t, daemonBeta, betaLine.Env,
		"beta instance's dispatch must have been spawned by the beta daemon, not a cross-routed one")

	cancelAlpha()
	<-doneAlpha

	cancelAlpha2, doneAlpha2, alphaStatus2 := startDaemon(t, proxyAddr, proxyCAPath, daemonStartOptions{RoutingLabel: daemonAlpha})
	t.Cleanup(func() {
		cancelAlpha2()
		<-doneAlpha2
	})
	waitDaemonConnected(t, alphaStatus2)

	iidAlphaReconnect := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-alpha-reconnect", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{"path": stub}},
		daemonAlpha,
	)
	waitInstanceLateBindTerminal(t, h, iidAlphaReconnect)
}

// @story: host-daemon-anonymous-mode
func assertAnonymousLabelCollisionRejected(t *testing.T, proxyAddr, proxyCAPath, collidingLabel, aliveStatusFile string) {
	t.Helper()
	conn := dialDaemonFacing(t, proxyAddr, proxyCAPath)

	stream, err := genv1.NewHostDaemonClient(conn).Connect(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               sillyname.AnonymousCredentialSentinel,
		DaemonLabel:          "collision-attacker",
		RoutingLabel:         collidingLabel,
		LocalCallbackBaseUrl: "http://127.0.0.1:1",
	}}}))

	_, recvErr := stream.Recv()
	require.Error(t, recvErr, "the proxy must reject a colliding routing_label rather than displace the live daemon")
	require.Equal(t, codes.AlreadyExists, status.Code(recvErr),
		"colliding anonymous routing_label must be rejected AlreadyExists so the connected daemon keeps its routing identity")

	body, err := os.ReadFile(aliveStatusFile)
	require.NoError(t, err, "the pre-existing daemon's status file must still be readable after a rejected collision")
	var snap hostdaemon.StatusSnapshot
	require.NoError(t, json.Unmarshal(body, &snap))
	require.True(t, snap.Connected,
		"the pre-existing daemon must remain connected — rejection is what proves it kept its routing identity")
}

// @concept: node
func countWorkerRuns(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	worker := h.FindNode(instanceID, "worker")
	require.NotNil(t, worker, "worker node must exist for instance %s", instanceID.String())
	var count int
	err := h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`, worker.ID).Scan(&count)
	require.NoError(t, err)
	return count
}

func waitInstanceLateBindTerminal(t *testing.T, h *scenario.Harness, iid shared.UUID) {
	t.Helper()
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist for instance %s", iid.String())
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
}
