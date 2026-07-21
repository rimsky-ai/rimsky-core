// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestTrySchedulerTick_ExcludesAcrossLockerInstances(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	heldA, releaseA, err := lockerA.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker A TrySchedulerTick: %v", err)
	}
	if !heldA {
		t.Fatal("locker A TrySchedulerTick returned held=false on a fresh path")
	}

	heldB, releaseB, err := lockerB.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker B TrySchedulerTick: %v", err)
	}
	if heldB {
		releaseB()
		t.Fatal("locker B acquired the tick lock while locker A still held it — cross-instance exclusion broken")
	}
	if releaseB != nil {
		t.Fatal("locker B got a non-nil release fn with held=false")
	}

	releaseA()

	heldB2, releaseB2, err := lockerB.TrySchedulerTick(ctx)
	if err != nil {
		t.Fatalf("locker B TrySchedulerTick after release: %v", err)
	}
	if !heldB2 {
		t.Fatal("locker B could not acquire the tick lock after locker A released it")
	}
	releaseB2()
}

func TestAcquireMigrationLock_BlocksAcrossLockerInstances(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	releaseA, err := lockerA.AcquireMigrationLock(ctx)
	if err != nil {
		t.Fatalf("locker A AcquireMigrationLock: %v", err)
	}

	acquired := make(chan func() error, 1)
	errCh := make(chan error, 1)
	go func() {
		rel, err := lockerB.AcquireMigrationLock(ctx)
		if err != nil {
			errCh <- err
			return
		}
		acquired <- rel
	}()

	select {
	case <-acquired:
		t.Fatal("locker B acquired the migration lock while locker A still held it — cross-instance exclusion broken")
	case err := <-errCh:
		t.Fatalf("locker B AcquireMigrationLock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := releaseA(); err != nil {
		t.Fatalf("locker A release: %v", err)
	}

	select {
	case releaseB := <-acquired:
		if err := releaseB(); err != nil {
			t.Fatalf("locker B release: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("locker B AcquireMigrationLock after release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("locker B never acquired the migration lock after locker A released it")
	}
}

func TestAcquireMigrationLock_HonorsContextCancel(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	lockerA := newAdvisoryLocker(dbPath)
	lockerB := newAdvisoryLocker(dbPath)

	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer acquireCancel()
	releaseA, err := lockerA.AcquireMigrationLock(acquireCtx)
	if err != nil {
		t.Fatalf("locker A AcquireMigrationLock: %v", err)
	}
	defer func() { _ = releaseA() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := lockerB.AcquireMigrationLock(ctx); err == nil {
		t.Fatal("locker B AcquireMigrationLock returned nil error while locker A held the lock and ctx expired")
	}
}

func TestTakeNamedLockInTx_MutualExclusionComesFromImmediateTxLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := open(ctx, persistence.SQLiteConfig{Path: filepath.Join(dir, "lockrace.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db, ok := DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE lock_race_probe (n INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create scratch table: %v", err)
	}

	store := d.Tables()
	locker := d.AdvisoryLocker()

	const racers = 16
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			errs <- store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := locker.TakeNamedLockInTx(ctx, tx, "lock-race-probe"); err != nil {
					return err
				}
				sTx, err := unwrapTx(tx)
				if err != nil {
					return err
				}
				var n int
				row := sTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lock_race_probe`)
				if err := row.Scan(&n); err != nil {
					return err
				}
				_, err = sTx.ExecContext(ctx, `INSERT INTO lock_race_probe (n) VALUES (?)`, n)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("racer transaction: %v", err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lock_race_probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != racers {
		t.Fatalf("lock_race_probe has %d rows, want %d — TakeNamedLockInTx is a no-op on sqlite, so this "+
			"only serializes via the _txlock=immediate DSN pragma making every Transaction a write tx from "+
			"BEGIN; a lost update here means that pragma stopped closing the read-then-write window", count, racers)
	}

	seen := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT n FROM lock_race_probe ORDER BY n`)
	if err != nil {
		t.Fatalf("scan probe rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan n: %v", err)
		}
		if seen[n] {
			t.Fatalf("duplicate observed count %d — two racers read the same pre-insert COUNT(*), proving the "+
				"check-then-insert window was not serialized", n)
		}
		seen[n] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
}
