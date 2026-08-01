// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: host-agent-anonymous-mode
func TestHostAgentAnonymousModeMultiAgentIsolation(t *testing.T) {
	stub := buildBinary(t, "lib/runtime/hostagent/testdata/stubchild")
	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: {"executor", "lifecycle_subscriber"}},
	})

	startProxyOnPort(t, proxyPort, h.ControlBase, "")

	const (
		agentAlpha = "sparkling-wombat"
		agentBeta  = "quiet-otter"
	)

	t.Setenv("STUBCHILD_EXEC_ENV_KEY", "RIMSKY_AGENT_ROUTING_LABEL")
	execLogAlpha := t.TempDir() + "/stub-exec-alpha.log"
	execLogBeta := t.TempDir() + "/stub-exec-beta.log"

	cancelAlpha, doneAlpha, alphaStatus := startAgent(t, proxyAddr, agentStartOptions{RoutingLabel: agentAlpha})
	t.Cleanup(func() {
		cancelAlpha()
		<-doneAlpha
	})
	waitAgentConnected(t, alphaStatus)

	cancelBeta, doneBeta, betaStatus := startAgent(t, proxyAddr, agentStartOptions{RoutingLabel: agentBeta})
	t.Cleanup(func() {
		cancelBeta()
		<-doneBeta
	})
	waitAgentConnected(t, betaStatus)

	assertAnonymousLabelCollisionRejected(t, proxyAddr, agentAlpha, alphaStatus)

	tid := h.DeployTemplateSpecMap(lateBindTemplateSpec("multi-agent-isolation"), "")

	iidAlpha := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-alpha", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{
			"path": stub,
			"env":  map[string]any{"STUBCHILD_EXEC_LOG": execLogAlpha},
		}},
		agentAlpha,
	)
	iidBeta := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-beta", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{
			"path": stub,
			"env":  map[string]any{"STUBCHILD_EXEC_LOG": execLogBeta},
		}},
		agentBeta,
	)
	require.NotEqual(t, iidAlpha, iidBeta, "the two instances must be distinct")

	waitInstanceLateBindTerminal(t, h, iidAlpha)
	waitInstanceLateBindTerminal(t, h, iidBeta)

	require.Equal(t, 1, countWorkerRuns(t, h, iidAlpha), "alpha instance worker must complete on exactly one dispatch (no cross-agent routing)")
	require.Equal(t, 1, countWorkerRuns(t, h, iidBeta), "beta instance worker must complete on exactly one dispatch (no cross-agent routing)")

	alphaLine := waitForExecLog(t, execLogAlpha)
	require.Equal(t, agentAlpha, alphaLine.Env,
		"alpha instance's dispatch must have been spawned by the alpha agent, not a cross-routed one")
	betaLine := waitForExecLog(t, execLogBeta)
	require.Equal(t, agentBeta, betaLine.Env,
		"beta instance's dispatch must have been spawned by the beta agent, not a cross-routed one")

	cancelAlpha()
	<-doneAlpha

	cancelAlpha2, doneAlpha2, alphaStatus2 := startAgent(t, proxyAddr, agentStartOptions{RoutingLabel: agentAlpha})
	t.Cleanup(func() {
		cancelAlpha2()
		<-doneAlpha2
	})
	waitAgentConnected(t, alphaStatus2)

	iidAlphaReconnect := h.CreateInstanceWithServiceBindingsAndTarget(tid, "ck-alpha-reconnect", "",
		map[string]any{},
		map[string]any{lateBindServiceName: map[string]any{"path": stub}},
		agentAlpha,
	)
	waitInstanceLateBindTerminal(t, h, iidAlphaReconnect)
}

// @story: host-agent-anonymous-mode
func assertAnonymousLabelCollisionRejected(t *testing.T, proxyAddr, collidingLabel, aliveStatusFile string) {
	t.Helper()
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	stream, err := genv1.NewHostAgentClient(conn).Connect(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               sillyname.AnonymousCredentialSentinel,
		AgentLabel:           "collision-attacker",
		RoutingLabel:         collidingLabel,
		LocalCallbackBaseUrl: "http://127.0.0.1:1",
	}}}))

	_, recvErr := stream.Recv()
	require.Error(t, recvErr, "the proxy must reject a colliding routing_label rather than displace the live agent")
	require.Equal(t, codes.AlreadyExists, status.Code(recvErr),
		"colliding anonymous routing_label must be rejected AlreadyExists so the connected agent keeps its routing identity")

	body, err := os.ReadFile(aliveStatusFile)
	require.NoError(t, err, "the pre-existing agent's status file must still be readable after a rejected collision")
	var snap hostagent.StatusSnapshot
	require.NoError(t, json.Unmarshal(body, &snap))
	require.True(t, snap.Connected,
		"the pre-existing agent must remain connected — rejection is what proves it kept its routing identity")
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
