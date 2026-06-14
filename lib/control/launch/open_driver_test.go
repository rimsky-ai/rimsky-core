// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: one-driver-per-process — these tests exhibit the
// open-side half of the invariant: every call to OpenDriverFromEnv
// that reaches a runner does so by funneling through THIS helper, so
// a single process exposes one persistence.Driver across whichever
// Run* runners it starts. The structural sharing exhibit lives in
// unified_test.go (TestStartUnifiedStack_OneDriverAcrossRunners),
// which substitutes the three runner seams and asserts the SAME
// driver pointer reaches each runner. These open-side tests pin the
// upstream half (one driver opened per process) and the runner-end
// test pins the downstream half (the one driver is the one threaded
// into every runner).
package launch

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger(_ *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpenDriverFromEnv_OpensSqliteDriverAtConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	blobRoot := filepath.Join(dir, "blobs")
	if err := os.MkdirAll(blobRoot, 0o755); err != nil {
		t.Fatalf("mkdir blob root: %v", err)
	}
	cfgPath := filepath.Join(dir, "rimsky.yml")
	cfg := `persistence:
  driver: sqlite
  sqlite:
    path: ` + dbPath + `
  blob:
    backend: filesystem
    filesystem:
      root: ` + blobRoot + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CONFIG", cfgPath)

	driver, parsed, err := OpenDriverFromEnv(context.Background(), testLogger(t))
	if err != nil {
		t.Fatalf("OpenDriverFromEnv: %v", err)
	}
	defer func() {
		if err := driver.Close(); err != nil {
			t.Fatalf("driver.Close: %v", err)
		}
	}()
	if parsed == nil {
		t.Fatal("OpenDriverFromEnv returned nil config")
	}
	if got, want := parsed.Persistence.Driver, "sqlite"; got != want {
		t.Fatalf("parsed.Persistence.Driver = %q, want %q", got, want)
	}
	if got, want := parsed.Persistence.SQLite.Path, dbPath; got != want {
		t.Fatalf("parsed.Persistence.SQLite.Path = %q, want %q", got, want)
	}
	if driver.Tables() == nil {
		t.Fatal("driver.Tables() returned nil — driver not usable")
	}
}

func TestOpenDriverFromEnv_MissingConfigSurfacesError(t *testing.T) {
	t.Setenv("RIMSKY_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yml"))
	driver, parsed, err := OpenDriverFromEnv(context.Background(), testLogger(t))
	if err == nil {
		_ = driver.Close()
		t.Fatal("OpenDriverFromEnv: want error for missing config, got nil")
	}
	if driver != nil {
		t.Errorf("OpenDriverFromEnv returned non-nil driver on error")
	}
	if parsed != nil {
		t.Errorf("OpenDriverFromEnv returned non-nil config on error")
	}
}
