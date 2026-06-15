// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/testaccess.go
// @diverged: true
// @reason: parallel driver — SQLite test access helper vs Postgres test access helper

package sqlite

import (
	"database/sql"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// DBFromDatabase returns the underlying *sql.DB for a sqlite-backed
// persistence.Database. Used by integration tests to inspect PRAGMA state
// on the database's actual connection (instead of opening a parallel sql.DB
// and risking a stale view of session-local PRAGMAs). Panics if d is not
// a sqlite database. Test-only.
func DBFromDatabase(d persistence.Database) *sql.DB {
	sd, ok := d.(*database)
	if !ok {
		panic("DBFromDatabase: not a sqlite database")
	}
	return sd.db
}
