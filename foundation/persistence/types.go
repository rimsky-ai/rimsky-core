// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package persistence is the runtime-state-storage protocol for rimsky.
// The Database interface (database.go) aggregates Queue, Tables, and AdvisoryLocker
// sub-interfaces. Two impls live under postgres/ and sqlite/. (The adapter
// selector string Config.Driver = "postgres"/"sqlite" is distinct from the
// runtime Database interface.)
//
// Spec: docs/history/2026-05-02-persistence-pluggable-and-unified-image-design.md
package persistence

import (
	"errors"
	"time"
)

// ErrNotFound is the database-agnostic sentinel for "row does not exist".
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
// *Table methods. Driver-implemented; opaque to callers. Concrete carriers
// embed TxMarker so they satisfy Tx without being forgeable from outside
// the persistence package tree.
type Tx interface{ isTx() }

// TxMarker is the zero-cost embed driver impls use to satisfy Tx.
type TxMarker struct{}

func (TxMarker) isTx() {}
