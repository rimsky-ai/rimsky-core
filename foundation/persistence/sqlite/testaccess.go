// testaccess.go provides test-only escape hatches for code that needs
// raw *sql.DB access against a sqlite-backed persistence.Driver.
// Mirrors `core/persistence/postgres/testaccess.go`.
//
// Production code MUST go through the persistence.Driver interface.
// Adding a non-test caller of these helpers is a regression against
// blessed-invariant 9a (the persistence layer is the single
// runtime-state surface).
package sqlite

import (
	"database/sql"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// DBFromDriver returns the underlying *sql.DB for a sqlite-backed
// persistence.Driver. Used by integration tests to inspect PRAGMA state
// on the driver's actual connection (instead of opening a parallel sql.DB
// and risking a stale view of session-local PRAGMAs). Panics if d is not
// a sqlite driver. Test-only.
func DBFromDriver(d persistence.Driver) *sql.DB {
	sd, ok := d.(*driver)
	if !ok {
		panic("DBFromDriver: not a sqlite driver")
	}
	return sd.db
}
