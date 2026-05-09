// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/modeling/shared"
)

// Driver is the umbrella over the rimsky persistence layer. One Driver
// is constructed per process via Open(); the three runtime processes hold
// it for their lifetime and Close() it on shutdown.
//
// Implementations live under postgres/ and sqlite/. No code outside this
// package tree may depend on driver-specific libraries (pgx, modernc).
type Driver interface {
	Queue() Queue
	Store() Store
	AdvisoryLocker() AdvisoryLocker
	// Migrate runs all embedded SQL migrations under the coordinator's
	// migration lock. log receives one Info per applied migration plus a
	// final summary. Pass shared.SilentLogger{} to suppress.
	Migrate(ctx context.Context, log shared.Logger) error
	// Ping issues a trivial round-trip to the underlying database to
	// surface connectivity problems. Returns nil when the driver can
	// successfully execute a query. Used by the observability
	// /v1/observability/system/health endpoint.
	Ping(ctx context.Context) error

	// SetBlobBackend installs the active BlobBackend + spill threshold +
	// orphan-retention window on the underlying Store. The attribute
	// write/read paths consult this configuration to decide whether to
	// spill bytes to the configured BlobBackend (vs. inline storage).
	// Set with bb=nil and threshold=0 to disable spill. See plan §D0/D6/D7.
	SetBlobBackend(bb BlobBackend, threshold int, retention time.Duration)

	Close() error
}

// AdvisoryLocker carries the cross-process synchronization primitives the
// scheduler, migration runner, and supervisor's acquisition tx depend on.
//
// Postgres impl: pg_(try_)advisory_lock and pg_advisory_xact_lock.
// SQLite impl: sync.Mutex for the cross-process methods and no-ops for the
// xact-lock methods (the surrounding BEGIN IMMEDIATE writer hold subsumes
// them — strictly stronger than per-name advisory locking).
//
// Per spec §4 and §3.10. Load-bearing for blessed invariants 3, 4b, 7, 8, 10.
type AdvisoryLocker interface {
	// TrySchedulerTick: returns held=true plus a release fn if the
	// scheduler-tick exclusion was acquired; held=false and a nil release
	// fn if another replica already holds it. The scheduler skips the
	// tick when held=false. Inv 7.
	TrySchedulerTick(ctx context.Context) (held bool, release func(), err error)

	// AcquireMigrationLock blocks until the migration exclusion is held.
	// The release fn must be safe to call even after the parent ctx is
	// cancelled (Postgres impl uses context.Background() internally for
	// the unlock; SQLite impl is a plain mu.Unlock). Inv 8.
	AcquireMigrationLock(ctx context.Context) (release func() error, err error)

	// TakeNamedLockInTx acquires the per-named-lock advisory exclusion
	// inside the supplied tx. Released automatically at tx end. Callers
	// MUST take locks in the deterministic sort order from v3 spec §4.10
	// invariant 3 (named-lock names sorted lexically before scope locks
	// sorted by store-name then by scope-data bytes). Inv 3, 10.
	//
	// Postgres: pg_advisory_xact_lock(hashtext('rimsky_lock:'+name)).
	// SQLite: no-op (writer slot already held).
	TakeNamedLockInTx(ctx context.Context, tx Tx, name string) error

	// TakeScopeLockInTx: same pattern, scoped to (storeName, scopeData).
	// Inv 3, 4b, 10.
	//
	// Postgres: pg_advisory_xact_lock(hashtext('rimsky_scope:'+store+':'+hex(scope))).
	// SQLite: no-op.
	TakeScopeLockInTx(ctx context.Context, tx Tx, storeName string, scopeData []byte) error
}
