// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducers

import (
	"context"
	"strings"
	"testing"
	"time"

	cpconf "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/server"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// @story: claim-producer-conformance
func TestFsStore_ClaimProducerConformance(t *testing.T) {
	root := t.TempDir()
	grpcAddr, teardown := startFilesystemStore(t, server.Config{
		Root:          root,
		SweepInterval: 60 * time.Second,
	})
	t.Cleanup(teardown)

	client, err := harness.DialClaimProducer(context.Background(), "conformance-target", "grpc://"+grpcAddr)
	if err != nil {
		t.Fatalf("DialClaimProducer: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	results := cpconf.Run(context.Background(), client)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			t.Errorf("claim-producer conformance FAIL %s: %v", r.Name, r.Err)
		} else {
			t.Logf("claim-producer conformance ok %s", r.Name)
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d claim-producer conformance checks failed", failed, len(results))
	}

	for _, name := range []string{
		"SplitScopeListShapeReturnsAllElements",
		"SplitScopeListShapePreservesPartitionKey",
		"SplitScopeListShapePreservesPayload",
		"SplitScopeListShapeAddressFieldEmpty",
	} {
		assertFsResultPassing(t, results, name)
	}
}

func assertFsResultPassing(t *testing.T, results []cpconf.CheckResult, name string) {
	t.Helper()
	for _, r := range results {
		if r.Name != name {
			continue
		}
		if r.Err != nil {
			t.Errorf("claim-producer conformance row %q present but FAILED: %v", name, r.Err)
		}
		return
	}
	rowNames := make([]string, 0, len(results))
	for _, r := range results {
		rowNames = append(rowNames, r.Name)
	}
	t.Errorf("claim-producer conformance result set is missing the %q row (have: [%s])",
		name, strings.Join(rowNames, ", "))
}
