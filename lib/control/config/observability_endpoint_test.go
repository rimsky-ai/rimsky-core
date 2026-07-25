// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorEntry_ObservabilityEndpoint_Optional(t *testing.T) {
	yamlBody := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
executors:
  http-node:
    transport: grpc
    endpoint: http-node:9090
    tls: off
`
	cfg := mustLoadCfg(t, yamlBody)
	e, ok := cfg.Executors.Executors["http-node"]
	if !ok {
		t.Fatalf("expected http-node executor entry")
	}
	if e.ObservabilityEndpoint != "" {
		t.Fatalf("ObservabilityEndpoint = %q, want empty", e.ObservabilityEndpoint)
	}
}

func TestExecutorEntry_ObservabilityEndpoint_Honored(t *testing.T) {
	yamlBody := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
executors:
  claude-agent:
    transport: grpc
    endpoint: claude-agent:9090
    observability_endpoint: claude-agent:9091
    tls: off
`
	cfg := mustLoadCfg(t, yamlBody)
	e := cfg.Executors.Executors["claude-agent"]
	if e.ObservabilityEndpoint != "claude-agent:9091" {
		t.Fatalf("ObservabilityEndpoint = %q, want claude-agent:9091", e.ObservabilityEndpoint)
	}
}

func TestClaimProducerEntry_ObservabilityEndpoint_Honored(t *testing.T) {
	yamlBody := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: claim-producer-postgres:9101
    observability_endpoint: claim-producer-postgres:9102
    write_semantics_allowed: [sync]
`
	cfg := mustLoadCfg(t, yamlBody)
	s := cfg.ClaimProducers.ClaimProducers["topics-ring"]
	if s.ObservabilityEndpoint != "claim-producer-postgres:9102" {
		t.Fatalf("ObservabilityEndpoint = %q, want claim-producer-postgres:9102", s.ObservabilityEndpoint)
	}
}

func mustLoadCfg(t *testing.T, body string) RimskyConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	return cfg
}
