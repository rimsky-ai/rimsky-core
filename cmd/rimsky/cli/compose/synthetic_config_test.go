// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"gopkg.in/yaml.v3"
)

func TestWriteSyntheticRimskyYAML_PathsCorrect(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-00-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{Project: "demo"}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML round-trip: %v", err)
	}
	if cfg.Persistence.Driver != "sqlite" {
		t.Fatalf("persistence.driver: got %q, want %q", cfg.Persistence.Driver, "sqlite")
	}
	if cfg.Persistence.SQLite == nil {
		t.Fatalf("persistence.sqlite block missing")
	}
	wantSQLitePath := filepath.Join(runDir, "state.db")
	if cfg.Persistence.SQLite.Path != wantSQLitePath {
		t.Fatalf("persistence.sqlite.path: got %q, want %q", cfg.Persistence.SQLite.Path, wantSQLitePath)
	}
	if cfg.Blob.Backend != "filesystem" {
		t.Fatalf("persistence.blob.backend: got %q, want %q", cfg.Blob.Backend, "filesystem")
	}
	wantBlobRoot := filepath.Join(runDir, "blobs")
	if cfg.Blob.Filesystem.Root != wantBlobRoot {
		t.Fatalf("persistence.blob.filesystem.root: got %q, want %q", cfg.Blob.Filesystem.Root, wantBlobRoot)
	}
}

func TestWriteSyntheticRimskyYAML_MergedExecutors(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-01-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	merged := map[string]ManifestExecutorEntry{
		"foo": {Transport: "grpc", Endpoint: "127.0.0.1:9091"},
	}
	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "demo"}, merged, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["foo"]
	if !ok {
		t.Fatalf("executors[foo] missing from round-tripped config")
	}
	if entry.Transport != "grpc" {
		t.Fatalf("executors[foo].transport: got %q, want %q", entry.Transport, "grpc")
	}
	if entry.Endpoint != "127.0.0.1:9091" {
		t.Fatalf("executors[foo].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9091")
	}
}

func TestWriteSyntheticRimskyYAML_ManifestExecutorsFoldedAsBase(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-04-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"manifest-foo": {Transport: "grpc", Endpoint: "127.0.0.1:9101"},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["manifest-foo"]
	if !ok {
		t.Fatalf("manifest executor not folded into synthetic config")
	}
	if entry.Endpoint != "127.0.0.1:9101" {
		t.Fatalf("executors[manifest-foo].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9101")
	}
}

func TestWriteSyntheticRimskyYAML_SpawnOverlayOverridesManifest(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-05-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"foo": {Transport: "grpc", Endpoint: "127.0.0.1:1111"},
		},
	}
	overlay := map[string]ManifestExecutorEntry{
		"foo": {Transport: "grpc", Endpoint: "127.0.0.1:2222"},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, overlay, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["foo"]
	if !ok {
		t.Fatalf("executors[foo] missing from round-tripped config")
	}
	if entry.Endpoint != "127.0.0.1:2222" {
		t.Fatalf("spawn overlay did not override manifest: endpoint=%q want=%q", entry.Endpoint, "127.0.0.1:2222")
	}
}

func TestWriteSyntheticRimskyYAML_ExecutorProtocolsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-06-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"multi": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9201",
				Protocols: []string{"executor", "lifecycle_subscriber"},
			},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["multi"]
	if !ok {
		t.Fatalf("executors[multi] missing from round-tripped config")
	}
	if !entry.HasProtocol("executor") || !entry.HasProtocol("lifecycle_subscriber") {
		t.Fatalf("executors[multi].protocols round-trip lost values: got %v", entry.Protocols)
	}
}

func TestWriteSyntheticRimskyYAML_ClaimProducersFromManifest(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-02-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9100",
				WriteSemanticsAllowed: []string{"sync"},
			},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Stores.Stores["items"]
	if !ok {
		t.Fatalf("claim_producers[items] missing from round-tripped config")
	}
	if entry.Endpoint != "127.0.0.1:9100" {
		t.Fatalf("claim_producers[items].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9100")
	}
}

