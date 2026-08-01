// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestResolveMaxQuietPeriod_BuiltinDefaultIsZeroDisabled(t *testing.T) {
	t.Parallel()
	got := resolveMaxQuietPeriod(nil, 0)
	if got != 0 {
		t.Fatalf("built-in max_quiet_period default = %v, want 0 (disabled) — comparing against the "+
			"literal, not against defaultMaxQuietPeriod itself, so a regression to the headline default "+
			"value cannot hide behind a self-referential assertion", got)
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

func TestComputeEffectiveDeadlineSecs_SubSecondNeverSilentlyDisabled(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{MaxQuietPeriod: "500ms", MaxRuntime: "999ms"}
	quietSecs, runtimeSecs := computeEffectiveDeadlineSecs(node, 0, 0)
	if quietSecs == nil || *quietSecs != 1 {
		t.Fatalf("quietSecs = %v, want pointer to 1 (sub-second positive duration must round up, never disable)", quietSecs)
	}
	if runtimeSecs == nil || *runtimeSecs != 1 {
		t.Fatalf("runtimeSecs = %v, want pointer to 1", runtimeSecs)
	}
}

func TestComputeEffectiveDeadlineSecs_ZeroStaysDisabled(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{}
	quietSecs, runtimeSecs := computeEffectiveDeadlineSecs(node, 0, 0)
	if quietSecs != nil {
		t.Fatalf("quietSecs = %v, want nil (disabled)", quietSecs)
	}
	if runtimeSecs != nil {
		t.Fatalf("runtimeSecs = %v, want nil (disabled)", runtimeSecs)
	}
}

func TestComputeEffectiveDeadlineSecs_RoundsToNearestSecond(t *testing.T) {
	t.Parallel()
	node := &spec.TemplateNodeDef{MaxQuietPeriod: "1500ms"}
	quietSecs, _ := computeEffectiveDeadlineSecs(node, 0, 0)
	if quietSecs == nil || *quietSecs != 2 {
		t.Fatalf("quietSecs = %v, want pointer to 2 (round-half-up from 1.5s)", quietSecs)
	}
}
