// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRimskyConfigYAML_SameLogicalNameAcrossRoleBlocksRegistersIndependently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  bundle:
    endpoint: bundle:9090
    tls: off
    write_semantics_allowed: [sync]
executors:
  bundle:
    transport: grpc
    endpoint: bundle:9090
    tls: off
publishers:
  bundle:
    endpoint: bundle:9090
    tls: off
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write rimsky.yml: %v", err)
	}
	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: a single binary registering the same logical name %q "+
			"in each of claim_producers/executors/publishers, all pointing at the same endpoint, "+
			"must load cleanly: %v", "bundle", err)
	}

	cp, ok := cfg.ClaimProducers.ClaimProducers["bundle"]
	if !ok {
		t.Fatal("claim_producers[bundle] missing after load")
	}
	if cp.Endpoint != "bundle:9090" {
		t.Fatalf("claim_producers[bundle].endpoint = %q, want bundle:9090", cp.Endpoint)
	}

	ex, ok := cfg.Executors.Executors["bundle"]
	if !ok {
		t.Fatal("executors[bundle] missing after load")
	}
	if ex.Endpoint != "bundle:9090" {
		t.Fatalf("executors[bundle].endpoint = %q, want bundle:9090", ex.Endpoint)
	}

	pub, ok := cfg.Publishers.Publishers["bundle"]
	if !ok {
		t.Fatal("publishers[bundle] missing after load")
	}
	if pub.Endpoint != "bundle:9090" {
		t.Fatalf("publishers[bundle].endpoint = %q, want bundle:9090", pub.Endpoint)
	}

	if !cfg.Executors.ExecutorDeclared("bundle") {
		t.Fatal("ExecutorsConfig.ExecutorDeclared(bundle) = false after a same-name multi-role load")
	}
}
