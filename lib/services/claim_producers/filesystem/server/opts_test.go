// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOptsFromEnv_EnableLifecycleWiredThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	root := filepath.Join(dir, "store")
	writeFile(t, cfgPath, "root: "+root+"\nenable_lifecycle: true\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if !opts.EnableLifecycle {
		t.Fatal("Opts.EnableLifecycle: got false, want true from enable_lifecycle: true")
	}
	if !opts.ServerConfig().EnableLifecycle {
		t.Fatal("ServerConfig().EnableLifecycle: got false, want true")
	}
}

func TestLoadOptsFromEnv_EnableLifecycleDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	root := filepath.Join(dir, "store")
	writeFile(t, cfgPath, "root: "+root+"\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.EnableLifecycle {
		t.Fatal("Opts.EnableLifecycle: got true, want false when enable_lifecycle omitted")
	}
	if opts.ServerConfig().EnableLifecycle {
		t.Fatal("ServerConfig().EnableLifecycle: got true, want false when enable_lifecycle omitted")
	}
}

func TestLoadOptsFromEnv_LedgerMaxRecordsWiredThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	root := filepath.Join(dir, "store")
	writeFile(t, cfgPath, "root: "+root+"\nledger_max_records: 2048\n")

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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
