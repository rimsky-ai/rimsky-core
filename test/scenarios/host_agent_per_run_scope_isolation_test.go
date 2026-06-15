// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_per_run_scope_isolation_test.go — end-to-end proof of the
// per-run-scope spawn-isolation invariant (concept:host-agent-proxy,
// story S-hostagent-per-run-scope-isolation). One instance fans out into
// two concurrent run-scopes that both dispatch the same late-bound
// executor node. The invariant: each run-scope gets its OWN isolated
// late-bound child process — one spawn per (run_scope, binding) — so
// concurrent run-scopes never share executor process state.
//
// Real stack: control-api + supervisor + rimsky-host-agent-proxy +
// in-process host-agent + a real exec'd stubchild whose Execute appends
// "<run_scope_id> <pid>" to STUBCHILD_PID_LOG. The fan-out claim is
// served by a real remote stub store advertising supports_split_scope,
// so the supervisor splits the parent scope into two partition
// run-scopes and dispatches a leaf per partition through the proxy.
//
// RED (current tree): the supervisor never threads run_scope_id onto the
// ExecuteRequest, and the proxy keys spawns by instance id
// (dispatch.go::resolveAndSpawn `scopeID := instanceID`). So the stub logs
// an EMPTY run_scope_id for every dispatch and the proxy's spawn dedup is
// keyed on the instance, not the run-scope — there is no per-run-scope
// attribution at all (the children that happen to spawn do so via a
// concurrent-first-dispatch race, not deterministic per-run-scope
// isolation). This test maps each logged run_scope_id back to its
// partition via the persisted run-tree and requires every partition to be
// served by its own distinct child; with the empty run_scope_id, no
// partition is attributable, so it FAILS until the run-scope-keyed spawn
// invariant is made real. A later GREEN pass threads run_scope_id onto the
// dispatch wire and re-keys the proxy's spawn dedup on it.
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

// perRunScopeStoreName is the operator-facing remote-store config name the
// fan-out node's claim alias binds to. The store advertises
// supports_split_scope and decodes {"partition_keys":[...]} into one
// sub-scope (hence one partition run-scope) per key.
const perRunScopeStoreName = "fanout-store"

// fanOutLateBindTemplateSpec builds the raw spec map for a single fan-out
// node template. The node:
//   - references the late-bound executor (so registration is bypassed via
//     late_bind_services, exactly like the non-fan-out late-bind template);
//   - acquires a claim from the remote stub store under alias "data";
//   - fans out across that claim into the given partition keys, producing
//     one concurrent leaf run-scope per key.
//
// The node carries no attributes block so dispatch skips the
// executor_schema_unavailable gate — the spawned binary's Capabilities are
// the authority for late-bound nodes (mirrors lateBindTemplateSpec).
func fanOutLateBindTemplateSpec(name string, partitionKeys []string) map[string]any {
	// @deliberate: partition_request is the producer-interpreted bytes the stub store
	// decodes into one SubScopeDescriptor (→ one partition run-scope) per
	// key. Two keys ⇒ two concurrent run-scopes.
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
	// @deliberate: Not parallel: execs real child processes and binds free ports; keep it
	// serial so the port reservations and process reaping stay predictable.

	// @deliberate: Set the PID-log env BEFORE the fixture starts so every spawned child
	// inherits it (env is inherited per spawn.go). Each Execute the stub
	// serves appends "<run_scope_id> <pid>" here.
	pidLog := t.TempDir() + "/stub-pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)

	// @deliberate: Real remote claim producer advertising supports_split_scope. The
	// fixture's ClaimProducer surface decodes {"partition_keys":[...]} into
	// one SubScopeDescriptor per key — the same wiring the fan-out scenarios
	// use to drive concurrent partition run-scopes.
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

	// @constraint: The observable, bound to the REAL run-scope topology: each fan-out
	// partition key must be served by its OWN isolated late-bound child.
	// We key on the partition_key (stable: "alpha"/"beta") rather than the
	// run_scope_id directly, because a failed leaf can be retried under a
	// fresh partition run-scope for the same key — all of which still map
	// to one logical partition and must all hit that partition's child.
	//
	// The stub appends "<run_scope_id> <pid>" per Execute. We map each
	// logged run_scope_id back to its partition_key via the persisted
	// run-tree, accumulate partition_key → set of pids, and require:
	//   1. both requested partition keys appear in the log — proving the
	//      real run_scope_id was threaded onto ExecuteRequest and reached
	//      the spawned child (today it is never set, so logged scope ids are
	//      empty and map to NO partition key);
	//   2. the pid sets for the two partition keys are disjoint — one
	//      isolated child per (run_scope, binding) (today all run-scopes of
	//      the instance collapse onto a single shared child → one pid).
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

// fanOutScopePartitions reads the run-tree and returns run_scope_id (string)
// → partition_key for every fan-out partition run-scope under the node.
// Each fan-out leaf dispatches under the same node row but in its own
// partition run-scope (rimsky_run_scopes.partition_key set); the leaf
// node-run's run_scope_id points at it.
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

// readPIDLog reads STUBCHILD_PID_LOG into raw run_scope_id → set-of-pids.
// Each line is "<run_scope_id> <pid>". An absent file yields an empty map.
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
			// @deliberate: "<run_scope_id> <pid>" — the GREEN shape once the supervisor
			// threads run_scope_id onto ExecuteRequest.
			scope, pid = fields[0], fields[1]
		case 1:
			// @deliberate: "<pid>" — today's shape: run_scope_id is empty, so the stub's
			// "%s %d" formats to a leading space and Fields drops it. Record
			// it under the empty-scope sentinel so the collapse is visible in
			// the diagnostic (it maps to NO partition, hence the gap).
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

// pidsByPartition folds the raw run_scope→pid log through the
// scope→partition map, yielding partition_key → set of pids that served it.
// A logged run_scope_id with no partition mapping (e.g. the empty scope id
// the supervisor emits today) contributes to no partition.
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

// partitionsServedByDistinctChildren reports whether every requested
// partition key was served (present in the log) AND no pid is shared across
// two partition keys — i.e. each partition got its own isolated child.
func partitionsServedByDistinctChildren(byPartition map[string]map[string]bool, partitionKeys []string) bool {
	pidOwner := map[string]string{} // @constraint: pid → partition key that first claimed it
	for _, key := range partitionKeys {
		pids := byPartition[key]
		if len(pids) == 0 {
			return false // @deliberate: this partition has not dispatched into any child yet
		}
		for pid := range pids {
			if owner, seen := pidOwner[pid]; seen && owner != key {
				return false // @deliberate: a single child served two partitions (shared spawn)
			}
			pidOwner[pid] = key
		}
	}
	return true
}
