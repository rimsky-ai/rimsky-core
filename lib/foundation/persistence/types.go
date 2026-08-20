// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("persistence: not found")

var ErrInternalInvariant = errors.New("persistence: internal invariant violated")

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
