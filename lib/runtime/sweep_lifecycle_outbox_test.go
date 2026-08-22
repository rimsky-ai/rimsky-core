// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type sweepingOutboxTables struct {
	persistence.Tables
	outbox *recordingLifecycleOutbox
}

func (s *sweepingOutboxTables) LifecycleOutbox() persistence.LifecycleOutboxTable { return s.outbox }

func (s *sweepingOutboxTables) Transaction(ctx context.Context, fn func(context.Context, persistence.Tx) error) error {
	s.outbox.transactions++
	return fn(ctx, nil)
}

type recordingLifecycleOutbox struct {
	persistence.LifecycleOutboxTable
	cutoffs      []time.Time
	deleted      int64
	transactions int
}

func (r *recordingLifecycleOutbox) DeleteOlderThan(_ context.Context, cutoff time.Time, _ persistence.Tx) (int64, error) {
	r.cutoffs = append(r.cutoffs, cutoff)
	return r.deleted, nil
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestSweepLifecycleOutboxDeletesTheRowsOlderThanTheTrailingWindow(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	outbox := &recordingLifecycleOutbox{deleted: 3}

	got, err := SweepLifecycleOutbox(context.Background(), &sweepingOutboxTables{outbox: outbox},
		RetentionConfig{LifecycleOutboxTrailing: 30 * 24 * time.Hour}, now, nil)
	if err != nil {
		t.Fatalf("SweepLifecycleOutbox: %v", err)
	}
	if got != 3 {
		t.Fatalf("deleted = %d, want the 3 the table reported", got)
	}
	if len(outbox.cutoffs) != 1 {
		t.Fatalf("the sweep issued %d deletes, want 1", len(outbox.cutoffs))
	}
	if want := now.Add(-30 * 24 * time.Hour); !outbox.cutoffs[0].Equal(want) {
		t.Fatalf("cutoff = %s, want %s", outbox.cutoffs[0], want)
	}
	if outbox.transactions != 1 {
		t.Fatalf("the sweep opened %d transactions, want 1: every table method takes an explicit tx",
			outbox.transactions)
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestSweepLifecycleOutboxKeepsEveryRowWhenTheOperatorDisablesIt(t *testing.T) {
	outbox := &recordingLifecycleOutbox{deleted: 3}

	got, err := SweepLifecycleOutbox(context.Background(), &sweepingOutboxTables{outbox: outbox},
		RetentionConfig{LifecycleOutboxTrailing: 0}, time.Now(), nil)
	if err != nil {
		t.Fatalf("SweepLifecycleOutbox: %v", err)
	}
	if got != 0 || len(outbox.cutoffs) != 0 {
		t.Fatalf("a zero trailing window swept %d rows over %d deletes; it must keep every undelivered "+
			"lifecycle event until its subscriber answers", got, len(outbox.cutoffs))
	}
}
