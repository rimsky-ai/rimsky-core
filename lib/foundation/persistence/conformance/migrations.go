// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testMigrationIdempotency(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = d.Migrate(ctx, shared.SilentLogger{})
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("concurrent Migrate %d: %v", i, e)
		}
	}

	if d.Queue() == nil {
		t.Fatalf("Queue() nil after re-migrate")
	}
	if d.Tables() == nil {
		t.Fatalf("Store() nil after re-migrate")
	}
}
