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

const migrationLockPollInterval = 25 * time.Millisecond

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

func (c *advisoryLockerImpl) TakeNamedLockInTx(_ context.Context, _ persistence.Tx, _ string) error {
	return nil
}

func (c *advisoryLockerImpl) TakeClaimScopeLockInTx(_ context.Context, _ persistence.Tx, _ string, _ []byte) error {
	return nil
}

var _ persistence.AdvisoryLocker = (*advisoryLockerImpl)(nil)
