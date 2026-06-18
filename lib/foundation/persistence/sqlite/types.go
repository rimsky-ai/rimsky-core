// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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
		return time.Time{}, nil
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

func instanceIDArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

func nodeIDArg(p *shared.UUID) any {
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
		return shared.UUID{}, nil
	}
	return uuid.Parse(s)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}

func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "FOREIGN KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: FOREIGN KEY")
}
