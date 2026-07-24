// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestClaimProducersRedesignSmoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemStore(ctx, t, netName, "store-filesystem",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs-ring": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}, {"docs", "beta"}, {"docs", "gamma"}},
		})

	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := smokeDeployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "stores-redesign-smoke",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "claim-acquirer",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	})

	const instanceKey = "stores-redesign-1"
	instanceID := smokeCreateInstance(t, ep, templateID, instanceKey)

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("connect rimsky state postgres: %v", err)
	}
	defer pool.Close()

	const cycles = 5

	for n := 1; n <= cycles; n++ {
		waitForCommittedDocsClaimFolders(ctx, t, ep, pool, instanceID, n)
		ep.EmptyWakeAfterCreate(t, instanceID, fmt.Sprintf("smoke-cycle-%d", n), instanceKey)
	}

	folders := waitForCommittedDocsClaimFolders(ctx, t, ep, pool, instanceID, cycles+1)
	if len(folders) != cycles+1 {
		t.Fatalf("want exactly %d committed claim_scope rows for producer %q after the initial wake plus %d re-wakes, got %d: %v",
			cycles+1, "docs", cycles, len(folders), folders)
	}
	distinct := map[string]bool{}
	for _, f := range folders {
		distinct[f] = true
	}
	if len(distinct) != 3 {
		t.Fatalf("recycle pick policy must rotate through all 3 seeded folders across %d sequential commits, got %v",
			cycles+1, folders)
	}
}

func committedDocsClaimFolders(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT ch.address FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = 'docs'
		    AND ch.state = 'committed'
		  ORDER BY ch.claimed_at`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("query committed docs claim_scope rows: %v", err)
	}
	defer rows.Close()
	var folders []string
	for rows.Next() {
		var addrRaw []byte
		if err := rows.Scan(&addrRaw); err != nil {
			t.Fatalf("scan claim_handles.address: %v", err)
		}
		var addrPath string
		if err := json.Unmarshal(addrRaw, &addrPath); err != nil {
			t.Fatalf("decode claim_handles.address %s: %v", string(addrRaw), err)
		}
		folders = append(folders, filepath.Base(addrPath))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate committed docs claim_scope rows: %v", err)
	}
	return folders
}

func waitForCommittedDocsClaimFolders(
	ctx context.Context, t *testing.T, ep harness.RimskyEndpoint, pool *pgxpool.Pool,
	instanceID string, want int,
) []string {
	t.Helper()
	for {
		folders := committedDocsClaimFolders(ctx, t, pool, instanceID)
		if len(folders) >= want {
			return folders
		}
		status, obs, _ := ep.GetNodeObservability(t, instanceID, "claim-acquirer")
		if status == http.StatusOK && obs.RunSummary.FailedCount > 0 {
			t.Fatalf("waiting for %d committed claim_scope rows (have %d: %v): node claim-acquirer failed; run_summary=%+v",
				want, len(folders), folders, obs.RunSummary)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func smokeDeployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
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
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func smokeCreateInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
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
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "smoke", instanceKey)
	return resp.InstanceID
}
