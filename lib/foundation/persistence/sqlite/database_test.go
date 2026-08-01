// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import "testing"

func TestSQLiteUnifiedStackMaxOpenConnsAvoidsBeginStarvation(t *testing.T) {
	if sqliteUnifiedStackMaxOpenConns < 2 {
		t.Fatalf("sqliteUnifiedStackMaxOpenConns=%d: a pool of 1 starves goroutines "+
			"waiting on Begin while another goroutine holds a long-running tx; "+
			"the unified in-process stack (rimsky compose run, all-in-one entrypoint) "+
			"requires at least 2 slots so the supervisor's settle tx does not block "+
			"the control-api's request handlers. The SQLite writer slot at the FILE "+
			"level remains 1 (writers serialize via busy_timeout=5000ms), so widening "+
			"the SQL pool does not break writer serialization.", sqliteUnifiedStackMaxOpenConns)
	}
}
