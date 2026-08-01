// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testAdvisoryLockerSchedulerTick(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	c := d.AdvisoryLocker()
	if c == nil {
		t.Fatalf("driver.AdvisoryLocker() returned nil")
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
