// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"database/sql"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func DBFromDatabaseForTest(d persistence.Database) (*sql.DB, bool) {
	sd, ok := d.(*database)
	if !ok {
		return nil, false
	}
	return sd.db, true
}
