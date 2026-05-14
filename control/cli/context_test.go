// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli

import (
	"path/filepath"
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
