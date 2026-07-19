// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

func clearSupervisorAdvertiseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST", "")
	t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT", "")
}

func TestResolveSupervisorConfig_DefaultsAndClamps(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)
	resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{})
	if err != nil {
		t.Fatalf("resolveSupervisorConfig: %v", err)
	}
	if resolved.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4 (default clamp)", resolved.Concurrency)
	}
	if resolved.LivenessInterval != 5000*time.Millisecond {
		t.Errorf("LivenessInterval = %v, want 5000ms (default clamp)", resolved.LivenessInterval)
	}
	if resolved.ClaimPollInterval != 1000*time.Millisecond {
		t.Errorf("ClaimPollInterval = %v, want 1000ms (default clamp)", resolved.ClaimPollInterval)
	}
	if resolved.CallbackHost != "0.0.0.0" {
		t.Errorf("CallbackHost = %q, want 0.0.0.0", resolved.CallbackHost)
	}
	if resolved.AdvertiseHost != "" || resolved.AdvertiseHostSource != "unset" {
		t.Errorf("AdvertiseHost = %q, source = %q, want empty/unset", resolved.AdvertiseHost, resolved.AdvertiseHostSource)
	}
	wantSupID := fmt.Sprintf("%s-%d", mustHostname(t), os.Getpid())
	if resolved.SupervisorID != wantSupID {
		t.Errorf("SupervisorID = %q, want %q", resolved.SupervisorID, wantSupID)
	}
}

func TestResolveSupervisorConfig_ExplicitValuesPassThrough(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)
	cfg := supervisorYAMLConfig{
		SupervisorID:        "sup-explicit",
		Concurrency:         8,
		LivenessIntervalMs:  200,
		ClaimPollIntervalMs: 75,
		Callback: supervisorYAMLCallback{
			Host: "127.0.0.5",
			Port: 9999,
		},
	}
	resolved, err := resolveSupervisorConfig(cfg)
	if err != nil {
		t.Fatalf("resolveSupervisorConfig: %v", err)
	}
	if resolved.SupervisorID != "sup-explicit" {
		t.Errorf("SupervisorID = %q, want sup-explicit", resolved.SupervisorID)
	}
	if resolved.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", resolved.Concurrency)
	}
	if resolved.LivenessInterval != 200*time.Millisecond {
		t.Errorf("LivenessInterval = %v, want 200ms", resolved.LivenessInterval)
	}
	if resolved.ClaimPollInterval != 75*time.Millisecond {
		t.Errorf("ClaimPollInterval = %v, want 75ms", resolved.ClaimPollInterval)
	}
	if resolved.CallbackHost != "127.0.0.5" || resolved.CallbackPort != 9999 {
		t.Errorf("CallbackHost/Port = %q/%d, want 127.0.0.5/9999", resolved.CallbackHost, resolved.CallbackPort)
	}
}

func TestResolveSupervisorConfig_BelowFloorValuesAreClamped(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)
	cfg := supervisorYAMLConfig{
		Concurrency:         0,
		LivenessIntervalMs:  99,
		ClaimPollIntervalMs: 49,
	}
	resolved, err := resolveSupervisorConfig(cfg)
	if err != nil {
		t.Fatalf("resolveSupervisorConfig: %v", err)
	}
	if resolved.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4 (below-floor clamp)", resolved.Concurrency)
	}
	if resolved.LivenessInterval != 5000*time.Millisecond {
		t.Errorf("LivenessInterval = %v, want 5000ms (below-floor clamp)", resolved.LivenessInterval)
	}
	if resolved.ClaimPollInterval != 1000*time.Millisecond {
		t.Errorf("ClaimPollInterval = %v, want 1000ms (below-floor clamp)", resolved.ClaimPollInterval)
	}
}

func TestResolveSupervisorConfig_AdvertiseHostPrecedence(t *testing.T) {
	t.Run("env wins over yaml", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST", "env-host")
		resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{
			Callback: supervisorYAMLCallback{AdvertiseHost: "yaml-host"},
		})
		if err != nil {
			t.Fatalf("resolveSupervisorConfig: %v", err)
		}
		if resolved.AdvertiseHost != "env-host" || resolved.AdvertiseHostSource != "env:RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST" {
			t.Errorf("AdvertiseHost = %q, source = %q, want env-host / env:...", resolved.AdvertiseHost, resolved.AdvertiseHostSource)
		}
	})

	t.Run("yaml used when env unset", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{
			Callback: supervisorYAMLCallback{AdvertiseHost: "yaml-host"},
		})
		if err != nil {
			t.Fatalf("resolveSupervisorConfig: %v", err)
		}
		if resolved.AdvertiseHost != "yaml-host" || resolved.AdvertiseHostSource != "yaml:callback.advertise_host" {
			t.Errorf("AdvertiseHost = %q, source = %q, want yaml-host / yaml:callback.advertise_host", resolved.AdvertiseHost, resolved.AdvertiseHostSource)
		}
	})

	t.Run("unset when neither is set", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{})
		if err != nil {
			t.Fatalf("resolveSupervisorConfig: %v", err)
		}
		if resolved.AdvertiseHost != "" || resolved.AdvertiseHostSource != "unset" {
			t.Errorf("AdvertiseHost = %q, source = %q, want empty / unset", resolved.AdvertiseHost, resolved.AdvertiseHostSource)
		}
	})
}

