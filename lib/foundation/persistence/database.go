// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: persistence-database
type Database interface {
	Queue() Queue
	Tables() Tables
	AdvisoryLocker() AdvisoryLocker
	Migrate(ctx context.Context, log shared.Logger) error
	Ping(ctx context.Context) error

	SetBlobBackend(bb BlobBackend, threshold int, retention time.Duration)

	Close() error
}

// @concept: advisory-lock
type AdvisoryLocker interface {
	TrySchedulerTick(ctx context.Context) (held bool, release func(), err error)

	AcquireMigrationLock(ctx context.Context) (release func() error, err error)

	TakeNamedLockInTx(ctx context.Context, tx Tx, name string) error

	TakeClaimScopeLockInTx(ctx context.Context, tx Tx, claimProducerName string, claimScopeData []byte) error
}
