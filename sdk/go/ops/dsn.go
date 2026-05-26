// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"fmt"
	"os"
)

// DSNFromEnv reads a Postgres DSN from the named env var. Returns
// the empty string and nil error when unset — callers treat that
// as "feature disabled" (typical for optional state-DB persistence
// in publisher / executor binaries). When set but malformed in any
// way the env var detects, an error is returned naming the env var
// for operator clarity.
//
// Validation here is intentionally minimal: we only check for non-
// emptiness after trimming whitespace. Driver-level connect / ping
// validation is the caller's responsibility (sql.Open + db.PingContext
// is the canonical follow-up).
func DSNFromEnv(envVar string) (string, error) {
	dsn := os.Getenv(envVar)
	if dsn == "" {
		return "", nil
	}
	// Trim leading/trailing whitespace — operators sometimes paste
	// DSNs from shell heredocs and an accidental newline at the end
	// produces unhelpful pgx errors like "missing equals sign".
	dsn = trimSpace(dsn)
	if dsn == "" {
		return "", fmt.Errorf("ops: %s is whitespace-only", envVar)
	}
	return dsn, nil
}

// trimSpace is the minimal ASCII whitespace trim. Avoids pulling
// strings just for one call site.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
