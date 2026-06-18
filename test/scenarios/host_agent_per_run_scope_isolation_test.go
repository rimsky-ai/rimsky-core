// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
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
				"stores": []map[string]any{
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

func TestHostAgentPerRunScopeIsolation(t *testing.T) {

	pidLog := t.TempDir() + "/stub-pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	fx := newHostAgentFixture(t, fixtureOpts{
		withAgent: true,
		stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
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

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		scopeToPartition := fanOutScopePartitions(fx, worker.ID)
		byPartition := pidsByPartition(t, pidLog, scopeToPartition)
		if partitionsServedByDistinctChildren(byPartition, partitionKeys) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	scopeToPartition := fanOutScopePartitions(fx, worker.ID)
	byPartition := pidsByPartition(t, pidLog, scopeToPartition)
	raw := readPIDLog(t, pidLog)
	t.Fatalf("each fan-out partition must dispatch into its own isolated late-bound child (a distinct pid per partition key); "+
		"want keys %v served by disjoint, present pid sets, got pid-by-partition=%v "+
		"(raw run_scope→pid log=%v — an empty run_scope_id key means the supervisor never threaded it onto ExecuteRequest; "+
		"scope→partition map=%v)",
		partitionKeys, byPartition, raw, scopeToPartition)
}

func fanOutScopePartitions(fx *hostAgentFixture, nodeID shared.UUID) map[string]string {
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
