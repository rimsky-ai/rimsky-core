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
	netName := harness.NewNetwork(ctx, t)

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

	instanceID := smokeCreateInstance(t, ep, templateID, "stores-redesign-1")

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("connect rimsky state postgres: %v", err)
	}
	defer pool.Close()

	const cycles = 5
	const perCycle = 30 * time.Second

	for n := 1; n <= cycles; n++ {
		ep.RequireNodeTerminalSucceeded(t, instanceID, "claim-acquirer", 30*time.Second)
		requireCommittedDocsClaimFolder(ctx, t, pool, instanceID, n)
		status, raw := ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/pause", instanceID), nil)
		if status != http.StatusOK {
			t.Fatalf("pause %d: %d %s", n, status, string(raw))
		}
		status, raw = ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/debug/override", instanceID),
			map[string]any{
				"action":    "invalidate_node",
				"node_type": "claim-acquirer",
			})
		if status != http.StatusOK {
			t.Fatalf("debug override %d: %d %s", n, status, string(raw))
		}
		status, raw = ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/resume", instanceID), nil)
		if status != http.StatusOK {
			t.Fatalf("resume %d: %d %s", n, status, string(raw))
		}
		_ = perCycle
	}

	ep.RequireNodeTerminalSucceeded(t, instanceID, "claim-acquirer", perCycle)
	requireCommittedDocsClaimFolder(ctx, t, pool, instanceID, cycles+1)

	folders := committedDocsClaimFolders(ctx, t, pool, instanceID)
	if len(folders) != cycles+1 {
		t.Fatalf("want %d committed claim_scope rows for producer %q across %d cycles, got %d: %v",
			cycles+1, "docs", cycles, len(folders), folders)
	}
	distinct := map[string]bool{}
	for _, f := range folders {
		distinct[f] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("recycle pick policy picked the same folder every cycle (%v) — the ring never rotated across %d cycles seeded with 3 folders",
			folders, cycles)
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

func requireCommittedDocsClaimFolder(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string, cycle int) {
	t.Helper()
	folders := committedDocsClaimFolders(ctx, t, pool, instanceID)
	if len(folders) < cycle {
		t.Fatalf("cycle %d: want at least %d committed claim_scope rows for producer %q, got %d: %v",
			cycle, cycle, "docs", len(folders), folders)
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
