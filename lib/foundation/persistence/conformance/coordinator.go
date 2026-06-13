// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// coordinator.go — CoordinatorSchedulerTick conformance area.
//
// Inv 7: TrySchedulerTick mutual exclusion.
//
// Under SQLite the tick lock is an exclusive flock on a lock file
// beside the database file: exclusion holds across OS processes sharing
// the file (flock contends per open file description, so the in-process
// acquisitions exercised here contend exactly like two processes do).
package conformance

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testCoordinatorSchedulerTick(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	c := d.AdvisoryLocker()
	if c == nil {
		t.Fatalf("driver.Coordinator() returned nil")
	}

	got1, release1, err := c.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("TrySchedulerTick #1: %v", err)
	}
	if !got1 {
		t.Fatalf("TrySchedulerTick #1 returned held=false on a fresh DB")
	}

	got2, release2, err := c.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("TrySchedulerTick #2: %v", err)
	}
	if got2 {
		release2()
		release1()
		t.Fatalf("TrySchedulerTick #2 returned held=true while #1 still held")
	}

	// Release #1, then #3 should succeed.
	release1()
	got3, release3, err := c.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("TrySchedulerTick #3: %v", err)
	}
	if !got3 {
		t.Fatalf("TrySchedulerTick #3 returned held=false after release #1")
	}
	release3()
}
