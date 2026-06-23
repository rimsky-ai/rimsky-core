// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scopesconflict

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const overlapProducerName = "overlap"

var terminalStates = map[string]bool{"fresh": true, "failed": true}

func TestScopesConflict_OverlapHeldOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	producerEndpoint := harness.StartOverlapClaimProducerOnNetwork(ctx, t, netName, "overlap-producer")
	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer(overlapProducerName, producerEndpoint, "sync"),
		harness.WithExecutor("stub", execEndpoint),
	)

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(%s): %v", ep.HostDSN, err)
	}
	t.Cleanup(pool.Close)

	t.Run("top_level_acquisition", func(t *testing.T) {
		runTopLevelOverlapCase(ctx, t, ep, pool)
	})
	t.Run("fanout_subclaim", func(t *testing.T) {
		runFanOutOverlapCase(ctx, t, ep, pool)
	})
}

func runTopLevelOverlapCase(ctx context.Context, t *testing.T, ep harness.RimskyEndpoint, pool *pgxpool.Pool) {
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "scopes-conflict-top-level",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "acquirer",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant/a",
							"intent":   "rw",
							"alias":    "held",
							"lifetime": "durable",
						},
					},
				},
				{
					"type":     "verifier",
					"executor": "stub",
					"holds": map[string]any{
						"held": map[string]any{"from": "acquirer"},
					},
					"subscribes": []map[string]any{
						{"node": "acquirer", "type": "terminal/*", "force_upstream_refresh": false},
					},
				},
				{
					"type":     "contender",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant/a/x",
							"intent":   "rw",
						},
					},
					"subscribes": []map[string]any{
						{"node": "verifier", "type": "terminal/*", "force_upstream_refresh": false},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-scopes-conflict-top-level")

	waitForNodeTerminal(t, ep, instanceID, "acquirer", 120*time.Second)
	waitForNodeTerminal(t, ep, instanceID, "verifier", 120*time.Second)

	deadline := time.Now().Add(30 * time.Second)
	got := 1
	for time.Now().Before(deadline) {
		got = countAcquiredClaimScopeRows(ctx, t, pool, instanceID)
		if got >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got != 1 {
		t.Fatalf("top-level overlap: want exactly 1 acquired claim_scope row for producer %q "+
			"(only the durable acquirer; the contender's overlapping non-byte-equal scope held off), "+
			"got %d — rimsky did not consult the producer's ScopesConflict during acquisition",
			overlapProducerName, got)
	}
}

func runFanOutOverlapCase(ctx context.Context, t *testing.T, ep harness.RimskyEndpoint, pool *pgxpool.Pool) {
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "scopes-conflict-fanout",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "fan-parent",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{
							"name":     overlapProducerName,
							"selector": "tenant",
							"intent":   "rw",
							"alias":    "data",
						},
					},
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{"partition_keys":["a","a/x"]}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-scopes-conflict-fanout")

	deadline := time.Now().Add(45 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		got = countSubClaimRows(ctx, t, pool, instanceID)
		if got >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got >= 2 {
		t.Fatalf("fan-out overlap: the acquisition tx committed BOTH overlapping sub-claim rows for "+
			"producer %q (got %d), but the conflicting sub-claim must be rejected so the tx commits "+
			"neither — AcquireSubClaims did not conflict-check the overlapping sub-scopes against the "+
			"producer's ScopesConflict", overlapProducerName, got)
	}
}

func countAcquiredClaimScopeRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = $2
		    AND ch.parent_claim_handle_id IS NULL
		    AND ch.address IS NOT NULL`,
		instanceID, overlapProducerName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count acquired claim_scope rows: %v", err)
	}
	return n
}

func countSubClaimRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = $2
		    AND ch.parent_claim_handle_id IS NOT NULL`,
		instanceID, overlapProducerName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count sub-claim rows: %v", err)
	}
	return n
}

func deployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "scopes-conflict", instanceKey)
	return resp.InstanceID
}

func waitForNodeTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				if terminalStates[lastState] {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last state=%q",
		nodeType, instanceID, deadline, lastState)
}
