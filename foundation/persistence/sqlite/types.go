// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// types.go contains SQLite-specific type-marshalling helpers shared across
// the per-feature impls. Per spec §6.3 the SQLite dialect drift requires
// app-side translation:
//
//   - JSONB columns -> TEXT (we marshal at write, unmarshal at read)
//   - UUID columns -> TEXT (we stringify at write, uuid.Parse at read)
//   - UUID[]/TEXT[] columns -> TEXT (JSON-encoded array)
//   - TIMESTAMPTZ columns -> TEXT (RFC3339Nano)
//   - NOW() -> caller passes time.Now().UTC().Format(...)
//
// These helpers keep the per-feature SQL terse without duplicating the
// translation rules across 12 files.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// nowUTC returns time.Now().UTC() formatted RFC3339Nano. SQLite has no
// NOW() that gives sub-second precision; we pass the formatted string as
// a parameter so timestamp comparisons (`< ?`) round-trip cleanly.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// formatTime formats t as RFC3339Nano. Empty-zero times are accepted; the
// caller decides whether to pass nil or formatTime via nullableTime.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime parses an RFC3339Nano-formatted string. SQLite's text time
// columns occasionally come back with a slightly different sub-second
// precision; time.Parse(time.RFC3339Nano, ...) handles all valid RFC3339
// variants we produce.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	// Try RFC3339Nano first (the format we write).
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	// Fall back to SQLite's `datetime('now')` default ("YYYY-MM-DD HH:MM:SS").
	const sqliteDefault = "2006-01-02 15:04:05"
	if t, err := time.Parse(sqliteDefault, s); err == nil {
		return t.UTC(), nil
	}
	// Final attempt: RFC3339 (no nanos).
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parseTime: %q: %w", s, err)
	}
	return t, nil
}

// nullableJSONB returns nil for empty []byte; otherwise the bytes (which
// are stored verbatim in the TEXT column). Mirrors postgres' nullableJSONB.
func nullableJSONB(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// nullableString returns nil for empty string; otherwise the string.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableInt returns nil for zero; otherwise the int.
func nullableInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

// nullableUUID returns nil for nil pointer; otherwise the UUID's string form.
func nullableUUID(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

// instanceIDArg formats a *shared.UUID into a query arg (string or nil).
// Naming mirrors postgres/events.go for parity.
func instanceIDArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

// nodeIDArg formats a *shared.UUID into a query arg (string or nil).
func nodeIDArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

// marshalUUIDArray serialises a []shared.UUID as a JSON array of strings.
// Empty / nil input returns `[]`.
func marshalUUIDArray(ids []shared.UUID) string {
	if len(ids) == 0 {
		return "[]"
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// unmarshalUUIDArray parses a JSON array of strings back to []shared.UUID.
func unmarshalUUIDArray(s string) ([]shared.UUID, error) {
	if s == "" || s == "[]" {
		return []shared.UUID{}, nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("unmarshalUUIDArray: %w", err)
	}
	out := make([]shared.UUID, len(raw))
	for i, r := range raw {
		u, err := uuid.Parse(r)
		if err != nil {
			return nil, fmt.Errorf("unmarshalUUIDArray: bad uuid %q: %w", r, err)
		}
		out[i] = u
	}
	return out, nil
}

// marshalStringArray serialises a []string as a JSON array.
func marshalStringArray(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// unmarshalStringArray parses a JSON-encoded string array back.
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

// scanUUID converts a sql.NullString into a *shared.UUID. Returns nil for
// invalid (null) input.
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

// parseUUID parses a non-empty UUID string. Empty string returns the zero
// shared.UUID.
func parseUUID(s string) (shared.UUID, error) {
	if s == "" {
		return shared.UUID{}, nil
	}
	return uuid.Parse(s)
}

// isUniqueViolation returns true when the SQLite error message indicates a
// UNIQUE / PRIMARY KEY constraint violation.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}

// isFKViolation returns true when the SQLite error indicates a foreign
// key constraint violation.
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "FOREIGN KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: FOREIGN KEY")
}
