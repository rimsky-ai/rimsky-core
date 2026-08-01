// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
