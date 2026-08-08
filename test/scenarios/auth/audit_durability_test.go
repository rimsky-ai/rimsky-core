// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log

package auth_test

import (
	"context"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const auditLoadRequests = 1500

const auditLoadConcurrency = 64

func TestAuditDurability_NoDropsUnderConcurrentLoad(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	const loadKeyName = "audit-load-key"
	code, loadBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
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
			st, _ := f.request(t, "GET", "/v1/auth/keys", loadKey, nil)
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

	got := countAttemptedRows(t, f, loadKeyName, "GET", "/v1/auth/keys")
	if got != auditLoadRequests {
		t.Fatalf("audit drop detected: synchronous write must land one auth.access_attempted row per request; "+
			"fired %d, found %d", auditLoadRequests, got)
	}
}

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
				persistence.EventListFilter{KindIn: []string{auth.EventAccessAttempted}},
				persistence.ListPagination{Limit: 500, Cursor: cursor}, tx)
			return err
		}); err != nil {
			t.Fatalf("Events.List: %v", err)
		}
		for _, e := range page.Events {
			kn, _ := e.Payload.Map()["key_name"].(string)
			m, _ := e.Payload.Map()["request_method"].(string)
			p, _ := e.Payload.Map()["request_path"].(string)
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
