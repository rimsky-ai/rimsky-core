// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"os"
	"path/filepath"
	"testing"
)

func writeOptsConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func TestLoadOptsFromEnv_LedgerMaxRecordsWiredThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\nledger_max_records: 2048\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.LedgerMaxRecords != 2048 {
		t.Fatalf("Opts.LedgerMaxRecords = %d, want 2048", opts.LedgerMaxRecords)
	}
	if opts.ServerConfig().LedgerMaxRecords != 2048 {
		t.Fatalf("ServerConfig().LedgerMaxRecords = %d, want 2048", opts.ServerConfig().LedgerMaxRecords)
	}
}

func TestLoadOptsFromEnv_LedgerMaxRecordsDefaultsZero(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.LedgerMaxRecords != 0 {
		t.Fatalf("Opts.LedgerMaxRecords = %d, want 0 (unset, store applies its own default)", opts.LedgerMaxRecords)
	}
}
