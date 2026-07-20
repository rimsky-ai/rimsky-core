// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("persistence: not found")

type Config struct {
	Driver   string
	Postgres *PostgresConfig
	SQLite   *SQLiteConfig
}

type PostgresConfig struct {
	DSN             string
	MaxOpenConns    int
	MinConns        int
	ConnMaxLifetime time.Duration
}

type SQLiteConfig struct {
	Path string
}

type Tx interface{ isTx() }

type TxMarker struct{}

func (TxMarker) isTx() {}
