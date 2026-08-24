// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
)

const perRunScopeStoreName = "fanout-store"

func fanOutLateBindTemplateSpec(name string, partitionKeys []string) map[string]any {
	partitionRequest := `{"partition_keys":[`
	for i, k := range partitionKeys {
		if i > 0 {
			partitionRequest += ","
		}
		partitionRequest += `"` + k + `"`
	}
	partitionRequest += `]}`

	return map[string]any{
		"name":               name,
		"version":            "1",
		"late_bind_services": []string{lateBindServiceName},
		"nodes": []map[string]any{
			{
				"type":     "worker",
				"executor": lateBindServiceName,
				"claim_producers": []map[string]any{
					{"name": perRunScopeStoreName, "selector": "data", "intent": "rw", "alias": "data"},
				},
				"fan_out": map[string]any{
					"claim":             "data",
					"partition_request": partitionRequest,
					"error_policy":      map[string]any{"kind": "best_effort"},
				},
			},
		},
	}
}

func TestHostDaemonPerRunScopeIsolation(t *testing.T) {

	pidLog := t.TempDir() + "/stub-pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	fx := newHostDaemonFixture(t, fixtureOpts{
		withDaemon: true,
		producers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				perRunScopeStoreName: {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})

	partitionKeys := []string{"alpha", "beta"}
	tid := fx.h.DeployTemplateSpecMap(fanOutLateBindTemplateSpec("late-bind-per-run-scope", partitionKeys), fx.adminKey)
	iid := fx.createLateBindInstance(t, tid, "ck-per-run-scope", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker (fan-out) node should exist")

	awaited.Until(t, "each fan-out partition to dispatch into its own isolated late-bound child (a distinct pid per partition key)", func() bool {
		scopeToPartition := fanOutScopePartitions(fx, worker.ID)
		byPartition := pidsByPartition(t, pidLog, scopeToPartition)
		return partitionsServedByDistinctChildren(byPartition, partitionKeys)
	})

	fx.h.WaitForAllRunsTerminal(worker.ID)
	fx.h.WaitForSchedulerQuiescence()
	scopeToPartition := fanOutScopePartitions(fx, worker.ID)
	byPartition := pidsByPartition(t, pidLog, scopeToPartition)
	if !partitionsServedByDistinctChildren(byPartition, partitionKeys) {
		raw := readPIDLog(t, pidLog)
		t.Fatalf("a later best-effort fan-out dispatch broke partition isolation after it had "+
			"initially been satisfied; pid-by-partition=%v (raw run_scope→pid log=%v; scope→partition map=%v)",
			byPartition, raw, scopeToPartition)
	}
}

func fanOutScopePartitions(fx *hostDaemonFixture, nodeID shared.UUID) map[string]string {
	out := map[string]string{}
	fx.h.QuerySQL(`
		SELECT DISTINCT rs.id, rs.partition_key
		  FROM rimsky_node_runs nr
		  JOIN rimsky_run_scopes rs ON rs.id = nr.run_scope_id
		 WHERE nr.node_id = $1
		   AND rs.partition_key IS NOT NULL
		   AND rs.partition_key <> ''
	`, []any{nodeID}, func(scan func(...any) error) error {
		var id shared.UUID
		var key string
		if err := scan(&id, &key); err != nil {
			return err
		}
		out[id.String()] = key
		return nil
	})
	return out
}

func readPIDLog(t *testing.T, path string) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out
	}
	if err != nil {
		t.Fatalf("read pid log: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		var scope, pid string
		switch len(fields) {
		case 2:
			scope, pid = fields[0], fields[1]
		case 1:
			scope, pid = "", fields[0]
		default:
			continue
		}
		if out[scope] == nil {
			out[scope] = map[string]bool{}
		}
		out[scope][pid] = true
	}
	return out
}

func pidsByPartition(t *testing.T, pidLog string, scopeToPartition map[string]string) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	for scope, pids := range readPIDLog(t, pidLog) {
		part, ok := scopeToPartition[scope]
		if !ok {
			continue
		}
		if out[part] == nil {
			out[part] = map[string]bool{}
		}
		for pid := range pids {
			out[part][pid] = true
		}
	}
	return out
}

func partitionsServedByDistinctChildren(byPartition map[string]map[string]bool, partitionKeys []string) bool {
	pidOwner := map[string]string{}
	for _, key := range partitionKeys {
		pids := byPartition[key]
		if len(pids) == 0 {
			return false
		}
		for pid := range pids {
			if owner, seen := pidOwner[pid]; seen && owner != key {
				return false
			}
			pidOwner[pid] = key
		}
	}
	return true
}

func TestHostDaemonPerRunScopeReapIsolation(t *testing.T) {
	pidLog := t.TempDir() + "/stub-pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)

	fx := newHostDaemonFixture(t, fixtureOpts{withDaemon: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-reap-isolation")
	iid := fx.createLateBindInstance(t, tid, "ck-reap-isolation", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	conn, err := grpc.NewClient(fx.proxyServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	execClient := genv1.NewExecutorClient(conn)
	alphaScope := uuid.NewString()
	betaScope := uuid.NewString()

	dispatchReapIsolationChild(t, execClient, iid.String(), alphaScope)
	dispatchReapIsolationChild(t, execClient, iid.String(), betaScope)

	alphaPID := onlyPID(t, readScopePIDs(t, pidLog, alphaScope))
	betaPID := onlyPID(t, readScopePIDs(t, pidLog, betaScope))
	require.NotEqual(t, alphaPID, betaPID, "sibling run-scopes must spawn distinct children")

	_, err = genv1.NewLifecycleSubscriberClient(conn).OnRunScopeTerminal(context.Background(), &genv1.OnRunScopeTerminalRequest{
		RunScopeId:     alphaScope,
		InstanceId:     iid.String(),
		TerminalReason: "test_probe",
	})
	require.NoError(t, err, "OnRunScopeTerminal for the alpha run-scope must succeed")

	awaited.Until(t, "the alpha run-scope's child to be reaped", func() bool { return !processAlive(t, alphaPID) })

	require.True(t, processAlive(t, betaPID),
		"closing the alpha run-scope (%s) reaped the beta sibling's child (pid %s) too; sibling scopes must reap independently", alphaScope, betaPID)
}

func dispatchReapIsolationChild(t *testing.T, client genv1.ExecutorClient, instanceID, runScopeID string) {
	t.Helper()
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-rimsky-service-name", lateBindServiceName)
	_, err := client.Execute(ctx, &genv1.ExecuteRequest{
		InstanceId: instanceID,
		RunScopeId: runScopeID,
		NodeId:     uuid.NewString(),
	})
	require.NoError(t, err, "Execute for run-scope %s must succeed", runScopeID)
}

func readScopePIDs(t *testing.T, pidLog, scopeID string) map[string]bool {
	t.Helper()
	return readPIDLog(t, pidLog)[scopeID]
}

func onlyPID(t *testing.T, pids map[string]bool) string {
	t.Helper()
	require.Len(t, pids, 1, "expected exactly one pid to have served this run-scope, got %v", pids)
	for pid := range pids {
		return pid
	}
	return ""
}

func processAlive(t *testing.T, pidStr string) bool {
	t.Helper()
	pid, err := strconv.Atoi(pidStr)
	require.NoError(t, err, "pid %q must parse", pidStr)
	return syscall.Kill(pid, 0) == nil
}
