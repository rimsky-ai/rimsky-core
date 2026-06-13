// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// migrationLockPollInterval is the retry cadence for AcquireMigrationLock.
// A blocking flock(2) cannot be cancelled by ctx, so the blocking
// contract is implemented as a non-blocking try-loop that honors ctx
// between attempts.
const migrationLockPollInterval = 25 * time.Millisecond

// advisoryLockerImpl is the SQLite AdvisoryLocker. The scheduler-tick and
// migration locks are exclusive file locks (flock(2) on unix,
// LockFileEx on Windows — see advisory_flock_{unix,windows}.go) on lock
// files derived
// from the database path (<db>.tick.lock / <db>.migrate.lock), so the
// exclusion holds across OS processes sharing the database file on one
// host — not merely across goroutines in one process. The lock contends
// per open file description; every acquisition opens its own fd, so two
// locker instances (or two acquisitions through one instance) exclude
// each other correctly even inside a single process.
//
// The lock files are created on first use and never deleted: removing a
// lock file while a holder's fd still references it would let a later
// opener lock a different inode and break the exclusion.
//
// Xact-locks are no-ops because the surrounding `BEGIN IMMEDIATE`
// writer-slot hold subsumes per-name advisory locking (strictly
// stronger) — and SQLite's own database-file locking makes that hold
// cross-process too.
type advisoryLockerImpl struct {
	tickLockPath      string
	migrationLockPath string
}

func newAdvisoryLocker(dbPath string) *advisoryLockerImpl {
	return &advisoryLockerImpl{
		tickLockPath:      dbPath + ".tick.lock",
		migrationLockPath: dbPath + ".migrate.lock",
	}
}

// TrySchedulerTick try-acquires the cross-process scheduler-tick
// exclusion via a non-blocking exclusive flock. Inv 7: at most one
// process sharing the database file runs the sweep pass at a time.
func (c *advisoryLockerImpl) TrySchedulerTick(_ context.Context) (bool, func(), error) {
	f, held, err := flockTry(c.tickLockPath)
	if err != nil || !held {
		return false, nil, err
	}
	release := func() {
		_ = flockRelease(f)
	}
	return true, release, nil
}

// AcquireMigrationLock blocks until the cross-process migration
// exclusion is held (Inv 8), polling a non-blocking flock so ctx
// cancellation is honored while waiting. The release fn is safe to call
// after the parent ctx is cancelled — it is a plain unlock + close with
// no context involved.
func (c *advisoryLockerImpl) AcquireMigrationLock(ctx context.Context) (func() error, error) {
	for {
		f, held, err := flockTry(c.migrationLockPath)
		if err != nil {
			return nil, err
		}
		if held {
			release := func() error {
				if err := flockRelease(f); err != nil {
					return fmt.Errorf("sqlite advisory lock: release %s: %w", c.migrationLockPath, err)
				}
				return nil
			}
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(migrationLockPollInterval):
		}
	}
}

// TakeNamedLockInTx is a no-op under SQLite: the surrounding BEGIN
// IMMEDIATE writer-slot hold subsumes per-name advisory locking, and
// SQLite's database-file locking makes that hold cross-process.
func (c *advisoryLockerImpl) TakeNamedLockInTx(_ context.Context, _ persistence.Tx, _ string) error {
	return nil
}

// TakeClaimScopeLockInTx is a no-op under SQLite. Same rationale as
// TakeNamedLockInTx.
func (c *advisoryLockerImpl) TakeClaimScopeLockInTx(_ context.Context, _ persistence.Tx, _ string, _ []byte) error {
	return nil
}

var _ persistence.AdvisoryLocker = (*advisoryLockerImpl)(nil)
