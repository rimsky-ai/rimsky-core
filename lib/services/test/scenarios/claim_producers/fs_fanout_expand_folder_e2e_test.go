// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducers

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// @story: fs-fanout-expand-folder
func TestFSFanOutExpandFolderE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
			SeedFolders: [][]string{{"data"}},
		})

	wantKeys := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range wantKeys {
		if err := os.WriteFile(filepath.Join(fs.HostDir, "data", name), []byte(name), 0o644); err != nil {
			t.Fatalf("seed file %s: %v", name, err)
		}
	}

	executorEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", executorEndpoint),
	)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "fs-fanout-expand-folder",
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
						"partition_request": `{"expand_folder":{"filter":"*.txt"}}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-fs-fanout-expand-folder", "fs-fanout-expand-folder")

	ep.RequireNodeTerminalSucceeded(t, instanceID, "fan-parent")

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(%s): %v", ep.HostDSN, err)
	}
	t.Cleanup(pool.Close)

	sortedWant := append([]string{}, wantKeys...)
	sort.Strings(sortedWant)

	var gotKeys []string
	for {
		gotKeys = partitionKeys(ctx, t, pool, instanceID)
		if len(gotKeys) >= len(sortedWant) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	sort.Strings(gotKeys)
	if len(gotKeys) != len(sortedWant) {
		t.Fatalf("want one fanout_partition RunScope per matched file (%v), got %v — "+
			"the expand_folder partition_request must open exactly one sub-claim per matching file "+
			"against the bundled filesystem producer", sortedWant, gotKeys)
	}
	for i, want := range sortedWant {
		if gotKeys[i] != want {
			t.Fatalf("partition keys mismatch: want %v, got %v — sub-scope partition keys must "+
				"be the matched files' relative paths under the parent folder claim", sortedWant, gotKeys)
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
		t.Fatalf("want %d sub-claim rows opened via SplitScope expand_folder against the bundled "+
			"filesystem producer, got %d", len(wantKeys), subClaims)
	}
}
