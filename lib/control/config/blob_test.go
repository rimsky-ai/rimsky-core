// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func writeRimskyYAML(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadRimskyConfig_Blob_DefaultsToInline(t *testing.T) {
	path := writeRimskyYAML(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
`)
	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Blob != persistence.DefaultBlobConfig() {
		t.Fatalf("Blob = %+v, want default %+v", cfg.Blob, persistence.DefaultBlobConfig())
	}
}

func TestLoadRimskyConfig_Blob_MemoryBackendRequiresUnifiedRole(t *testing.T) {
	path := writeRimskyYAML(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
  blob:
    backend: memory
`)
	t.Setenv(persistence.ProcessRoleEnv, "")
	_, err := LoadRimskyConfigYAML(path)
	if err == nil {
		t.Fatal("want error when memory backend requested outside unified role, got nil")
	}
	if !errors.Is(err, persistence.ErrInvalidBlobConfig) {
		t.Fatalf("want errors.Is(persistence.ErrInvalidBlobConfig), got %v", err)
	}
	if !strings.Contains(err.Error(), "persistence.blob") {
		t.Fatalf("error %q does not identify the persistence.blob loader boundary", err.Error())
	}

	t.Setenv(persistence.ProcessRoleEnv, "unified")
	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("load with unified role: %v", err)
	}
	if cfg.Blob.Backend != "memory" {
		t.Fatalf("Blob.Backend = %q, want memory", cfg.Blob.Backend)
	}
}

func TestLoadRimskyConfig_Topology(t *testing.T) {
	path := writeRimskyYAML(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
`)
	t.Run("split when unset", func(t *testing.T) {
		t.Setenv(persistence.ProcessRoleEnv, "")
		cfg, err := LoadRimskyConfigYAML(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Topology != persistence.TopologySplit {
			t.Fatalf("Topology = %q, want %q", cfg.Topology, persistence.TopologySplit)
		}
	})
	t.Run("unified when set", func(t *testing.T) {
		t.Setenv(persistence.ProcessRoleEnv, "unified")
		cfg, err := LoadRimskyConfigYAML(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Topology != persistence.TopologyUnified {
			t.Fatalf("Topology = %q, want %q", cfg.Topology, persistence.TopologyUnified)
		}
	})
}

func TestLoadRimskyConfig_SQLiteReplicaWarning(t *testing.T) {
	sqlitePath := writeRimskyYAML(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
`)
	postgresPath := writeRimskyYAML(t, `
persistence:
  driver: postgres
  postgres:
    dsn: postgres://x@y/z?sslmode=disable
`)

	t.Run("sqlite outside unified topology warns, does not error", func(t *testing.T) {
		t.Setenv(persistence.ProcessRoleEnv, "")
		cfg, err := LoadRimskyConfigYAML(sqlitePath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.Warnings) != 1 {
			t.Fatalf("Warnings = %v, want exactly one warning", cfg.Warnings)
		}
		if !strings.Contains(cfg.Warnings[0], "sqlite") {
			t.Errorf("warning %q does not name the sqlite driver", cfg.Warnings[0])
		}
	})

	t.Run("sqlite in unified topology does not warn", func(t *testing.T) {
		t.Setenv(persistence.ProcessRoleEnv, "unified")
		cfg, err := LoadRimskyConfigYAML(sqlitePath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.Warnings) != 0 {
			t.Fatalf("Warnings = %v, want none", cfg.Warnings)
		}
	})

	t.Run("postgres outside unified topology does not warn", func(t *testing.T) {
		t.Setenv(persistence.ProcessRoleEnv, "")
		cfg, err := LoadRimskyConfigYAML(postgresPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.Warnings) != 0 {
			t.Fatalf("Warnings = %v, want none", cfg.Warnings)
		}
	})
}

func TestOpenBlobBackend_Inline(t *testing.T) {
	db := openMigratedSQLite(t)
	bb, err := OpenBlobBackend(persistence.DefaultBlobConfig(), db, persistence.TopologySplit)
	if err != nil {
		t.Fatalf("OpenBlobBackend: %v", err)
	}
	if bb.Name() != "inline" {
		t.Fatalf("backend = %q, want inline", bb.Name())
	}
}

func TestOpenBlobBackend_MemoryGatedByTopology(t *testing.T) {
	db := openMigratedSQLite(t)
	_, err := OpenBlobBackend(persistence.BlobConfig{Backend: "memory"}, db, persistence.TopologySplit)
	if err == nil {
		t.Fatal("want error opening memory backend outside the unified topology, got nil")
	}
	if !errors.Is(err, persistence.ErrInvalidBlobConfig) {
		t.Fatalf("want errors.Is(persistence.ErrInvalidBlobConfig), got %v", err)
	}

	bb, err := OpenBlobBackend(persistence.BlobConfig{Backend: "memory"}, db, persistence.TopologyUnified)
	if err != nil {
		t.Fatalf("OpenBlobBackend with unified topology: %v", err)
	}
	if bb.Name() != "memory" {
		t.Fatalf("backend = %q, want memory", bb.Name())
	}
}

func TestOpenBlobBackend_Filesystem(t *testing.T) {
	db := openMigratedSQLite(t)
	bb, err := OpenBlobBackend(persistence.BlobConfig{Backend: "filesystem", Filesystem: persistence.FilesystemBlobConfig{Root: t.TempDir()}}, db, persistence.TopologySplit)
	if err != nil {
		t.Fatalf("OpenBlobBackend: %v", err)
	}
	if bb.Name() != "filesystem" {
		t.Fatalf("backend = %q, want filesystem", bb.Name())
	}
}

func TestOpenBlobBackend_PgLargeObjectRequiresPostgresDriver(t *testing.T) {
	db := openMigratedSQLite(t)
	_, err := OpenBlobBackend(persistence.BlobConfig{Backend: "pg-largeobject"}, db, persistence.TopologySplit)
	if err == nil {
		t.Fatal("want error opening pg-largeobject backend against a sqlite driver, got nil")
	}
	if !strings.Contains(err.Error(), "requires the postgres driver") {
		t.Fatalf("error %q does not name the postgres-driver requirement", err.Error())
	}
}
