// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	sqlite3 "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	sqliteConstraintUnique     = 2067
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintForeignKey = 787
	sqliteConstraintTrigger    = 1811
)

const timeLayoutFixedNanos = "2006-01-02T15:04:05.000000000Z07:00"

func nowUTC() string {
	return time.Now().UTC().Format(timeLayoutFixedNanos)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayoutFixedNanos)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("parseTime: empty timestamp string")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	const sqliteDefault = "2006-01-02 15:04:05"
	t, err := time.Parse(sqliteDefault, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parseTime: %q: %w", s, err)
	}
	return t.UTC(), nil
}

func parseNullableTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullableJSONB(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func nullableUUID(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

func marshalStringArray(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func unmarshalStringArray(s string) ([]string, error) {
	if s == "" || s == "[]" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("unmarshalStringArray: %w", err)
	}
	if out == nil {
		return []string{}, nil
	}
	return out, nil
}

func scanNullableUUID(ns sql.NullString) (*shared.UUID, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	u, err := uuid.Parse(ns.String)
	if err != nil {
		return nil, fmt.Errorf("scanNullableUUID: %w", err)
	}
	return &u, nil
}

func parseUUID(s string) (shared.UUID, error) {
	if s == "" {
		return shared.UUID{}, errors.New("parseUUID: empty uuid string")
	}
	return uuid.Parse(s)
}

func sqliteErrorCode(err error) (int, bool) {
	var sErr *sqlite3.Error
	if errors.As(err, &sErr) {
		return sErr.Code(), true
	}
	return 0, false
}

func isUniqueViolation(err error) bool {
	code, ok := sqliteErrorCode(err)
	if !ok {
		return false
	}
	return code == sqliteConstraintUnique
}

func isFKViolation(err error) bool {
	code, ok := sqliteErrorCode(err)
	if !ok {
		return false
	}
	return code == sqliteConstraintForeignKey || code == sqliteConstraintTrigger
}
