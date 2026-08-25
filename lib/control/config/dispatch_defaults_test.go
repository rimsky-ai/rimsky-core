// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestDispatchDefaultsZeroWhenAbsent(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
`)
	d := cfg.Dispatch
	if d.SyncRPCDeadlineDefault != 0 {
		t.Fatalf("SyncRPCDeadlineDefault = %s, want 0", d.SyncRPCDeadlineDefault)
	}
	if d.MaxQuietPeriodDefault != 0 {
		t.Fatalf("MaxQuietPeriodDefault = %s, want 0", d.MaxQuietPeriodDefault)
	}
	if d.MaxRuntimeDefault != 0 {
		t.Fatalf("MaxRuntimeDefault = %s, want 0", d.MaxRuntimeDefault)
	}
	if d.MaxRetriesDefault != 0 {
		t.Fatalf("MaxRetriesDefault = %d, want 0", d.MaxRetriesDefault)
	}
	if d.RetryBackoffDefault != nil {
		t.Fatalf("RetryBackoffDefault = %+v, want nil", d.RetryBackoffDefault)
	}
}

func TestDispatchDefaultsExplicitValuesHonored(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
dispatch_defaults:
  sync_rpc_deadline: 45s
  max_quiet_period: 10m
  max_runtime: 24h
  max_retries: 4
  retry_backoff:
    kind: exponential
    jitter: plus_minus
    base_delay_ms: 250
    max_delay_ms: 30000
`)
	d := cfg.Dispatch
	if d.SyncRPCDeadlineDefault != 45*time.Second {
		t.Fatalf("SyncRPCDeadlineDefault = %s, want 45s", d.SyncRPCDeadlineDefault)
	}
	if d.MaxQuietPeriodDefault != 10*time.Minute {
		t.Fatalf("MaxQuietPeriodDefault = %s, want 10m", d.MaxQuietPeriodDefault)
	}
	if d.MaxRuntimeDefault != 24*time.Hour {
		t.Fatalf("MaxRuntimeDefault = %s, want 24h", d.MaxRuntimeDefault)
	}
	if d.MaxRetriesDefault != 4 {
		t.Fatalf("MaxRetriesDefault = %d, want 4", d.MaxRetriesDefault)
	}
	if d.RetryBackoffDefault == nil {
		t.Fatalf("RetryBackoffDefault = nil, want the declared object")
	}
	want := spec.RetryBackoffConfig{
		Kind:        spec.BackoffExponential,
		Jitter:      spec.JitterPlusMinus,
		BaseDelayMs: 250,
		MaxDelayMs:  30000,
	}
	if *d.RetryBackoffDefault != want {
		t.Fatalf("RetryBackoffDefault = %+v, want %+v", *d.RetryBackoffDefault, want)
	}
}

func TestDispatchDefaultsNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
dispatch_defaults:
  max_runtime: -1h
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadRimskyConfigYAML(path); err == nil {
		t.Fatalf("expected a validation error for negative max_runtime, got nil")
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestDispatchDefaultsRejectRetryPolicyTheDeploymentCannotApply(t *testing.T) {
	cases := map[string]string{
		"a negative retry cap": `
dispatch_defaults:
  max_retries: -1
`,
		"a backoff kind rimsky does not compute": `
dispatch_defaults:
  retry_backoff:
    kind: fibonacci
    base_delay_ms: 100
`,
		"a jitter rimsky does not compute": `
dispatch_defaults:
  retry_backoff:
    jitter: gaussian
    base_delay_ms: 100
`,
		"a cap below the base delay": `
dispatch_defaults:
  retry_backoff:
    base_delay_ms: 500
    max_delay_ms: 100
`,
		"a backoff with no base delay": `
dispatch_defaults:
  retry_backoff:
    kind: exponential
`,
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "rimsky.yml")
			body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
` + block
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := LoadRimskyConfigYAML(path); err == nil {
				t.Fatalf("expected %s to be refused at load, got nil", name)
			}
		})
	}
}
