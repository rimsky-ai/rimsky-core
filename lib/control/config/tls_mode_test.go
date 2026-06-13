// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Parse-time validation of the per-peer `tls:` key: accepted values are
// exactly "" (→ off), off, and required, on every peer kind
// (claim_producers / executors / publishers). Anything else — including
// the retired "optional" — is a config error naming the entry.

package config

import (
	"strings"
	"testing"
)

// tlsTestYAML renders a rimsky.yml carrying one entry of each peer kind
// with the given `tls:` line (pass "" to omit the key entirely).
func tlsTestYAML(tlsLine string) string {
	indent := ""
	if tlsLine != "" {
		indent = "    " + tlsLine + "\n"
	}
	return `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  items-store:
    endpoint: items-store:9101
    write_semantics_allowed: [sync]
` + indent + `executors:
  agent-runner:
    transport: grpc
    endpoint: agent-runner:9090
` + indent + `publishers:
  ticker:
    endpoint: ticker:9300
` + indent
}

func TestTLSMode_AbsentKey_DefaultsOff(t *testing.T) {
	cfg := mustLoadCfg(t, tlsTestYAML(""))
	if got := cfg.Stores.Stores["items-store"].TLS; got != "off" {
		t.Fatalf("claim_producer TLS = %q, want off", got)
	}
	if got := cfg.Executors.Executors["agent-runner"].TLS; got != "off" {
		t.Fatalf("executor TLS = %q, want off", got)
	}
	if got := cfg.Publishers.Publishers["ticker"].TLS; got != "off" {
		t.Fatalf("publisher TLS = %q, want off", got)
	}
}

func TestTLSMode_ValidValues_Pass(t *testing.T) {
	for _, mode := range []string{"off", "required"} {
		cfg := mustLoadCfg(t, tlsTestYAML("tls: "+mode))
		if got := cfg.Stores.Stores["items-store"].TLS; got != mode {
			t.Fatalf("claim_producer TLS = %q, want %q", got, mode)
		}
		if got := cfg.Executors.Executors["agent-runner"].TLS; got != mode {
			t.Fatalf("executor TLS = %q, want %q", got, mode)
		}
		if got := cfg.Publishers.Publishers["ticker"].TLS; got != mode {
			t.Fatalf("publisher TLS = %q, want %q", got, mode)
		}
	}
}

// TestTLSMode_OptionalRejected_NamingEntry verifies the retired
// "optional" value (and any other junk) rejects at parse with an error
// naming the entry and the accepted values, per peer kind.
func TestTLSMode_OptionalRejected_NamingEntry(t *testing.T) {
	cases := []struct {
		name      string
		yamlBody  string
		entryHint string
	}{
		{
			name: "claim_producer optional",
			yamlBody: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  items-store:
    endpoint: items-store:9101
    write_semantics_allowed: [sync]
    tls: optional
`,
			entryHint: `claim_producers["items-store"]`,
		},
		{
			name: "executor optional",
			yamlBody: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
executors:
  agent-runner:
    transport: grpc
    endpoint: agent-runner:9090
    tls: optional
`,
			entryHint: `executors["agent-runner"]`,
		},
		{
			name: "publisher optional",
			yamlBody: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
publishers:
  ticker:
    endpoint: ticker:9300
    tls: optional
`,
			entryHint: `publishers["ticker"]`,
		},
		{
			name: "executor junk value",
			yamlBody: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
executors:
  agent-runner:
    transport: grpc
    endpoint: agent-runner:9090
    tls: mutual
`,
			entryHint: `executors["agent-runner"]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadCfgErr(t, tc.yamlBody)
			if err == nil {
				t.Fatalf("expected config error, got nil")
			}
			if !strings.Contains(err.Error(), tc.entryHint) {
				t.Fatalf("error %q does not name the entry %s", err, tc.entryHint)
			}
			if !strings.Contains(err.Error(), "one of: off, required") {
				t.Fatalf("error %q does not name the accepted values", err)
			}
		})
	}
}

// TestTLSMode_HTTPBridgeRequiredNeedsHTTPS verifies the
// ExecutorsConfig.Validate guard: a `tls: required` HTTP-bridge
// executor must carry an https:// endpoint — a plaintext URL fails
// startup validation naming the entry, never accepted-and-ignored.
func TestTLSMode_HTTPBridgeRequiredNeedsHTTPS(t *testing.T) {
	reject := ExecutorsConfig{Executors: map[string]ExecutorEntry{
		"bridge-runner": {Transport: "http", Endpoint: "http://bridge-runner:8080", TLS: "required"},
	}}
	err := reject.Validate()
	if err == nil {
		t.Fatal("expected Validate to reject tls: required with a plaintext http:// endpoint")
	}
	if !strings.Contains(err.Error(), "bridge-runner") || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("error %q does not name the entry and the https requirement", err)
	}

	accept := ExecutorsConfig{Executors: map[string]ExecutorEntry{
		"bridge-runner": {Transport: "http", Endpoint: "https://bridge-runner:8443", TLS: "required"},
		"agent-runner":  {Transport: "grpc", Endpoint: "agent-runner:9090", TLS: "required"},
		"plain-bridge":  {Transport: "http", Endpoint: "http://plain-bridge:8080", TLS: "off"},
	}}
	if err := accept.Validate(); err != nil {
		t.Fatalf("Validate rejected valid tls/transport combinations: %v", err)
	}
}
