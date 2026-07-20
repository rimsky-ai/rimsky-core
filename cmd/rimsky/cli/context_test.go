// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func tempCfg(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.yml")
}

func TestRunCtxAdd_NewFile(t *testing.T) {
	cfg := tempCfg(t)
	if got := RunCtxAdd([]string{"--endpoint", "http://x", "dev"}, cfg); got != 0 {
		t.Errorf("exit %d", got)
	}
	loaded, _ := LoadConfig(cfg)
	if loaded.Contexts["dev"].Endpoint != "http://x" {
		t.Errorf("got %+v", loaded.Contexts)
	}
	if loaded.CurrentContext != "dev" {
		t.Errorf("current_context: %q", loaded.CurrentContext)
	}
}

func TestRunCtxAdd_Duplicate(t *testing.T) {
	cfg := tempCfg(t)
	RunCtxAdd([]string{"--endpoint", "http://x", "dev"}, cfg)
	if got := RunCtxAdd([]string{"--endpoint", "http://y", "dev"}, cfg); got != 2 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxUse_Unknown(t *testing.T) {
	cfg := tempCfg(t)
	if got := RunCtxUse([]string{"nope"}, cfg); got != 2 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxUse_Switch(t *testing.T) {
	cfg := tempCfg(t)
	RunCtxAdd([]string{"--endpoint", "http://a", "a"}, cfg)
	RunCtxAdd([]string{"--endpoint", "http://b", "b"}, cfg)
	if got := RunCtxUse([]string{"b"}, cfg); got != 0 {
		t.Errorf("exit %d", got)
	}
	loaded, _ := LoadConfig(cfg)
	if loaded.CurrentContext != "b" {
		t.Errorf("current_context: %q", loaded.CurrentContext)
	}
}

func TestRunCtxRm_RefuseCurrent(t *testing.T) {
	cfg := tempCfg(t)
	RunCtxAdd([]string{"--endpoint", "http://a", "a"}, cfg)
	if got := RunCtxRm([]string{"a"}, cfg); got != 2 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxRm_NonCurrent(t *testing.T) {
	cfg := tempCfg(t)
	RunCtxAdd([]string{"--endpoint", "http://a", "a"}, cfg)
	RunCtxAdd([]string{"--endpoint", "http://b", "b"}, cfg)
	if got := RunCtxRm([]string{"b"}, cfg); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxList_Empty(t *testing.T) {
	cfg := tempCfg(t)
	if got := RunCtxList(nil, cfg); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxCurrent_Unset(t *testing.T) {
	cfg := tempCfg(t)
	if got := RunCtxCurrent(nil, cfg); got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestRunCtxCurrent_Set(t *testing.T) {
	cfg := tempCfg(t)
	RunCtxAdd([]string{"--endpoint", "http://a", "a"}, cfg)
	if got := RunCtxCurrent(nil, cfg); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRedactedConfig_HidesAPIKeys(t *testing.T) {
	const secret = "rim_super_secret_token"
	cfg := &Config{
		CurrentContext: "a",
		Contexts: map[string]Context{
			"a": {Endpoint: "http://a", APIKey: secret},
			"b": {Endpoint: "http://b"},
		},
	}
	redacted := redactedConfig(cfg)
	if redacted.Contexts["a"].APIKey != redactedAPIKeyPlaceholder {
		t.Fatalf("APIKey for context a = %q, want %q", redacted.Contexts["a"].APIKey, redactedAPIKeyPlaceholder)
	}
	if redacted.Contexts["a"].Endpoint != "http://a" {
		t.Fatalf("endpoint should be preserved, got %q", redacted.Contexts["a"].Endpoint)
	}
	if redacted.Contexts["b"].APIKey != "" {
		t.Fatalf("APIKey for a context with no key should stay empty, got %q", redacted.Contexts["b"].APIKey)
	}
	if cfg.Contexts["a"].APIKey != secret {
		t.Fatalf("redactedConfig must not mutate the original config; got %q", cfg.Contexts["a"].APIKey)
	}
}

func TestRunCtxList_JSONRedactsAPIKey(t *testing.T) {
	cfg := tempCfg(t)
	const secret = "rim_super_secret_token"
	if err := SaveConfig(cfg, &Config{
		CurrentContext: "a",
		Contexts:       map[string]Context{"a": {Endpoint: "http://a", APIKey: secret}},
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	out := captureStdout(t, func() {
		if got := RunCtxList([]string{"--output", "json"}, cfg); got != 0 {
			t.Errorf("exit %d", got)
		}
	})
	if strings.Contains(out, secret) {
		t.Fatalf("ctx list --output json leaked the API key to stdout: %s", out)
	}
	if !strings.Contains(out, redactedAPIKeyPlaceholder) {
		t.Fatalf("ctx list --output json should render the redaction placeholder, got: %s", out)
	}
}
