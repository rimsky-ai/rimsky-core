// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"testing"
	"time"
)

func TestParseTimeEmptyStringErrors(t *testing.T) {
	if _, err := parseTime(""); err == nil {
		t.Fatalf("parseTime(\"\") = nil error, want an error (empty string in a NOT NULL timestamp column is corruption, not a valid zero time)")
	}
}

func TestParseTimeValidRoundTrip(t *testing.T) {
	fixture := time.Date(2026, 7, 20, 12, 30, 0, 0, time.UTC)
	want := formatTime(fixture)
	got, err := parseTime(want)
	if err != nil {
		t.Fatalf("parseTime(%q): %v", want, err)
	}
	if !got.Equal(fixture) {
		t.Fatalf("parseTime(%q) = %v, want %v", want, got, fixture)
	}
}

func TestParseUUIDEmptyStringErrors(t *testing.T) {
	if _, err := parseUUID(""); err == nil {
		t.Fatalf("parseUUID(\"\") = nil error, want an error (empty string in a NOT NULL uuid column is corruption, not a valid zero uuid)")
	}
}

func TestParseUUIDValidRoundTrip(t *testing.T) {
	const id = "0d4e6b8a-1c2d-4e3f-8a9b-0c1d2e3f4a5b"
	got, err := parseUUID(id)
	if err != nil {
		t.Fatalf("parseUUID(%q): %v", id, err)
	}
	if got.String() != id {
		t.Fatalf("parseUUID(%q) = %v, want %v", id, got, id)
	}
}
