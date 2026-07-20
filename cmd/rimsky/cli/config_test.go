// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfig_LoadMissing(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.Contexts == nil {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.CurrentContext != "" {
		t.Errorf("current_context: %q", cfg.CurrentContext)
	}
}

func TestConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	want := &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://localhost:8080"},
			"staging": {Endpoint: "https://rimsky.staging.example.com"},
		},
	}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip diff:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestConfig_SavedFileNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yml")
	cfg := &Config{Contexts: map[string]Context{"dev": {Endpoint: "http://localhost:8080", APIKey: "secret-token"}}}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600 (must not be group/world readable, it carries a plaintext api key)", perm)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir mode = %o, want 0700", perm)
	}
}

func TestConfig_ResavingTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("current_context: dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveConfig(path, &Config{Contexts: map[string]Context{}}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode after re-save = %o, want 0600", perm)
	}
}

func TestConfig_RejectsInvalidName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := &Config{Contexts: map[string]Context{"_bad": {Endpoint: "x"}}}
	if err := SaveConfig(path, cfg); err == nil {
		t.Fatal("want error")
	}
}

func TestValidContextName(t *testing.T) {
	cases := []struct {
		s  string
		ok bool
	}{
		{"dev", true},
		{"prod-1", true},
		{"a.b_c", true},
		{"", false},
		{"1bad", false},
		{"-bad", false},
	}
	for _, c := range cases {
		if got := ValidContextName(c.s); got != c.ok {
			t.Errorf("%q: got %v want %v", c.s, got, c.ok)
		}
	}
}
