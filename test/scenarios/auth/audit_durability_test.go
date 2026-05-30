// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Audit durability under concurrent load. The per-request
// auth.access_attempted row is written SYNCHRONOUSLY in the request
// goroutine (controlapi.AuthState.insertEvent) — there is no
// queue/worker/buffer and therefore no drop path. This test fires a
// burst of authenticated requests larger than the bounded queue the
// old async dispatcher carried (1024) and asserts that exactly one
// auth.access_attempted row landed per request: zero drops.
//
// @concept: event-log

package auth_test

import (
	"context"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// auditLoadRequests is the burst size. Deliberately larger than the
// old async dispatcher's bounded queue (the retired auditQueueSize was
// 1024) so that, under the old code, the 4-worker pool could not keep
// up and `submit` would have silently dropped rows. Under the
// synchronous write every request's row must land.
const auditLoadRequests = 1500

// auditLoadConcurrency caps in-flight HTTP requests so the test does
// not exhaust ephemeral ports / file descriptors while still keeping
// enough requests outstanding to contend on the single SQLite write
// connection — the realistic stress the synchronous write must
// survive without losing a row.
const auditLoadConcurrency = 64

func TestAuditDurability_NoDropsUnderConcurrentLoad(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Bootstrap an admin via anonymous mode. This emits one
	// auth.access_attempted row of its own (POST /auth/keys), which we
	// exclude from the count below by scoping to GET /auth/keys under
	// the dedicated load key.
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// Mint a dedicated read-only key with a unique name so its audit
	// rows are unambiguously attributable to this test's burst.
	const loadKeyName = "audit-load-key"
	code, loadBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        loadKeyName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint load key: %d %+v", code, loadBody)
	}
	loadKey, _ := loadBody["plaintext"].(string)
	if loadKey == "" {
		t.Fatalf("load key plaintext missing: %+v", loadBody)
	}

	// Fire auditLoadRequests authenticated GET /auth/keys requests,
	// bounded to auditLoadConcurrency in flight. Each one must succeed
	// (200) and synchronously write its auth.access_attempted row
	// before the gate returns.
	sem := make(chan struct{}, auditLoadConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var badStatus int
	for i := 0; i < auditLoadRequests; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			st, _ := f.request(t, "GET", "/auth/keys", loadKey, nil)
			if st != 200 {
				mu.Lock()
				if badStatus == 0 {
					badStatus = st
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if badStatus != 0 {
		t.Fatalf("at least one load request did not return 200: got %d", badStatus)
	}

	// Synchronous write: by the time every request returned, its row is
	// committed and visible. Page through all auth.access_attempted
	// rows and count those attributable to the load key's GET /auth/keys
	// burst. Exactly auditLoadRequests must be present — no drops.
	got := countAttemptedRows(t, f, loadKeyName, "GET", "/auth/keys")
	if got != auditLoadRequests {
		t.Fatalf("audit drop detected: synchronous write must land one auth.access_attempted row per request; "+
			"fired %d, found %d", auditLoadRequests, got)
	}
}

// countAttemptedRows pages through every auth.access_attempted row and
// counts those whose key_name / request_method / request_path match
// the supplied burst signature. Pagination (rather than a single huge
// limit) keeps the count robust regardless of page size.
func countAttemptedRows(t *testing.T, f *authFixture, keyName, method, path string) int {
	t.Helper()
	ctx := context.Background()
	count := 0
	cursor := ""
	for {
		var page persistence.EventListResult
		if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			page, err = f.db.Tables().Events().List(ctx,
				persistence.EventListFilter{Kind: auth.EventAccessAttempted},
				persistence.ListPagination{Limit: 500, Cursor: cursor}, tx)
			return err
		}); err != nil {
			t.Fatalf("Events.List: %v", err)
		}
		for _, e := range page.Events {
			kn, _ := e.Payload["key_name"].(string)
			m, _ := e.Payload["request_method"].(string)
			p, _ := e.Payload["request_path"].(string)
			if kn == keyName && m == method && p == path {
				count++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return count
}