// @decision: services-source
func TestLoadSiblingBlocks_PublishersAndNamedLocksFromSibling(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-08-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	siblingPath := filepath.Join(tmp, "rimsky.yml")
	sibling := []byte(`publishers:
  ticker:
    endpoint: 127.0.0.1:9301
    tls: off
named_locks:
  resource-a:
    limit: 3
`)
	if err := os.WriteFile(siblingPath, sibling, 0o644); err != nil {
		t.Fatalf("write sibling rimsky.yml: %v", err)
	}
	siblings, err := LoadSiblingBlocks(siblingPath)
	if err != nil {
		t.Fatalf("LoadSiblingBlocks: %v", err)
	}
	if siblings == nil {
		t.Fatal("LoadSiblingBlocks returned nil for a sibling that declares publishers+named_locks")
	}
	if _, ok := siblings.Publishers["ticker"]; !ok {
		t.Fatalf("publishers[ticker] missing from loaded sibling: %#v", siblings.Publishers)
	}
	if lock, ok := siblings.NamedLocks["resource-a"]; !ok || lock.Limit != 3 {
		t.Fatalf("named_locks[resource-a] missing or wrong limit: %#v", siblings.NamedLocks)
	}

	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "demo"}, nil, siblings); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	pub, ok := cfg.Publishers.Publishers["ticker"]
	if !ok {
		t.Fatalf("publishers[ticker] not folded into synthetic rimsky.yml: %#v", cfg.Publishers.Publishers)
	}
	if pub.Endpoint != "127.0.0.1:9301" {
		t.Fatalf("publishers[ticker].endpoint: got %q, want 127.0.0.1:9301", pub.Endpoint)
	}
	nl, ok := cfg.NamedLocks.Locks["resource-a"]
	if !ok {
		t.Fatalf("named_locks[resource-a] not folded into synthetic rimsky.yml: %#v", cfg.NamedLocks)
	}
	if nl.Limit != 3 {
		t.Fatalf("named_locks[resource-a].limit: got %d, want 3", nl.Limit)
	}
}

func TestLoadSiblingBlocks_EmptyPathNoOp(t *testing.T) {
	siblings, err := LoadSiblingBlocks("")
	if err != nil {
		t.Fatalf("LoadSiblingBlocks(\"\"): %v", err)
	}
	if siblings != nil {
		t.Fatalf("LoadSiblingBlocks(\"\") = %#v, want nil", siblings)
	}
}

func TestWriteSyntheticSupervisorYAMLWithCallbackPort_PortRoundTrips(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-07-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	const wantPort = 0
	if err := WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, wantPort); err != nil {
		t.Fatalf("WriteSyntheticSupervisorYAMLWithCallbackPort: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(runDir, "supervisor.yml"))
	if err != nil {
		t.Fatalf("read written supervisor.yml: %v", err)
	}
	var probe struct {
		Callback struct {
			Host          string `yaml:"host"`
			Port          int    `yaml:"port"`
			AdvertiseHost string `yaml:"advertise_host"`
		} `yaml:"callback"`
	}
	if err := yaml.Unmarshal(body, &probe); err != nil {
		t.Fatalf("yaml round-trip: %v\n%s", err, string(body))
	}
	if probe.Callback.Port != wantPort {
		t.Fatalf("callback.port: got %d, want %d\n%s", probe.Callback.Port, wantPort, string(body))
	}
	if probe.Callback.Host != "0.0.0.0" {
		t.Fatalf("callback.host: got %q, want %q\n%s", probe.Callback.Host, "0.0.0.0", string(body))
	}
	if probe.Callback.AdvertiseHost != "127.0.0.1" {
		t.Fatalf("callback.advertise_host: got %q, want %q\n%s", probe.Callback.AdvertiseHost, "127.0.0.1", string(body))
	}
}

func TestWriteSyntheticSupervisorYAML_MatchesBakedDefault(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-03-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	if err := WriteSyntheticSupervisorYAML(runDir); err != nil {
		t.Fatalf("WriteSyntheticSupervisorYAML: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(runDir, "supervisor.yml"))
	if err != nil {
		t.Fatalf("read written supervisor.yml: %v", err)
	}
	baked := filepath.Join(repoRoot(t), "dockerfiles", "all-in-one.supervisor-config.yml")
	want, err := os.ReadFile(baked)
	if err != nil {
		t.Fatalf("read baked supervisor-config file %q: %v", baked, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("synthetic supervisor.yml differs from baked %q\n--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
			baked, len(got), string(got), len(want), string(want))
	}
}
