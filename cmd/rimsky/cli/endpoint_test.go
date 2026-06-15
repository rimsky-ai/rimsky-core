// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, cfg *Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveEndpoint_FlagWins(t *testing.T) {
	t.Setenv("RIMSKY_CONTROL_API", "http://env")
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {Endpoint: "http://cfg"}},
	})
	got, err := ResolveEndpoint("http://flag", "http://env", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://flag" {
		t.Errorf("got %q", got)
	}
}

// TestResolveEndpoint_FlagBeatsManifestForNonCompose — flag beats a
// manifestContext when flag is set. The non-compose entry point is
// meant for verbs that don't load manifests, so this code path
// exercises the fallback when callers pass a manifestContext
// defensively.
func TestResolveEndpoint_FlagBeatsManifestForNonCompose(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	got, err := ResolveEndpoint("http://flag", "http://env", cfg, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://flag" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpointForCompose_ManifestPinConflictsWithFlag(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	// @deliberate: --endpoint contradicts the manifest's pinned context → error.
	_, err := ResolveEndpointForCompose("http://flag", "http://env", cfg, "staging")
	if err == nil {
		t.Fatal("want error when --endpoint conflicts with manifest pin")
	}
	if !strings.Contains(err.Error(), "contradicts manifest's pinned context") {
		t.Errorf("error %v does not mention manifest pin conflict", err)
	}
}

func TestResolveEndpointForCompose_ManifestPinMatchesFlag(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	// @deliberate: --endpoint matches the manifest pin's resolved endpoint → accepted.
	got, err := ResolveEndpointForCompose("http://staging", "http://env", cfg, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://staging" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpointForCompose_ManifestPin(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	got, err := ResolveEndpointForCompose("", "http://env", cfg, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://staging" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpoint_ManifestPin(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	got, err := ResolveEndpoint("", "", cfg, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://staging" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpoint_ManifestPinUnknown(t *testing.T) {
	cfg := writeConfig(t, &Config{
		Contexts: map[string]Context{"dev": {Endpoint: "http://dev"}},
	})
	if _, err := ResolveEndpoint("", "", cfg, "missing"); err == nil {
		t.Fatal("want error")
	}
}

func TestResolveEndpoint_EnvBeforeConfig(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {Endpoint: "http://cfg"}},
	})
	got, err := ResolveEndpoint("", "http://env", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://env" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpoint_RimskyContextEnv(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "staging")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts: map[string]Context{
			"dev":     {Endpoint: "http://dev"},
			"staging": {Endpoint: "http://staging"},
		},
	})
	got, err := ResolveEndpoint("", "", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://staging" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpoint_CurrentContext(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {Endpoint: "http://dev"}},
	})
	got, err := ResolveEndpoint("", "", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://dev" {
		t.Errorf("got %q", got)
	}
}

func TestResolveEndpoint_NothingConfigured(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{Contexts: map[string]Context{}})
	if _, err := ResolveEndpoint("", "", cfg, ""); err == nil {
		t.Fatal("want error")
	}
}
