// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducers

import (
	"context"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

// @story: fanout-list-array
func TestFSFanOutListArrayE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
			SeedFolders: [][]string{{"data"}},
		})

	executorEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", executorEndpoint),
	)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "fs-fanout-list-array",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "fan-parent",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "data", "intent": "rw", "alias": "data"},
					},
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{"list":[{"key":"item-1","payload":{"n":1}},{"key":"item-2","payload":{"n":2}},{"key":"item-3","payload":{"n":3}}]}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-fs-fanout-list", "fs-fanout-list")

	ep.RequireNodeTerminalSucceeded(t, instanceID, "fan-parent")

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(%s): %v", ep.HostDSN, err)
	}
	t.Cleanup(pool.Close)

	wantKeys := []string{"item-1", "item-2", "item-3"}
	var gotKeys []string
	awaited.Until(t, "the partition_request to open one fanout_partition RunScope per declared list item", func() bool {
		gotKeys = partitionKeys(ctx, t, pool, instanceID)
		return len(gotKeys) >= len(wantKeys)
	})
	sort.Strings(gotKeys)
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("want one fanout_partition RunScope per declared list item (%v), got %v — "+
			"the list partition_request must open exactly one sub-claim per item against the "+
			"bundled filesystem producer", wantKeys, gotKeys)
	}
	for i, want := range wantKeys {
		if gotKeys[i] != want {
			t.Fatalf("partition keys mismatch: want %v, got %v — sub-scope partition keys must "+
				"be exactly the declared list item keys", wantKeys, gotKeys)
		}
	}

	var subClaims int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.lock_kind = 'claim_scope'
		    AND ch.producer_name = 'docs'
		    AND ch.parent_claim_handle_id IS NOT NULL`,
		instanceID,
	).Scan(&subClaims); err != nil {
		t.Fatalf("count sub-claim rows: %v", err)
	}
	if subClaims != len(wantKeys) {
		t.Fatalf("want %d sub-claim rows opened via SplitScope against the bundled filesystem "+
			"producer, got %d", len(wantKeys), subClaims)
	}
}

func partitionKeys(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT partition_key FROM rimsky_run_scopes
		  WHERE instance_id = $1 AND partition_key <> ''`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("query partition run scopes: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan partition_key: %v", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate partition run scopes: %v", err)
	}
	return keys
}
