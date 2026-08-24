// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: persistence-database
type Database interface {
	Queue() Queue
	Tables() Tables
	AdvisoryLocker() AdvisoryLocker
	Migrate(ctx context.Context, log shared.Logger) error
	Ping(ctx context.Context) error

	Close() error
}

// @concept: advisory-lock
// @decision: advisory-locks
type AdvisoryLocker interface {
	TrySchedulerTick(ctx context.Context) (held bool, release func(), err error)

	AcquireMigrationLock(ctx context.Context) (release func() error, err error)

	TakeNamedLock(ctx context.Context, name string, tx Tx) error

	TakeClaimScopeLock(ctx context.Context, claimProducerName string, claimScopeData []byte, tx Tx) error

	TakeLifecycleScopeLock(ctx context.Context, scopeKind LifecycleScopeKind, scopeID string, tx Tx) error
}
