// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestResolveSyncRPCDeadline_PerNodeOverridesDeploymentDefault(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{SyncRPCDeadline: "5s"}
	got := resolveSyncRPCDeadline(node, 10*time.Second)
	if got != 5*time.Second {
		t.Fatalf("per-node override = %v, want 5s", got)
	}
}

func TestResolveSyncRPCDeadline_FallsBackToDeploymentDefault(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{}
	got := resolveSyncRPCDeadline(node, 10*time.Second)
	if got != 10*time.Second {
		t.Fatalf("deployment fallback = %v, want 10s", got)
	}
}

func TestResolveSyncRPCDeadline_FallsBackToBuiltinDefault(t *testing.T) {
	t.Parallel()
	got := resolveSyncRPCDeadline(nil, 0)
	if got != defaultSyncRPCDeadline {
		t.Fatalf("built-in fallback = %v, want %v", got, defaultSyncRPCDeadline)
	}
}

func TestResolveSyncRPCDeadline_PerNodeZeroSecondsExplicitlyDisables(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{SyncRPCDeadline: "0s"}
	got := resolveSyncRPCDeadline(node, 30*time.Second)
	if got != 0 {
		t.Fatalf("explicit 0s = %v, want 0 (disabled)", got)
	}
}

func TestResolveSyncRPCDeadline_PerNodeUnparseableFallsThrough(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{SyncRPCDeadline: "junk"}
	got := resolveSyncRPCDeadline(node, 12*time.Second)
	if got != 12*time.Second {
		t.Fatalf("unparseable per-node = %v, want deployment fallback 12s", got)
	}
}

func TestResolveMaxQuietPeriod_PerNodeOverridesDeploymentDefault(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{MaxQuietPeriod: "2m"}
	got := resolveMaxQuietPeriod(node, 30*time.Minute)
	if got != 2*time.Minute {
		t.Fatalf("per-node override = %v, want 2m", got)
	}
}

func TestResolveMaxQuietPeriod_FallsBackToBuiltinDefault(t *testing.T) {
	t.Parallel()
	got := resolveMaxQuietPeriod(nil, 0)
	if got != defaultMaxQuietPeriod {
		t.Fatalf("built-in fallback = %v, want %v", got, defaultMaxQuietPeriod)
	}
}

func TestResolveMaxRuntime_PerNodeOverridesDeploymentDefault(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{MaxRuntime: "10m"}
	got := resolveMaxRuntime(node, 1*time.Hour)
	if got != 10*time.Minute {
		t.Fatalf("per-node override = %v, want 10m", got)
	}
}

func TestResolveMaxRuntime_BuiltinDefaultIsZeroDisabled(t *testing.T) {
	t.Parallel()
	got := resolveMaxRuntime(nil, 0)
	if got != 0 {
		t.Fatalf("built-in max_runtime default = %v, want 0 (disabled)", got)
	}
}
