// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// testaccess.go provides test-only escape hatches for code that needs
// raw *sql.DB access against a sqlite-backed persistence.Database.
// Mirrors `foundation/persistence/postgres/testaccess.go`.
//
// Production code MUST go through the persistence.Database interface.
// Adding a non-test caller of these helpers is a regression against
// blessed-invariant 9a (the persistence layer is the single
// runtime-state surface).
package sqlite

import (
	"database/sql"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
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
