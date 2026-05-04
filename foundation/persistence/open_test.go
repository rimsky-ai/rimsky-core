package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestOpenValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"empty", Config{}, "driver is required"},
		{"unknown", Config{Driver: "mysql"}, "unknown driver"},
		{"postgres-no-block", Config{Driver: "postgres"}, "requires postgres: block"},
		{"postgres-no-dsn", Config{Driver: "postgres", Postgres: &PostgresConfig{}}, "dsn is required"},
		{"postgres-with-sqlite", Config{Driver: "postgres", Postgres: &PostgresConfig{DSN: "x"}, SQLite: &SQLiteConfig{}}, "mutually exclusive"},
		{"sqlite-no-path", Config{Driver: "sqlite", SQLite: &SQLiteConfig{}}, "path is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(context.Background(), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}
