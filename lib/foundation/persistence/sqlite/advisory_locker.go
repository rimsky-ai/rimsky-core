// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: advisory-lock

package sqlite

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const migrationLockPollInterval = 25 * time.Millisecond

type advisoryLockerImpl struct {
	tickLockPath       string
	migrationLockPath  string
	contendedPollCount atomic.Int64
}

// @decision: sqlite-multiproc-safety
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

func (c *advisoryLockerImpl) contendedPolls() int64 { return c.contendedPollCount.Load() }

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
		c.contendedPollCount.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(migrationLockPollInterval):
		}
	}
}

func (c *advisoryLockerImpl) TakeNamedLock(_ context.Context, _ string, _ persistence.Tx) error {
	return nil
}

func (c *advisoryLockerImpl) TakeClaimScopeLock(_ context.Context, _ string, _ []byte, _ persistence.Tx) error {
	return nil
}

func (c *advisoryLockerImpl) TakeLifecycleScopeLock(_ context.Context, _ persistence.LifecycleIdempotencyScopeKind, _ string, _ persistence.Tx) error {
	return nil
}

var _ persistence.AdvisoryLocker = (*advisoryLockerImpl)(nil)
