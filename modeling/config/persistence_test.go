package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRimskyConfig_Persistence covers the four shapes from spec §8.2:
// postgres-only, sqlite-only, missing driver, and conflicting blocks.
func TestLoadRimskyConfig_Persistence(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		wantPg   bool
		wantSqlt bool
		wantErr  string
	}{
		{
			name: "postgres-only",
			yaml: `
persistence:
  driver: postgres
  postgres:
    dsn: postgres://x@y/z?sslmode=disable
`,
			wantPg: true,
		},
		{
			name: "sqlite-only",
			yaml: `
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
`,
			wantSqlt: true,
		},
		{
			name:    "missing-driver",
			yaml:    ``,
			wantErr: "driver is required",
		},
		{
			name: "both-blocks",
			yaml: `
persistence:
  driver: postgres
  postgres:
    dsn: postgres://x@y/z?sslmode=disable
  sqlite:
    path: /var/lib/rimsky/state.db
`,
			wantErr: "mutually exclusive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "rimsky.yml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			cfg, err := LoadRimskyConfigYAML(path)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if tc.wantPg {
				if cfg.Persistence.Postgres == nil {
					t.Fatalf("expected postgres block populated")
				}
				if cfg.Persistence.Driver != "postgres" {
					t.Fatalf("driver = %q, want postgres", cfg.Persistence.Driver)
				}
			}
			if tc.wantSqlt {
				if cfg.Persistence.SQLite == nil {
					t.Fatalf("expected sqlite block populated")
				}
				if cfg.Persistence.Driver != "sqlite" {
					t.Fatalf("driver = %q, want sqlite", cfg.Persistence.Driver)
				}
				if cfg.Persistence.SQLite.Path != "/var/lib/rimsky/state.db" {
					t.Fatalf("path = %q", cfg.Persistence.SQLite.Path)
				}
			}
		})
	}
}