func TestResolveSupervisorConfig_AdvertisePortPrecedence(t *testing.T) {
	t.Run("env wins over yaml", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT", "7001")
		resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{
			Callback: supervisorYAMLCallback{AdvertisePort: 8001},
		})
		if err != nil {
			t.Fatalf("resolveSupervisorConfig: %v", err)
		}
		if resolved.AdvertisePort != 7001 {
			t.Errorf("AdvertisePort = %d, want 7001", resolved.AdvertisePort)
		}
	})

	t.Run("yaml used when env unset", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		resolved, err := resolveSupervisorConfig(supervisorYAMLConfig{
			Callback: supervisorYAMLCallback{AdvertisePort: 8001},
		})
		if err != nil {
			t.Fatalf("resolveSupervisorConfig: %v", err)
		}
		if resolved.AdvertisePort != 8001 {
			t.Errorf("AdvertisePort = %d, want 8001", resolved.AdvertisePort)
		}
	})

	t.Run("non-numeric env port is an error", func(t *testing.T) {
		clearSupervisorAdvertiseEnv(t)
		t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT", "not-a-port")
		_, err := resolveSupervisorConfig(supervisorYAMLConfig{})
		if err == nil {
			t.Fatal("resolveSupervisorConfig: want error for non-numeric advertise port")
		}
		if !strings.Contains(err.Error(), "not-a-port") {
			t.Errorf("error %q does not name the offending value", err.Error())
		}
	})
}

func TestLoadSupervisorYAML_ExpandsEnvVars(t *testing.T) {
	t.Setenv("SUPERVISOR_TEST_HOST", "expanded-host.example")
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.yml")
	contents := "supervisor_id: sup-1\ncallback:\n  advertise_host: ${SUPERVISOR_TEST_HOST}\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadSupervisorYAML(path)
	if err != nil {
		t.Fatalf("loadSupervisorYAML: %v", err)
	}
	if cfg.Callback.AdvertiseHost != "expanded-host.example" {
		t.Errorf("Callback.AdvertiseHost = %q, want expanded-host.example (env expansion)", cfg.Callback.AdvertiseHost)
	}
}

func TestLoadSupervisorYAML_MissingFile(t *testing.T) {
	_, err := loadSupervisorYAML(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if err == nil {
		t.Fatal("loadSupervisorYAML: want error for a missing file")
	}
}

func TestLoadSupervisorYAML_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.yml")
	if err := os.WriteFile(path, []byte("concurrency: [this is not an int\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := loadSupervisorYAML(path)
	if err == nil {
		t.Fatal("loadSupervisorYAML: want error for malformed YAML")
	}
}

func TestMergeBundledExecutorAliases_ConfiguredWinsOverBundled(t *testing.T) {
	configured := map[string]executor.Endpoint{
		"exec-a": {Transport: "grpc", URL: "configured-host:9090"},
	}
	resolver := executor.NewStaticResolver(configured)

	bundled := map[string]executor.Endpoint{
		"exec-a": {Transport: "inproc", URL: "bundled-a"},
		"exec-b": {Transport: "inproc", URL: "bundled-b"},
	}

	var overridden []string
	mergeBundledExecutorAliases(resolver, configured, bundled, func(name string) {
		overridden = append(overridden, name)
	})

	got, ok := resolver.Resolve("exec-a", executor.DispatchContext{})
	if !ok {
		t.Fatalf("exec-a missing from resolver after merge")
	}
	want := executor.Endpoint{Transport: "grpc", URL: "configured-host:9090"}
	if got != want {
		t.Fatalf("exec-a was overwritten by bundled alias: got %+v, want %+v", got, want)
	}

	got, ok = resolver.Resolve("exec-b", executor.DispatchContext{})
	if !ok {
		t.Fatalf("exec-b was not registered from bundled aliases")
	}
	wantB := executor.Endpoint{Transport: "inproc", URL: "bundled-b"}
	if got != wantB {
		t.Fatalf("exec-b: got %+v, want %+v", got, wantB)
	}

	if len(overridden) != 1 || overridden[0] != "exec-a" {
		t.Fatalf("onOverride callback: got %v, want exactly [exec-a]", overridden)
	}
}

func mustHostname(t *testing.T) string {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	return hostname
}
