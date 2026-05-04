package sqlite

import (
	"context"
	"database/sql"
	"sync"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// advisoryLockerImpl is the SQLite AdvisoryLocker. Cross-process locks reduce
// to in-process mutexes (single-process is the only supported topology
// per spec §1 and §6); xact-locks are no-ops because the surrounding
// `BEGIN IMMEDIATE` writer-slot hold subsumes per-name advisory locking
// (strictly stronger). Per spec §4.2.
type advisoryLockerImpl struct {
	schedulerTick sync.Mutex
	migration     sync.Mutex
}

func newAdvisoryLocker(_ *sql.DB) *advisoryLockerImpl { return &advisoryLockerImpl{} }

func (c *advisoryLockerImpl) TrySchedulerTick(_ context.Context) (bool, func(), error) {
	if !c.schedulerTick.TryLock() {
		return false, nil, nil
	}
	return true, c.schedulerTick.Unlock, nil
}

func (c *advisoryLockerImpl) AcquireMigrationLock(_ context.Context) (func() error, error) {
	c.migration.Lock()
	return func() error { c.migration.Unlock(); return nil }, nil
}

// TakeNamedLockInTx is a no-op under SQLite. Per spec §4.2 the surrounding
// BEGIN IMMEDIATE writer-slot hold subsumes per-name advisory locking.
func (c *advisoryLockerImpl) TakeNamedLockInTx(_ context.Context, _ persistence.Tx, _ string) error {
	return nil
}

// TakeScopeLockInTx is a no-op under SQLite. Same rationale as
// TakeNamedLockInTx.
func (c *advisoryLockerImpl) TakeScopeLockInTx(_ context.Context, _ persistence.Tx, _ string, _ []byte) error {
	return nil
}

var _ persistence.AdvisoryLocker = (*advisoryLockerImpl)(nil)
