// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli

import (
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
