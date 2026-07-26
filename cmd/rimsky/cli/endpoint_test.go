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
	t.Setenv("RIMSKY_CONTROL_API_URL", "http://env")
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

func TestResolveEndpoint_RimskyContextEnvNotFound(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "ghost")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {Endpoint: "http://dev"}},
	})
	_, err := ResolveEndpoint("", "", cfg, "")
	if err == nil {
		t.Fatal("want error when RIMSKY_CONTEXT names an undefined context")
	}
	if !strings.Contains(err.Error(), `RIMSKY_CONTEXT="ghost" not found`) {
		t.Errorf("error %v does not name the missing RIMSKY_CONTEXT value", err)
	}
}

func TestResolveEndpoint_RimskyContextEnvHasNoEndpoint(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "dev")
	cfg := writeConfig(t, &Config{
		Contexts: map[string]Context{"dev": {}},
	})
	_, err := ResolveEndpoint("", "", cfg, "")
	if err == nil {
		t.Fatal("want error when the RIMSKY_CONTEXT-named context has no endpoint")
	}
	if !strings.Contains(err.Error(), `context "dev" has no endpoint set`) {
		t.Errorf("error %v does not mention the missing endpoint", err)
	}
}

func TestResolveEndpoint_CurrentContextNotDefined(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "ghost",
		Contexts:       map[string]Context{"dev": {Endpoint: "http://dev"}},
	})
	_, err := ResolveEndpoint("", "", cfg, "")
	if err == nil {
		t.Fatal("want error when current_context names an undefined context")
	}
	if !strings.Contains(err.Error(), `current_context "ghost" not defined`) {
		t.Errorf("error %v does not name the undefined current_context", err)
	}
}

func TestResolveEndpoint_CurrentContextHasNoEndpoint(t *testing.T) {
	t.Setenv("RIMSKY_CONTEXT", "")
	cfg := writeConfig(t, &Config{
		CurrentContext: "dev",
		Contexts:       map[string]Context{"dev": {}},
	})
	_, err := ResolveEndpoint("", "", cfg, "")
	if err == nil {
		t.Fatal("want error when current_context's endpoint is empty")
	}
	if !strings.Contains(err.Error(), `context "dev" has no endpoint set`) {
		t.Errorf("error %v does not mention the missing endpoint", err)
	}
}
