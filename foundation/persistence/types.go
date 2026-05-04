// Package persistence is the runtime-state-storage protocol for rimsky.
// The Driver interface (driver.go) aggregates Queue, Store, and AdvisoryLocker
// sub-interfaces. Two impls live under postgres/ and sqlite/.
//
// Spec: docs/history/2026-05-02-persistence-pluggable-and-unified-image-design.md
package persistence

import (
	"errors"
	"time"
)

// ErrNotFound is the driver-agnostic sentinel for "row does not exist".
// Methods that distinguish absence from other failure modes (e.g.
// MergeDelta's "row required") wrap this. Callers can use
// `errors.Is(err, persistence.ErrNotFound)` regardless of driver — pgx's
// pgx.ErrNoRows and database/sql's sql.ErrNoRows are not exposed
// across the persistence interface boundary.
var ErrNotFound = errors.New("persistence: not found")

// Config is the operator-supplied driver selection + parameters. Loaded
// from the `persistence:` block in rimsky.yml. Validation rules per spec §8.2:
//   - Driver in {"postgres","sqlite"}.
//   - Exactly one of Postgres / SQLite is non-nil; mutual exclusion is enforced
//     at the loader and re-checked here in Open.
type Config struct {
	Driver   string
	Postgres *PostgresConfig
	SQLite   *SQLiteConfig
}

type PostgresConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type SQLiteConfig struct {
	Path string // absolute; relative paths rejected at the loader
}

// Tx is the transaction handle threaded through Queue and per-feature
// *Store methods. Driver-implemented; opaque to callers. Concrete carriers
// embed TxMarker so they satisfy Tx without being forgeable from outside
// the persistence package tree.
type Tx interface{ isTx() }

// TxMarker is the zero-cost embed driver impls use to satisfy Tx.
type TxMarker struct{}

func (TxMarker) isTx() {}
