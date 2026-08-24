// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package launch

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/ports"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

func clearSupervisorAdvertiseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST", "")
	t.Setenv("RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT", "")
}

func TestResolveSupervisorConfig_DefaultsAndClamps(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)
	resolved, err := resolveSupervisorConfig(config.SupervisorSection{})
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

func intPtr(n int) *int { return &n }

// @decision: default-port-allocation
func TestResolveSupervisorConfig_CallbackPortDefaultsIntoTheCoreBlockAndZeroStaysEphemeral(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)

	resolved, err := resolveSupervisorConfig(config.SupervisorSection{})
	if err != nil {
		t.Fatalf("resolveSupervisorConfig: %v", err)
	}
	if resolved.CallbackPort != ports.SupervisorCallback {
		t.Errorf("CallbackPort with no callback.port = %d, want %d", resolved.CallbackPort, ports.SupervisorCallback)
	}

	ephemeral, err := resolveSupervisorConfig(config.SupervisorSection{
		Callback: config.SupervisorCallbackSection{Port: intPtr(0)},
	})
	if err != nil {
		t.Fatalf("resolveSupervisorConfig: %v", err)
	}
	if ephemeral.CallbackPort != 0 {
		t.Errorf("CallbackPort with an explicit callback.port of 0 = %d, want 0 so the operating system assigns one", ephemeral.CallbackPort)
	}
}

func TestResolveSupervisorConfig_ExplicitValuesPassThrough(t *testing.T) {
	clearSupervisorAdvertiseEnv(t)
	cfg := config.SupervisorSection{
		SupervisorID:        "sup-explicit",
		Concurrency:         8,
		LivenessIntervalMs:  200,
		ClaimPollIntervalMs: 75,
		Callback: config.SupervisorCallbackSection{
			Host: "127.0.0.5",
			Port: intPtr(9999),
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
	cfg := config.SupervisorSection{
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
		resolved, err := resolveSupervisorConfig(config.SupervisorSection{
			Callback: config.SupervisorCallbackSection{AdvertiseHost: "yaml-host"},
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
		resolved, err := resolveSupervisorConfig(config.SupervisorSection{
			Callback: config.SupervisorCallbackSection{AdvertiseHost: "yaml-host"},
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
		resolved, err := resolveSupervisorConfig(config.SupervisorSection{})
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
		resolved, err := resolveSupervisorConfig(config.SupervisorSection{
			Callback: config.SupervisorCallbackSection{AdvertisePort: 8001},
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
		resolved, err := resolveSupervisorConfig(config.SupervisorSection{
			Callback: config.SupervisorCallbackSection{AdvertisePort: 8001},
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
		_, err := resolveSupervisorConfig(config.SupervisorSection{})
		if err == nil {
			t.Fatal("resolveSupervisorConfig: want error for non-numeric advertise port")
		}
		if !strings.Contains(err.Error(), "not-a-port") {
			t.Errorf("error %q does not name the offending value", err.Error())
		}
	})
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

func TestBuildSupervisorResolver_NoLateBindProxiesReturnsStaticResolver(t *testing.T) {
	static := executor.NewStaticResolver(map[string]executor.Endpoint{
		"exec-a": {Transport: "grpc", URL: "host:9090"},
	})

	got := buildSupervisorResolver(static, nil, nil)

	if got != executor.Resolver(static) {
		t.Fatalf("buildSupervisorResolver with no late-bind proxies: got %T, want the same *StaticResolver instance", got)
	}
}

func TestBuildSupervisorResolver_LateBindProxiesWrapsInLateBindResolver(t *testing.T) {
	static := executor.NewStaticResolver(map[string]executor.Endpoint{
		"exec-a": {Transport: "grpc", URL: "host:9090"},
	})
	lateBindProxies := map[string]string{"executor": "host-daemon-proxy"}

	got := buildSupervisorResolver(static, lateBindProxies, nil)

	lbr, ok := got.(*executor.LateBindResolver)
	if !ok {
		t.Fatalf("buildSupervisorResolver with late-bind proxies configured: got %T, want *executor.LateBindResolver — a late-bound executor dispatch would resolve via the bare static resolver and terminal unresolved_executor", got)
	}
	if unwrapped := lbr.Unwrap(); unwrapped != executor.Resolver(static) {
		t.Fatalf("LateBindResolver.Unwrap(): got %T, want the same *StaticResolver instance passed in", unwrapped)
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
