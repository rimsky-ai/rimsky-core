package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExecutorEntry_ObservabilityEndpoint_Optional verifies that
// rimsky.yml without `observability_endpoint:` parses successfully and
// the field is empty (callers fall back to Endpoint).
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

// TestExecutorEntry_ObservabilityEndpoint_Honored verifies that the
// optional `observability_endpoint:` field reaches the parsed
// ExecutorEntry.
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

// TestStoreEntry_ObservabilityEndpoint_Honored mirrors the executor case
// for claim-producer entries.
func TestStoreEntry_ObservabilityEndpoint_Honored(t *testing.T) {
	yamlBody := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: store-postgres:9101
    observability_endpoint: store-postgres:9102
    write_semantics_envelope: [sync]
`
	cfg := mustLoadCfg(t, yamlBody)
	s := cfg.Stores.Stores["topics-ring"]
	if s.ObservabilityEndpoint != "store-postgres:9102" {
		t.Fatalf("ObservabilityEndpoint = %q, want store-postgres:9102", s.ObservabilityEndpoint)
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

// TestLoadDeployRimskyYAML_Phase4Shape parses the canonical
// deploy/rimsky.yml against the post-Phase-4 parser and asserts each
// claim_producers entry carries the protocols list and a non-empty
// write_semantics_envelope. Gates the verification block for Task 28.
func TestLoadDeployRimskyYAML_Phase4Shape(t *testing.T) {
	cfg, err := LoadRimskyConfigYAML(filepath.Join("..", "..", "deploy", "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML(deploy/rimsky.yml): %v", err)
	}
	if len(cfg.Stores.Stores) == 0 {
		t.Fatal("expected at least one claim_producer in deploy/rimsky.yml")
	}
	for name, e := range cfg.Stores.Stores {
		if len(e.Protocols) == 0 {
			t.Errorf("claim_producers[%q]: protocols list empty", name)
		}
		if !e.HasProtocol(ProtocolClaimProducer) {
			t.Errorf("claim_producers[%q]: protocols list must include %q", name, ProtocolClaimProducer)
		}
		if len(e.Capabilities.WriteSemanticsEnvelope) == 0 {
			t.Errorf("claim_producers[%q]: write_semantics_envelope empty", name)
		}
	}
	if len(cfg.Executors.Executors) == 0 {
		t.Fatal("expected at least one executor in deploy/rimsky.yml")
	}
	for name, e := range cfg.Executors.Executors {
		if !e.HasProtocol(ProtocolExecutor) {
			t.Errorf("executors[%q]: protocols list must include %q", name, ProtocolExecutor)
		}
	}
}
